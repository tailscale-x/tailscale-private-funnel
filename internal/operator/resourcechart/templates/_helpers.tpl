{{- define "funnel.controllerName" -}}
{{- default (printf "tailscale-private-funnel.io/controller/%s/%s" .Release.Namespace .Release.Name) .Values.controller.name -}}
{{- end -}}

{{- define "funnel.namespaceIsolation" -}}
{{- if not (has .Values.component (list "control" "loadbalancer" "k8s-gateway")) -}}{{ fail "Unknown Funnel component" }}{{- end -}}
{{- if or (gt (len .Values.installation) 31) (not (regexMatch "^[a-z0-9]([a-z0-9-]*[a-z0-9])?$" .Values.installation)) -}}{{ fail "installation must be a DNS label of at most 31 characters" }}{{- end -}}
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
