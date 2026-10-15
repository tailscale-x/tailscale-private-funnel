package web

import (
	"fmt"
	"net/http"
	"regexp"
	"strings"
)

func controlHelmScript(installation, hostname, tailnet, image string) string {
	return fmt.Sprintf(`#!/bin/sh
set -eu
umask 077
namespace=%s
chart='oci://ghcr.io/tailscale-x/charts/tailscale-private-funnel'
chart_version=%s
helm show chart "$chart" --version "$chart_version" >/dev/null
# Create these protected OAuth files first. Never put their contents in Helm values.
oauth_id_file="$HOME/.config/tailscale-private-funnel/ts-client-id"
oauth_secret_file="$HOME/.config/tailscale-private-funnel/ts-client-secret"
test -r "$oauth_id_file" && test -r "$oauth_secret_file"
kubectl create namespace "$namespace" --dry-run=client -o yaml | kubectl apply -f -
kubectl label namespace "$namespace" tailscale-private-funnel.io/installation=%s --overwrite=false
kubectl -n "$namespace" get secret control-identity >/dev/null 2>&1 || kubectl -n "$namespace" create secret generic control-identity
kubectl -n "$namespace" create secret generic control-bootstrap --from-file=client-id="$oauth_id_file" --from-file=client-secret="$oauth_secret_file" --dry-run=client -o yaml | kubectl apply -f -
helm upgrade --install control "$chart" --version "$chart_version" --namespace "$namespace" --set-string component=control --set-string installation=%s --set-string image.reference=%s --set-string bootstrap.existingSecret=control-bootstrap --set-string control.hostname=%s --set-string control.tailnet=%s --wait --timeout 5m
kubectl -n "$namespace" delete secret control-bootstrap
kubectl -n "$namespace" logs deployment/control-control -c control --tail=30
# Open the tailnet-only control URL printed in these logs. Keep the PVC and encryption key.
`, shellQuote("tailscale-private-funnel-"+installation+"-system"), shellQuote(helmChartVersion(image)), shellQuote(installation), shellQuote(installation), shellQuote(image), shellQuote(hostname), shellQuote(tailnet))
}
func (c *Control) kubernetesControlInstructions(w http.ResponseWriter, r *http.Request) {
	if !requestAccess(r).Admin {
		http.Error(w, "administrator required", 403)
		return
	}
	installation := strings.TrimSpace(r.URL.Query().Get("installation"))
	if installation == "" {
		installation = "main"
	}
	hostname := strings.TrimSpace(r.URL.Query().Get("hostname"))
	if hostname == "" {
		hostname = "funnel-control-" + installation
	}
	var settings map[string]any
	_ = c.Store.GetJSON("tailnet", &settings)
	tailnet := stringValue(settings, "name", "")
	if v := r.URL.Query().Get("tailnet"); v != "" {
		tailnet = v
	}
	if len(installation) > 31 || !regexp.MustCompile(`^[a-z0-9]([a-z0-9-]*[a-z0-9])?$`).MatchString(installation) || len(hostname) > 63 || !regexp.MustCompile(`^[a-z0-9]([a-z0-9-]*[a-z0-9])?$`).MatchString(hostname) || tailnet == "" {
		http.Error(w, "valid installation, hostname and tailnet required", 400)
		return
	}
	version := c.BuildVersion
	if version == "" || version == "dev" {
		version = defaultRelease
	}
	writeJSON(w, map[string]string{"script": controlHelmScript(installation, hostname, tailnet, "ghcr.io/tailscale-x/tailscale-private-funnel:"+version)})
}
func (c *Control) kubernetesControlPage(w http.ResponseWriter, r *http.Request) {
	if !requestAccess(r).Admin {
		http.Error(w, "administrator required", 403)
		return
	}
	var settings map[string]any
	_ = c.Store.GetJSON("tailnet", &settings)
	render(w, "Kubernetes control", pageIntro("Infrastructure", "Deploy control on Kubernetes", "Create an independent, tailnet-only control installation. Moving existing control data requires a stopped-state migration; never run two copies of the same identity.")+`<section class="pf-surface"><form id="control-helm" class="pf-form-grid"><label class="pf-field"><span class="pf-label">Installation</span><input class="pf-input" name="installation" value="main" maxlength="31" required></label><label class="pf-field"><span class="pf-label">Control hostname (optional)</span><input class="pf-input" name="hostname" placeholder="funnel-control-main"></label><label class="pf-field"><span class="pf-label">Tailnet</span><input class="pf-input" name="tailnet" value="`+safeText(stringValue(settings, "name", ""))+`" required></label><div class="pf-form-actions"><button class="pf-button">Generate Helm instructions</button></div></form><p class="pf-help">Requires Helm, kubectl, a persistent StorageClass, and protected Tailscale OAuth files. OAuth needs auth_keys, devices:core, services and users:read. Use the existing shared tag and static grant; Funnel does not modify ACLs. Only the init container mounts the bootstrap Secret. The database and encrypted credential values stay on control’s dedicated PVC.</p><p id="control-helm-error" role="alert"></p><pre class="pf-command" id="control-helm-result">Configure this installation to generate commands.</pre><button class="pf-secondary-button" id="control-helm-copy" type="button">Copy instructions</button></section><script>document.getElementById('control-helm').onsubmit=async e=>{e.preventDefault();let r=await fetch('/api/kubernetes/control-install?'+new URLSearchParams(new FormData(e.target)));if(!r.ok){document.getElementById('control-helm-error').textContent=await r.text();return}document.getElementById('control-helm-error').textContent='';document.getElementById('control-helm-result').textContent=(await r.json()).script};document.getElementById('control-helm-copy').onclick=async function(){const text=document.getElementById('control-helm-result').textContent;try{if(navigator.clipboard){await navigator.clipboard.writeText(text)}else{const input=document.createElement('textarea');input.value=text;document.body.append(input);input.select();if(!document.execCommand('copy'))throw Error();input.remove()}this.textContent='Copied'}catch(_){this.textContent='Select the instructions and copy them'}}</script>`)
}
