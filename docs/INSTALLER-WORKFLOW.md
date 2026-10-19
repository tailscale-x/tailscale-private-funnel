# Generate an installer, execute it on the target

Public `create` only generates. Other lifecycle operations execute on the local
native host or the explicitly selected Kubernetes context. Use matching pinned
artifacts for the generator and target. UI commands are rendered by Go; the browser
never assembles shell scripts or mints keys.

## Embedded native deployment

Run on an authorized machine with private control access:

```sh
tailscale-private-funnel native gateways create site --type docker \
  --installation main --control-url http://CONTROL_SERVICE_FQDN:8080 \
  --version VERSION --output script | ssh ubuntu@TARGET 'sh -s'
```

The target needs Linux/systemd, internet connectivity, and Docker for Docker
discovery, but no host Tailscale installation. The installer passes a single-use,
15-minute role-tagged key through stdin. Embedded tsnet joins before contacting
control. The key is discarded; native identity persists. Treat generated embedded
installers as secrets. Expired or consumed keys require newly generated output.

## Existing host Tailscale

Select `--tailscale host`, optionally `--tailscale-socket PATH`. This installer has
no join secret. The host must already be connected with the correct installation
role tag and LocalAPI permissions. Funnel does not change the device's tags,
hostname, operator, login, routes, or unrelated Serve configuration.

## Kubernetes

```sh
tailscale-private-funnel k8s gateways create site --installation main \
  --context TARGET_CONTEXT --namespace tailscale-private-funnel-main-system \
  --key-issuer-secret installation-issuer \
  --control-url http://CONTROL_SERVICE_FQDN:8080 --version VERSION \
  --wait --output script > install-site.sh
```

Generation reads no kubeconfig and contacts no cluster. Execute with `sh` on a
machine that has the selected context and an installed operator managing that
namespace. The executor needs no tailnet route to control. The existing issuer
Secret holds installation-wide `auth_keys` OAuth credentials. Pod-bound audience tokens
and TLS authenticate key issuance; the operator derives the allowed role from
verified workload ownership. Every Pod receives its own ephemeral, single-use key.
No shared reusable key, bootstrap Secret, or identity restoration is used.

## Fresh control

Native control references separate protected target-side files for the installation
issuer, Service manager, and `users:read` client. Control requires embedded Tailscale:
tailscaled rejects PROXY v2 with Unix targets, so host-reused control fails closed.
Native load balancers and Docker/Tailnet gateways can reuse a connected role-tagged
daemon with appropriate LocalAPI permissions, without a joining credential.
Kubernetes control references `--key-issuer-secret`, `--services-secret`, and
`--users-secret`; credentials never enter CRs or Helm values.
See [role requirements](TAILSCALE-ROLES.md).

## Lifecycle

`--output instructions` is the readable default; `script` is executable POSIX shell
only; `json` is typed settings and an installer, with a temporary key only where
applicable. Dry runs mint no keys and produce non-executable previews. `--wait` and
`--timeout` apply on the execution target.

Native stop preserves identity and data. Kubernetes replacement intentionally
changes device identity while preserving application state. Disable/delete removes
owned Funnel resources; remove Tailscale role tags or devices to revoke authority.
Installer generation is not deployment success: inspect role registration, fresh
reports, backend health, and public readiness after execution.
