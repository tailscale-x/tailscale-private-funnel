# API, CLI, and deployment instructions

The protobuf contracts under `api/funnel/v1` generate Go/TypeScript Connect
clients, handlers, OpenAPI, RPC reference, CLI flags, and agent documentation.
Run `scripts/generate-rpc.sh` to refresh the generated contract references.
The authorized `/llms.txt` handler adds live control settings, matching release
binary downloads, checksum/install guidance, and CLI examples on every request.
Stable builds link their exact release; development builds explicitly identify
the latest-stable download fallback. Documentation requests never mint credentials
or trust request Host headers. Node communication, brokers and management use
Connect over verified Tailscale identity or broker-scoped tokens.

## Command boundaries

- `native controls|gateways|loadbalancers`: named system services on this Linux host.
- `k8s operator`: singleton operator installation and lifecycle.
- `k8s controls|gateways|loadbalancers`: component CR creation and lifecycle.
- `manage`: application management through the authorized Funnel API.

Use standard help/completion offline. Kubernetes operations require explicit
`--context`. Use `--output json`, mutation `--dry-run`, deployment `--wait` and
`--timeout`, and `--yes` for deletion. Stop preserves native state and identity;
delete withdraws owned state; Tailscale tag/device removal revokes role authority. Runtime commands are hidden infrastructure
entrypoints and are not a public deployment API.

Public `create` generates an installer; it never installs, invokes SSH, or contacts
Kubernetes. Other lifecycle operations execute their action. See
[installer workflow](INSTALLER-WORKFLOW.md) for the connectivity matrix.

## Instructions versus execution

`DeploymentService.Instructions` is pure: it resolves settings and returns typed
commands, prerequisites and diagnostics. It never creates keys.
Both React and CLI use shared Go renderers. The browser does not build command
strings, upload kubeconfigs, or execute host/cluster commands.

`DeploymentService.Authorize` issues an exact-role, one-use key for embedded native installers. Bootstrap enters the target through protected stdin. Host reuse has no join secret; Kubernetes references one shared installation issuer Secret and receives fresh ephemeral keys through the operator TLS endpoint. OAuth stays server-side; creating an independent control uses
protected OAuth files on the target. UI instruction generation does not start
bootstrap expiry.

```sh
tailscale-private-funnel native gateways create site \
  --type tailnet --control-url http://CONTROL:8080 --version VERSION \
  --output script | ssh ubuntu@TARGET 'sh -s'
```

`create` performs no SSH connection. `--output instructions` is readable;
`script` emits only complete POSIX shell on stdout; `json` returns structured
instructions and script. Diagnostics use stderr. `--wait` applies to target
execution. Dry runs mint no credentials and emit a non-executable preview.

Native deployment handles are unique, immutable operational names. Editable
Unicode display names and immutable internal node/resource IDs are separate.
Inspection reads systemd/runtime status without opening another tsnet identity.
No Docker deployment target, public join/serve/reset/bootstrap/provision,
print-command, import/adoption/resume, or compatibility alias remains.

## Authorization and ownership

Management uses WhoIs and server-verified Tailscale roles/capabilities. Node
operations bind verified WhoIs roles to logical component instances; selectors
never supply identity or authority. Report freshness, inventory revisions and
configuration generations prevent stale writers. No component signatures or
client signing keys are required.
DNS mutations pass through control, preserving exact ledger ownership and
preview-bound overwrite approval. Revoking access does not prove deployment
resources were removed; deletion is a separate infrastructure action.

## Generation and verification

Pinned tools: buf v1.74.0, protoc-gen-go v1.36.11,
protoc-gen-connect-go v1.21.1, protoc-gen-es v2.16.0,
protoc-gen-connect-openapi v0.28.0, controller-tools v0.20.0.
`go test ./...`, `go test -tags caddy ./...`, `go vet ./...`, envtest,
Helm lint/template, generation drift checks, and image startup are required.
`scripts/code-surface.py` tracks authored source separately from generated and
bundled output. See [Kubernetes operator](KUBERNETES.md).

For an explicitly authorized tagged automation machine, first control creation
also accepts repeated `--administrator 'node:STABLE_TAILSCALE_NODE_ID'` selectors.
This is a first-bootstrap addition; `autogroup:owner` remains the default and
restarts never overwrite stored roles. Do not grant administrator access to a
shared workload tag. Later access changes use `manage access` or the web UI.
