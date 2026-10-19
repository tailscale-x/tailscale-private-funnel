{{- define "funnel.controllerName" -}}
{{- default (printf "tailscale-private-funnel.io/controller/%s/%s" .Release.Namespace .Release.Name) .Values.controller.name -}}
{{- end -}}

{{- define "funnel.namespaceIsolation" -}}
{{- if not (has .Values.component (list "control" "loadbalancer" "k8s-gateway")) -}}{{ fail "Unknown Funnel component" }}{{- end -}}
{{- if or (gt (len .Values.installation) 24) (not (regexMatch "^[a-z0-9]([a-z0-9-]*[a-z0-9])?$" .Values.installation)) -}}{{ fail "installation must be a DNS label of at most 24 characters" }}{{- end -}}
{{- if and (eq .Values.component "k8s-gateway") (ne (int .Values.controller.replicas) 1) -}}{{ fail "Kubernetes controllers require exactly one replica" }}{{- end -}}
{{- if and (eq .Values.component "control") (ne (int .Values.control.replicas) 1) -}}{{ fail "Control supports one replica per persistent identity" }}{{- end -}}
{{- if and (eq .Values.component "loadbalancer") (ne (int .Values.loadbalancer.replicas) 1) -}}{{ fail "Load balancers support one replica per enrolled release" }}{{- end -}}
{{- $namespace := lookup "v1" "Namespace" "" .Release.Namespace -}}
{{- if $namespace -}}
{{- $owner := index ($namespace.metadata.labels | default dict) "tailscale-private-funnel.io/installation" -}}
{{- if and $owner (ne $owner .Values.installation) -}}{{ fail "Namespace belongs to a different Funnel installation" }}{{- end -}}
{{- end -}}
{{- range $kind := list "Deployment" "StatefulSet" -}}
{{- range (lookup "apps/v1" $kind $.Release.Namespace "").items -}}
{{- if and (eq (index .metadata.labels "app.kubernetes.io/part-of") "tailscale-private-funnel") (ne (index .metadata.labels "app.kubernetes.io/instance") $.Release.Name) -}}
{{- if ne (index .metadata.labels "tailscale-private-funnel.io/installation") $.Values.installation -}}{{ fail "Different Funnel installations must use separate namespaces" }}{{- end -}}
{{- end -}}
{{- end -}}
{{- end -}}
{{- end -}}

{{- define "funnel.runtimeEnvironment" -}}
- name: POD_NAMESPACE
  valueFrom: {fieldRef: {fieldPath: metadata.namespace}}
- {name: FUNNEL_ROUTING_ID, value: {{ default .Values.componentID .Values.routingID | quote }}}
- {name: FUNNEL_COMPONENT_ID, value: {{ .Values.componentID | quote }}}
- {name: FUNNEL_CONTROL_URL, value: {{ .Values.managementURL | quote }}}
- {name: FUNNEL_RESOURCE_NAME, value: {{ .Release.Name | quote }}}
- {name: FUNNEL_RESOURCE_NAMESPACE, value: {{ .Release.Namespace | quote }}}
- {name: FUNNEL_DEPLOYMENT_TARGET, value: kubernetes}
- {name: FUNNEL_BOOTSTRAP_URL, value: "https://tailscale-private-funnel-k8s-operator-bootstrap.tailscale-private-funnel-k8s-operator-system.svc:9444"}
{{- end }}
{{- define "funnel.operatorBootstrapVolumes" -}}
- name: operator-token
  projected:
    defaultMode: 0440
    sources:
      - serviceAccountToken: {path: token, audience: tailscale-private-funnel/operator-bootstrap, expirationSeconds: 600}
- name: operator-ca
  configMap: {name: tailscale-private-funnel-operator-ca}
{{- end }}
