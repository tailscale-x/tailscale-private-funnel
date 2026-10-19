//go:build caddy

package proxy

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/caddyserver/caddy/v2"
	"github.com/caddyserver/caddy/v2/caddyconfig"
)

func TestDirectCaddySSECompletesHTTP1AndHTTP2AcrossReload(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		for i := 0; i < 512; i++ {
			fmt.Fprintf(w, "data: %d %s\n\n", i, strings.Repeat("x", 1024))
			w.(http.Flusher).Flush()
		}
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer backend.Close()
	sharedDialer.Lock()
	sharedDialer.dial = func(ctx context.Context, network, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, backend.Listener.Addr().String())
	}
	sharedDialer.Unlock()
	defer func() { sharedDialer.Lock(); sharedDialer.dial = nil; sharedDialer.Unlock() }()
	key, e := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if e != nil {
		t.Fatal(e)
	}
	cert := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "app.example.test"}, DNSNames: []string{"app.example.test"}, NotBefore: time.Now().Add(-time.Minute), NotAfter: time.Now().Add(time.Hour), KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	der, e := x509.CreateCertificate(rand.Reader, cert, cert, &key.PublicKey, key)
	if e != nil {
		t.Fatal(e)
	}
	encodedKey, e := x509.MarshalPKCS8PrivateKey(key)
	if e != nil {
		t.Fatal(e)
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: encodedKey})
	roots := x509.NewCertPool()
	roots.AppendCertsFromPEM(certPEM)
	socket, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	address := socket.Addr().String()
	socket.Close()
	file, e := caddyfile(":443", "", t.TempDir(), "", []Route{{Hostname: "app.example.test", Service: "100.64.0.1", Port: 8080}})
	if e != nil {
		t.Fatal(e)
	}
	raw, _, e := caddyconfig.GetAdapter("caddyfile").Adapt([]byte(file), nil)
	if e != nil {
		t.Fatal(e)
	}
	var root map[string]any
	if e = json.Unmarshal(raw, &root); e != nil {
		t.Fatal(e)
	}
	var selected map[string]any
	for _, v := range root["apps"].(map[string]any)["http"].(map[string]any)["servers"].(map[string]any) {
		s := v.(map[string]any)
		for _, listen := range s["listen"].([]any) {
			if listen == ":443" {
				selected = s
			}
		}
	}
	if selected == nil {
		t.Fatal("public server missing")
	}
	selected["listen"] = []string{address}
	selected["automatic_https"] = map[string]any{"disable": true}
	selected["tls_connection_policies"] = []any{map[string]any{}}
	root["admin"] = map[string]any{"disabled": true}
	root["apps"] = map[string]any{"http": map[string]any{"servers": map[string]any{"test": selected}}, "tls": map[string]any{"certificates": map[string]any{"load_pem": []any{map[string]any{"certificate": string(certPEM), "key": string(keyPEM)}}}}}
	raw, e = json.Marshal(root)
	if e != nil {
		t.Fatal(e)
	}
	defer caddy.Stop()
	for reload := 0; reload < 2; reload++ {
		if e = caddy.Load(raw, true); e != nil {
			t.Fatal(e)
		}
		for _, h2 := range []bool{false, true} {
			transport := &http.Transport{TLSClientConfig: &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}, ForceAttemptHTTP2: h2, DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
				return (&net.Dialer{}).DialContext(ctx, network, address)
			}}
			if !h2 {
				transport.TLSNextProto = map[string]func(string, *tls.Conn) http.RoundTripper{}
			}
			client := &http.Client{Transport: transport, Timeout: 10 * time.Second}
			response, e := client.Get("https://app.example.test/events")
			if e != nil {
				t.Fatal(e)
			}
			body, e := io.ReadAll(io.LimitReader(response.Body, 2<<20))
			response.Body.Close()
			transport.CloseIdleConnections()
			wantProto := 1
			if h2 {
				wantProto = 2
			}
			if e != nil || response.StatusCode != 200 || response.ProtoMajor != wantProto || strings.Count(string(body), "data:") != 513 || !strings.HasSuffix(string(body), "data: [DONE]\n\n") {
				t.Fatalf("reload=%d HTTP%d status=%d proto=%s bytes=%d error=%v", reload, wantProto, response.StatusCode, response.Proto, len(body), e)
			}
		}
	}
}
