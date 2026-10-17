#!/bin/sh
set -eu
cd "$(dirname "$0")/.."
go run sigs.k8s.io/controller-tools/cmd/controller-gen@v0.20.0 object crd rbac:roleName=tailscale-private-funnel-k8s-operator paths=./api/kubernetes/... paths=./internal/operator/... output:crd:artifacts:config=config/crd/bases output:rbac:artifacts:config=config/rbac
cp config/crd/bases/*.yaml charts/tailscale-private-funnel/crds/
go run ./cmd/generate-operator
