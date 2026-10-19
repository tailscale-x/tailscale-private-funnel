package kubebroker

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"time"

	core "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func serial() *big.Int {
	n, e := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if e != nil {
		panic(e)
	}
	return n
}
func issueLeaf(ca *x509.Certificate, key *ecdsa.PrivateKey, dns string) ([]byte, []byte, error) {
	leafKey, e := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if e != nil {
		return nil, nil, e
	}
	now := time.Now()
	leaf := &x509.Certificate{SerialNumber: serial(), DNSNames: []string{dns}, NotBefore: now.Add(-time.Minute), NotAfter: now.AddDate(1, 0, 0), ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}, KeyUsage: x509.KeyUsageDigitalSignature}
	der, e := x509.CreateCertificate(rand.Reader, leaf, ca, &leafKey.PublicKey, key)
	if e != nil {
		return nil, nil, e
	}
	pk, e := x509.MarshalPKCS8PrivateKey(leafKey)
	if e != nil {
		return nil, nil, e
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: pk}), nil
}
func EnsureTLS(ctx context.Context, c client.Client, ns, name, dns string, ownerUID ...string) (tls.Certificate, []byte, error) {
	var s core.Secret
	e := c.Get(ctx, types.NamespacedName{Namespace: ns, Name: name}, &s)
	if apierrors.IsNotFound(e) {
		key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if err != nil {
			return tls.Certificate{}, nil, err
		}
		now := time.Now()
		ca := &x509.Certificate{SerialNumber: serial(), Subject: pkix.Name{CommonName: name}, NotBefore: now.Add(-time.Minute), NotAfter: now.AddDate(10, 0, 0), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature}
		der, err := x509.CreateCertificate(rand.Reader, ca, ca, &key.PublicKey, key)
		if err != nil {
			return tls.Certificate{}, nil, err
		}
		pk, err := x509.MarshalPKCS8PrivateKey(key)
		if err != nil {
			return tls.Certificate{}, nil, err
		}
		cert, leafKey, err := issueLeaf(ca, key, dns)
		if err != nil {
			return tls.Certificate{}, nil, err
		}
		s = core.Secret{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns, Labels: ownedRuntimeLabels()}, Data: map[string][]byte{"ca.crt": pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), "ca.key": pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: pk}), "tls.crt": cert, "tls.key": leafKey}}
		if len(ownerUID) > 0 {
			s.Labels["tailscale-private-funnel.io/owner-uid"] = ownerUID[0]
		}
		e = c.Create(ctx, &s)
		if apierrors.IsAlreadyExists(e) {
			e = c.Get(ctx, types.NamespacedName{Namespace: ns, Name: name}, &s)
		}
	}
	if e != nil {
		return tls.Certificate{}, nil, e
	}
	if len(ownerUID) > 0 && s.Labels["tailscale-private-funnel.io/owner-uid"] != ownerUID[0] {
		return tls.Certificate{}, nil, fmt.Errorf("TLS Secret belongs to another owner")
	}
	pair, e := tls.X509KeyPair(s.Data["tls.crt"], s.Data["tls.key"])
	if e != nil {
		return pair, nil, e
	}
	cert, e := x509.ParseCertificate(pair.Certificate[0])
	if e != nil {
		return pair, nil, e
	}
	if cert.NotAfter.Before(time.Now().Add(30 * 24 * time.Hour)) {
		block, _ := pem.Decode(s.Data["ca.crt"])
		keyBlock, _ := pem.Decode(s.Data["ca.key"])
		if block == nil || keyBlock == nil {
			return pair, nil, fmt.Errorf("broker CA key missing; explicit certificate rotation required")
		}
		ca, e := x509.ParseCertificate(block.Bytes)
		if e != nil {
			return pair, nil, e
		}
		key, e := x509.ParsePKCS8PrivateKey(keyBlock.Bytes)
		if e != nil {
			return pair, nil, e
		}
		ecdsaKey, ok := key.(*ecdsa.PrivateKey)
		if !ok {
			return pair, nil, fmt.Errorf("unsupported CA key")
		}
		s.Data["tls.crt"], s.Data["tls.key"], e = issueLeaf(ca, ecdsaKey, dns)
		if e != nil {
			return pair, nil, e
		}
		if e = c.Update(ctx, &s); e != nil {
			return pair, nil, e
		}
		pair, e = tls.X509KeyPair(s.Data["tls.crt"], s.Data["tls.key"])
	}
	return pair, s.Data["ca.crt"], e
}