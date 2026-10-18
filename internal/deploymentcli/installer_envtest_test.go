package deploymentcli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	funnel "github.com/tailscale-x/tailscale-private-funnel/api/kubernetes/v1alpha1"
	v1 "github.com/tailscale-x/tailscale-private-funnel/internal/gen/funnel/v1"
	"github.com/tailscale-x/tailscale-private-funnel/internal/provision"
	apps "k8s.io/api/apps/v1"
	core "k8s.io/api/core/v1"
	apiext "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
)

// Tests the execution boundary against a real Kubernetes API. The private
// control URL is intentionally unreachable: only the future workload needs it.
func TestInstallerKubernetesAPIBoundary(t *testing.T) {
	if os.Getenv("KUBEBUILDER_ASSETS") == "" && os.Getenv("PF_INSTALLER_K3D_CONTEXT") == "" {
		t.Skip("requires envtest assets or an isolated k3d context")
	}
	contextName := os.Getenv("PF_INSTALLER_K3D_CONTEXT")
	if contextName == "" {
		env := &envtest.Environment{CRDs: []*apiext.CustomResourceDefinition{{ObjectMeta: metav1.ObjectMeta{Name: "gatewayclasses.gateway.networking.k8s.io", Annotations: map[string]string{"api-approved.kubernetes.io": "unapproved, installer-test-fixture-only"}}, Spec: apiext.CustomResourceDefinitionSpec{Group: "gateway.networking.k8s.io", Scope: apiext.ClusterScoped, Names: apiext.CustomResourceDefinitionNames{Plural: "gatewayclasses", Kind: "GatewayClass"}, Versions: []apiext.CustomResourceDefinitionVersion{{Name: "v1", Served: true, Storage: true, Schema: &apiext.CustomResourceValidation{OpenAPIV3Schema: &apiext.JSONSchemaProps{Type: "object"}}}}}}}, CRDDirectoryPaths: []string{filepath.Join("..", "..", "config", "crd", "bases")}, ErrorIfCRDPathMissing: true}
		cfg, err := env.Start()
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if err := env.Stop(); err != nil {
				t.Error(err)
			}
		})
		contextName = "installer-envtest"
		kc := clientcmdapi.Config{Clusters: map[string]*clientcmdapi.Cluster{"fixture": {Server: cfg.Host, CertificateAuthorityData: cfg.CAData}}, AuthInfos: map[string]*clientcmdapi.AuthInfo{"fixture": {ClientCertificateData: cfg.CertData, ClientKeyData: cfg.KeyData}}, Contexts: map[string]*clientcmdapi.Context{contextName: {Cluster: "fixture", AuthInfo: "fixture"}}}
		path := filepath.Join(t.TempDir(), "kubeconfig")
		if err = clientcmd.WriteToFile(kc, path); err != nil {
			t.Fatal(err)
		}
		t.Setenv("KUBECONFIG", path)
	}
	ns := "funnel-installer-test"
	k, err := kubeClient(Flags{Context: contextName})
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{ns, OperatorNamespace} {
		if err = k.Create(t.Context(), &core.Namespace{ObjectMeta: metav1.ObjectMeta{Name: name}}); err != nil {
			t.Fatal(err)
		}
		if os.Getenv("PF_INSTALLER_K3D_CONTEXT") != "" {
			name := name
			t.Cleanup(func() { _ = k.Delete(context.Background(), &core.Namespace{ObjectMeta: metav1.ObjectMeta{Name: name}}) })
		}
	}
	// This fixture validates installer gating, not the operator's own readiness.
	op := &apps.Deployment{ObjectMeta: metav1.ObjectMeta{Name: "tailscale-private-funnel-k8s-operator", Namespace: OperatorNamespace}, Spec: apps.DeploymentSpec{Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"fixture": "operator"}}, Template: core.PodTemplateSpec{ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"fixture": "operator"}}, Spec: core.PodSpec{Containers: []core.Container{{Name: "fixture", Image: "busybox:1.36.1", Command: []string{"sh", "-c", "sleep 3600"}, Args: []string{"--managed-namespace", ns}}}}}}}
	if err = k.Create(t.Context(), op); err != nil {
		t.Fatal(err)
	}
	if os.Getenv("PF_INSTALLER_K3D_CONTEXT") != "" {
		deadline := time.Now().Add(2 * time.Minute)
		for time.Now().Before(deadline) {
			if err = k.Get(t.Context(), types.NamespacedName{Namespace: OperatorNamespace, Name: op.Name}, op); err != nil {
				t.Fatal(err)
			}
			if op.Status.ReadyReplicas == 1 {
				break
			}
			time.Sleep(time.Second)
		}
		if op.Status.ReadyReplicas != 1 {
			t.Fatal("fixture operator never ready")
		}
	} else {
		op.Status.ReadyReplicas = 1
		op.Status.Replicas = 1
		if err = k.Status().Update(t.Context(), op); err != nil {
			t.Fatal(err)
		}
	}
	p, err := provision.Resolve(&v1.DeploymentRequest{Mode: "k8s-gateway", Target: "kubernetes", ResourceName: "site", Context: contextName, Namespace: ns, PlacementJson: []byte(`{"nodeSelector":{"pool":"fixture"}}`)}, "http://unreachable.private.invalid:8080", "dev-test")
	if err != nil {
		t.Fatal(err)
	}
	p.Credentials = &v1.BootstrapCredentials{AuthKey: "fixture-secret", EnrollmentId: "fixture", EnrollmentCode: "fixture-proof", ControlKey: "fixture-control-public-key", ExpiresAt: time.Now().Add(time.Minute).Unix()}
	run := func() error {
		b, _ := json.Marshal(p)
		cmd := Installer("kubernetes")
		cmd.SetIn(bytes.NewReader(b))
		cmd.SetArgs(nil)
		var out bytes.Buffer
		cmd.SetOut(&out)
		cmd.SetErr(&out)
		err := cmd.ExecuteContext(t.Context())
		if strings.Contains(out.String(), "fixture-secret") || strings.Contains(out.String(), "fixture-proof") {
			t.Fatal("secret in installation output")
		}
		return err
	}
	if err = run(); err != nil {
		t.Fatal(err)
	}
	var got funnel.FunnelGateway
	if err = k.Get(t.Context(), types.NamespacedName{Namespace: ns, Name: "site"}, &got); err != nil {
		t.Fatal(err)
	}
	if got.Spec.ManagementURL != p.ManagementUrl || got.Spec.Placement.NodeSelector["pool"] != "fixture" {
		t.Fatal("lost configuration")
	}
	b, _ := json.Marshal(got)
	if strings.Contains(string(b), "fixture-secret") || strings.Contains(string(b), "fixture-proof") {
		t.Fatal("credentials in CR")
	}
	var secret core.Secret
	if err = k.Get(t.Context(), types.NamespacedName{Namespace: ns, Name: "site-bootstrap"}, &secret); err != nil {
		t.Fatal(err)
	}
	if string(secret.Data["auth-key"]) != "fixture-secret" || len(secret.OwnerReferences) != 1 || secret.OwnerReferences[0].UID != got.UID {
		t.Fatal("bootstrap not owned by exact CR")
	}
	if err = run(); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatal("conflicting resource accepted", err)
	}
}
