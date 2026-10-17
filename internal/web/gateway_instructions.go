package web

import (
	"strings"
)

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
	if d.ResourceName == "" {
		d.ResourceName = d.InstallationName + "-gateway"
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
