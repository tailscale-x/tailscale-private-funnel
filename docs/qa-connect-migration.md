# Connect migration verification

## Scope

Development migration after v2.0.0. No stable tag or GitHub release. Builds ran
on the development machine. The isolated acceptance described below was followed by the fleet upgrade and
control/native host migration documented in [live rollout](qa-connect-live-rollout.md).

## Automated gates

- `go test ./...`, `go test -tags caddy ./...`, and `go vet ./...`: passed.
- Generated Go, TypeScript, OpenAPI and CLI documentation rebuilt from protobuf
  descriptors; strict inputs, permission metadata and signed-model checks covered.
- UI typecheck/build and repeat asset SHA-256 comparison: passed.
- Helm package lint for control, loadbalancer and Kubernetes gateway: passed.
- Static Linux executable and distroless image startup: passed.
- `git diff --check`: passed.

## Real isolated integration

An OAuth-bootstrapped native control and two-node disposable k3d installation
used the generated management CLI, published development OCI chart and image.
`control provision apply` created enrollment, installed the controller and both
forwarders, and waited for verified readiness. Cloudflare configuration, read
testing and domain discovery ran through the CLI/control service; no public DNS
record was manually created. Provider credentials remained encrypted.

Two independent forwarder identities advertised one application Service. An HTTP
request through its TailVIP reached a local backend. Removing the server node's
backend withdrew that contribution while the agent backend remained available;
restoring the backend recovered it. A newly enrolled-child cache race found in
this test was fixed and regression-tested. The forwarder cannot read Kubernetes
Secrets; bootstrap credentials were removed and absent from persisted identity
Secrets and Helm values. Native control restarts preserved its identity and data.

Public DNS/ACME and WAN client-IP acceptance are not new acceptance claims for
this migration. Existing public Voice and harness health endpoints were checked
without changing their live v2.0.0 deployment.

## Browser checks

Chrome DevTools MCP inspected synthetic data at 1440×1024 and 390×844. The Browser
plugin was unavailable. Dashboard, inventories, providers, zones, policies,
access, settings, exposures, operations and provisioning fit both viewports.
There were no console errors. Checks exercised inline credential-preserving
rename, zone selection, multi-pattern policy editing, selected gateway IDs,
policy testing, enrollment review/edit preservation and mobile navigation.
Viewer provider pages expose no mutation controls; server-side role tests remain
the authorization gate. Screenshots contain only synthetic data.

## Remaining limitations

- Fleet rollout is recorded separately in [live rollout](qa-connect-live-rollout.md).
- Development publication is Linux amd64; multi-architecture release publication
  remains configured in CI and is not claimed as exercised here.
- npm reports advisories in the build-time Tailwind toolchain. Node and its
  dependencies are not shipped in the executable/runtime image; this is not an
  audit-clean claim.
- Authored server-interface source shrank; overall authored code grew with
  executable provisioning and management CLI. See the separate source metrics.

## Final artifact and cleanup

Development image `ghcr.io/tailscale-x/tailscale-private-funnel:dev-connect-20261017-final2`:
`sha256:444f51f846b1df32ee72c982d3041d652a5332b2ecd4198428d3c77c75e2c840`.
OCI chart `ghcr.io/tailscale-x/charts/tailscale-private-funnel:0.0.0-dev-connect-20261017-final2`:
`sha256:5ddf23842bed4e838b2de6df7d02cdf0603aedd7362dac5ef90e032f7f6eb759`.

The final OCI upgrade preserved the controller and both child identities. After
reports and Service approval recovered, an HTTP TailVIP request succeeded.
Generated contracts/docs and UI assets were regenerated twice with identical
hashes; Buf lint and formatting passed. Test nodes were revoked through control,
the test Service and devices were removed, and the disposable cluster was
deleted. The test zone's ownership ledger was empty. No DNS mutation bypassed control and no stable release was performed.
