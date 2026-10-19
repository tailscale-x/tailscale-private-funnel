package operator

import (
	"encoding/json"
	"strings"
	"testing"

	funnel "github.com/tailscale-x/tailscale-private-funnel/api/kubernetes/v1alpha1"
	apps "k8s.io/api/apps/v1"
	core "k8s.io/api/core/v1"
	networking "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

func TestPostgresControlRendering(t *testing.T) {
	c := testControl()
	c.Spec.Database = funnel.ControlDatabase{Type: "postgres", Postgres: &funnel.PostgresDatabase{ConnectionSecretRef: core.SecretKeySelector{LocalObjectReference: core.LocalObjectReference{Name: "cnpg-app"}, Key: "uri"}, CASecretRef: &core.SecretKeySelector{LocalObjectReference: core.LocalObjectReference{Name: "cnpg-ca"}, Key: "ca.crt"}, EgressPeers: []networking.NetworkPolicyPeer{{PodSelector: &metav1.LabelSelector{MatchLabels: map[string]string{"cnpg.io/cluster": "pg"}}}}}}
	objects, e := Render(c)
	if e != nil {
		t.Fatal(e)
	}
	deployment, policy := false, false
	for _, o := range objects {
		data, e := json.Marshal(o)
		if e != nil {
			t.Fatal(e)
		}
		if o.GetObjectKind().GroupVersionKind().Kind == "PersistentVolumeClaim" {
			t.Fatal("PostgreSQL control must not allocate PVC")
		}
		if o.GetObjectKind().GroupVersionKind().Kind == "Deployment" {
			deployment = true
			var v apps.Deployment
			if e = json.Unmarshal(data, &v); e != nil {
				t.Fatal(e)
			}
			if *v.Spec.Replicas != 1 || v.Spec.Strategy.Type != apps.RecreateDeploymentStrategyType {
				t.Fatal("not singleton")
			}
			for _, container := range append(v.Spec.Template.Spec.InitContainers, v.Spec.Template.Spec.Containers...) {
				text := strings.Join(container.Args, " ")
				if !strings.Contains(text, "--database=postgres") || !strings.Contains(text, "--database-ca-file=/etc/funnel-database-ca/ca.crt") {
					t.Fatal(text)
				}
			}
			for _, volume := range v.Spec.Template.Spec.Volumes {
				if volume.PersistentVolumeClaim != nil {
					t.Fatal("unexpected PVC mount")
				}
			}
		}
		if o.GetObjectKind().GroupVersionKind().Kind == "NetworkPolicy" {
			policy = true
			if !strings.Contains(string(data), "cnpg.io/cluster") || !strings.Contains(string(data), "5432") {
				t.Fatal("missing scoped DB egress")
			}
		}
	}
	if !deployment || !policy {
		t.Fatal("missing resources")
	}
	// Cleanup must receive the same database mounts and options.
	r := &Reconciler{Scheme: runtime.NewScheme()}
	job, e := r.cleanupJob(c)
	if e != nil {
		t.Fatal(e)
	}
	if !strings.Contains(strings.Join(job.Spec.Template.Spec.Containers[0].Args, " "), "--database=postgres") {
		t.Fatal("cleanup lost database configuration")
	}
}

func TestPostgresCredentialsCannotBeUsedAsIdentity(t *testing.T) {
	c := testControl()
	c.Spec.Database = funnel.ControlDatabase{Type: "postgres", Postgres: &funnel.PostgresDatabase{ConnectionSecretRef: core.SecretKeySelector{LocalObjectReference: core.LocalObjectReference{Name: "control-material"}, Key: "uri"}}}
	if _, e := Render(c); e == nil {
		t.Fatal("would mutate database credential Secret as identity")
	}
}
