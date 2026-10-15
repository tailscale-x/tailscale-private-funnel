package web

import (
	"encoding/json"
	"fmt"
	"strings"
)

func helmEnrollmentScript(id, code, key, controlKey, url, hostname, image string, d nodeDeployment) string {
	d = resolveKubernetesDeployment(d)
	extra := map[string]any{"watchNamespaces": splitValues(d.WatchNamespaces), "installation": d.InstallationName, "component": "k8s-gateway"}
	var selector map[string]string
	if d.NodeSelector != "" {
		_ = json.Unmarshal([]byte(d.NodeSelector), &selector)
		extra["forwarder"] = map[string]any{"nodeSelector": selector}
	}
	values, _ := json.MarshalIndent(extra, "", "  ")
	return fmt.Sprintf(`#!/bin/sh
set -eu
umask 077
namespace=%s
installation=%s
release=%s
chart='oci://ghcr.io/tailscale-x/charts/tailscale-private-funnel'
chart_version=%s
# Check the matching published chart before creating resources or bootstrap credentials.
helm show chart "$chart" --version "$chart_version" >/dev/null
for watched_namespace in %s; do
  kubectl get namespace "$watched_namespace" >/dev/null || { echo "Create watched namespace $watched_namespace before installing" >&2; exit 1; }
done
kubectl apply --server-side -f https://github.com/kubernetes-sigs/gateway-api/releases/download/v1.5.1/standard-install.yaml
kubectl create namespace "$namespace" --dry-run=client -o yaml | kubectl apply -f -
kubectl label namespace "$namespace" tailscale-private-funnel.io/installation=%s --overwrite=false
bootstrap=$(mktemp -d)
trap 'rm -rf "$bootstrap"' EXIT HUP INT TERM
printf '%%s' %s > "$bootstrap/code"
printf '%%s' %s > "$bootstrap/auth-key"
printf '%%s' %s > "$bootstrap/enrollment-id"
printf '%%s' %s > "$bootstrap/control-key"
printf '%%s' %s > "$bootstrap/management-url"
printf '%%s' %s > "$bootstrap/hostname"
kubectl -n "$namespace" create secret generic "$release-bootstrap" --from-file="$bootstrap" --dry-run=client -o yaml | kubectl apply -f -
printf '%%s' %s > "$bootstrap/values.json"
helm upgrade --install "$release" "$chart" --version "$chart_version" --namespace "$namespace" --set-string image.reference=%s --set-string bootstrap.existingSecret="$release-bootstrap" --values "$bootstrap/values.json" --set-string gatewayClass=%s --set-string ingressClass=%s --wait --timeout 5m
kubectl -n "$namespace" get pods
# Controllers persist identity before readiness; forwarders verify signed control configuration.
kubectl -n "$namespace" delete secret "$release-bootstrap"
`, shellQuote(d.KubernetesNamespace), shellQuote(d.InstallationName), shellQuote(d.HelmRelease), shellQuote(helmChartVersion(image)), strings.Join(splitValues(d.WatchNamespaces), " "), shellQuote(d.InstallationName), shellQuote(code), shellQuote(key), shellQuote(id), shellQuote(controlKey), shellQuote(url), shellQuote(hostname), shellQuote(string(values)), shellQuote(image), shellQuote(d.GatewayClass), shellQuote(d.IngressClass))
}
func gatewayBackendExample(mode string) string {
	switch mode {
	case "docker-gateway":
		return "labels:\n  tailscale-private-funnel.expose: \"8080:app.example.com:http\"\n# Attach the target to a user-defined Docker network.\n# Removing the label withdraws the exposure."
	case "tailnet-gateway":
		return "Open Exposures → Expose a tailnet machine.\nChoose this gateway → backend machine → HTTP port → public hostname.\nCheck policy and test connectivity before saving."
	case "k8s-gateway":
		return `# Replace app.example.com with a hostname allowed by control policy.
apiVersion: gateway.networking.k8s.io/v1
kind: Gateway
metadata:
  name: private-funnel
spec:
  gatewayClassName: tailscale-private-funnel
  listeners:
    - name: http
      protocol: HTTP
      port: 80
---
apiVersion: gateway.networking.k8s.io/v1
kind: HTTPRoute
metadata:
  name: example
spec:
  parentRefs:
    - name: private-funnel
  hostnames: ["app.example.com"]
  rules:
    - backendRefs:
        - name: your-http-service
          port: 8080
---
# Alternative: use Ingress instead of HTTPRoute for this hostname.
apiVersion: networking.k8s.io/v1
kind: Ingress
metadata:
  name: example-ingress
spec:
  ingressClassName: tailscale-private-funnel
  rules:
    - host: other.example.com
      http:
        paths:
          - path: /
            pathType: Prefix
            backend:
              service:
                name: your-http-service
                port:
                  number: 8080`
	}
	return ""
}

func selectedGatewayExample(mode string, d nodeDeployment) string {
	example := gatewayBackendExample(mode)
	if mode == "k8s-gateway" {
		d = resolveKubernetesDeployment(d)
		if d.GatewayClass != "" {
			example = strings.ReplaceAll(example, "gatewayClassName: tailscale-private-funnel", "gatewayClassName: "+d.GatewayClass)
		}
		if d.IngressClass != "" {
			example = strings.ReplaceAll(example, "ingressClassName: tailscale-private-funnel", "ingressClassName: "+d.IngressClass)
		}
	}
	return example
}

// Helm OCI tags are SemVer without the release tag's leading v. Development
// images use a matching explicit prerelease chart rather than an unpinned latest.
func helmChartVersion(image string) string {
	tag := image[strings.LastIndex(image, ":")+1:]
	if strings.HasPrefix(tag, "dev") {
		return "0.0.0-" + tag
	}
	return strings.TrimPrefix(tag, "v")
}

func resolveKubernetesDeployment(d nodeDeployment) nodeDeployment {
	if d.InstallationName == "" {
		d.InstallationName = "main"
	}
	if d.KubernetesNamespace == "" {
		d.KubernetesNamespace = "tailscale-private-funnel-" + d.InstallationName + "-system"
	}
	if d.HelmRelease == "" {
		d.HelmRelease = d.InstallationName + "-gateway"
	}
	if d.GatewayClass == "" {
		d.GatewayClass = "tailscale-private-funnel"
		if d.InstallationName != "main" {
			d.GatewayClass += "-" + d.InstallationName
		}
	}
	if d.IngressClass == "" {
		d.IngressClass = d.GatewayClass
	}

	return d
}

func loadBalancerHelmScript(id, code, key, controlKey, url, hostname, image string, d nodeDeployment) string {
	script := helmEnrollmentScript(id, code, key, controlKey, url, hostname, image, d)
	script = strings.Replace(script, `kubectl -n "$namespace" create secret generic "$release-bootstrap"`, `kubectl -n "$namespace" get secret "$release-identity" >/dev/null 2>&1 || kubectl -n "$namespace" create secret generic "$release-identity"
kubectl -n "$namespace" create secret generic "$release-bootstrap"`, 1)
	script = strings.ReplaceAll(script, "kubectl apply --server-side -f https://github.com/kubernetes-sigs/gateway-api/releases/download/v1.5.1/standard-install.yaml\n", "")
	script = strings.Replace(script, `"component": "k8s-gateway"`, `"component": "loadbalancer"`, 1)
	script = strings.Replace(script, `printf '%s' `+shellQuote(hostname)+` > "$bootstrap/hostname"`, `printf '%s' `+shellQuote(hostname)+` > "$bootstrap/hostname"`+"\n"+`printf '%s' `+shellQuote(d.CertEmail)+` > "$bootstrap/cert-email"`+"\n"+`printf '%s' `+shellQuote(d.LoadBalancerHostname)+` > "$bootstrap/loadbalancer-hostname"`, 1)
	script = strings.Replace(script, " --wait --timeout 5m", " --timeout 5m\n"+`kubectl -n "$namespace" rollout status deployment/"$release-loadbalancer" --timeout=5m`, 1)
	script += `kubectl -n "$namespace" get service "$release-public"
# Wait for a routable public address in Service status. Private k3d addresses are not published.
`
	return script
}
