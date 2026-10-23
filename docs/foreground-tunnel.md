# Foreground local HTTP tunnels

**Requires v4.1.0 or newer** on control, load balancers and the CLI.
Supported targets are Linux amd64/arm64 and macOS arm64.

```sh
tailscale-private-funnel tunnel 8080 \
  --hostname app.example.com \
  --control-url http://CONTROL-SERVICE.YOUR-TAILNET.ts.net:8080
```

Run on the backend machine and keep the terminal open. No Docker, systemd,
launchd, installed service, manually supplied auth key or OAuth file is needed.

## Default: phone home, then join separately

1. Use the already-connected host Tailscale daemon to reach private control.
2. Control verifies the caller with WhoIs. Any verified tailnet peer allowed to
   reach control may request bootstrap; no Funnel management role or host tag is
   required.
3. Control issues a preauthorized, **single-use, ephemeral auth key valid for
   15 minutes**, carrying only this installation's Tailnet gateway role. Callers
   cannot select another role or tags. OAuth credentials remain in control.
4. Start a separate embedded tsnet instance with an in-memory identity store and
   discard the key after joining. The host daemon is never joined, retagged,
   renamed, logged out or modified.
5. Register through normal role-tag/WhoIs authorization. Hostname policy, backend
   validation, fresh reports, Service readiness, load-balancer acknowledgment and
   exact DNS ownership still govern publication.

The 15 minutes is the join window, not the maximum lifetime of a running tunnel.
Every invocation requests a new key. This is not a reusable enrollment credential.
Issuance is limited to five requests per verified device per minute and thirty
per installation per minute. Failed issuance counts against the budget; counters
survive control restarts. Keys and OAuth secrets never appear in command output,
UI instructions, saved metadata or service units.

A machine without an existing Tailscale connection cannot phone home to a private
control. This command does not introduce a public bootstrap endpoint.

### Trust boundary and policy

Allowing bootstrap means any reachable tailnet peer can obtain this installation's
Tailnet gateway runtime authority. It does **not** grant human viewer/operator/admin
permission. Device-role tags remain shared authority rather than per-process
sandbox credentials. Exposure policy determines which hostnames may be published.

The generated ACL template allows tailnet peers (`*`) to reach only the control
Service's configured TCP port. Management RPCs retain their separate authorization.
Application Service connectivity remains restricted to the installation's load
balancer role. Review and merge the template yourself: Funnel never applies ACLs.
Different installation role tags do not restrict who may request phone-home bootstrap
if that peer can reach those controls.

## Backend and packet path

The default backend is `127.0.0.1:8080`; use `--local-address ::1` for IPv6.
Only literal loopback addresses are allowed. Start the backend before the tunnel.

```text
Bootstrap: host user-owned Tailscale → private control → temporary key
Traffic:   public HTTPS → Caddy → application Service
                                → embedded ephemeral tsnet → loopback HTTP
```

Caddy remains the only HTTP proxy. The tunnel relays raw TCP, preserving streaming
and WebSockets. First-visit ACME issuance still needs public DNS and reachable
load-balancer ports. Ready means control publication checks passed, not that an
ACME certificate has already been issued.

## Explicit host reuse

```sh
tailscale-private-funnel tunnel 8080 --hostname app.example.com \
  --control-url http://CONTROL-SERVICE.YOUR-TAILNET.ts.net:8080 --tailscale host
```

This optional mode requests no key. It requires an already-connected host carrying
`tag:tailscale-private-funnel-<installation>-tailnet-gateway`, LocalAPI permissions
for Service forwarding, and Service auto-approval. It changes only exact owned
Service entries. Shared-daemon components run as the same OS user for protected
locking. Prefer embedded mode on a personal laptop to retain user-owned identity.
`--tailscale-socket /absolute/path` selects the bootstrap/borrowed daemon in either mode.

## UI and lifecycle

**Gateways → Expose a local port** generates the same secret-free command.
The management UI keeps its normal access requirements; peers without UI access
can run the CLI directly. Choose
Embedded (recommended) or Host. Generating instructions creates no node, key or
DNS record; bootstrap begins when the command runs. `--output json` emits NDJSON
progress, including `connecting`, `bootstrapping`, `waiting`, `ready`, and `stopped`.

Ctrl+C closes listeners, asks control to retire this tunnel and logs out only its
embedded identity. In host mode it removes only its owned Service entry instead.
DNS and unused-Service cleanup are asynchronous. Existing connections may be
interrupted. Failed cleanup is reported; the host identity and unrelated Serve
configuration remain unchanged.

After a crash, report freshness expiry (five minutes by default), an expired
instance lease and the next cleanup pass retire publication. Offline ephemeral
devices are eventually removed by Tailscale. Restart requires fresh bootstrap;
ordinary gateway and load-balancer identity behavior is unchanged.

Nonsecret component metadata preserves exposure ownership across clean restarts:

- Linux: `$XDG_STATE_HOME/tailscale-private-funnel/tunnels/…`, defaulting to
  `~/.local/state/tailscale-private-funnel/tunnels/…`.
- macOS: `~/Library/Application Support/tailscale-private-funnel/tunnels/…`.

Tailscale identity is memory-backed, with temporary runtime files removed at exit.
No auth-key file or `tailscaled.state` is saved in component metadata. `--name`
selects a local handle. Saved operational settings cannot change in place; use a
new name for another assignment. Active competing hostname owners are rejected.

## Verification

Unit/storage contracts cover untagged peers, absence of human privileges, fixed-role
single-use ephemeral issuance, managed-zone validation, rate-limit transactions,
policy rejection, ownership conflicts, lease expiry and scoped cleanup. Opt-in
`TestLiveMacPhoneHome` uses real WhoIs and Tailscale Services with isolated control,
synthetic DNS, and an external untagged Mac runner. See the verification results
below; a cross-build alone does not prove Mac forwarding.

Release publication does not update existing deployments. See the
[v4.1.0 upgrade notes](releases/v4.1.0.md) before using the new command.

### Mac acceptance, 2026-10-19

Verified on a real Apple Silicon Mac with its existing **untagged** Tailscale GUI
identity (1.102.3), using the development Darwin ARM64 binary and an isolated
WhoIs-authenticated control with synthetic DNS:

- Phone-home key issuance and separate ephemeral registration, without a host tag
  or Funnel administrator grant.
- Automatically approved application Service, full 1,040,014-byte SSE response
  including `[DONE]`, WebSocket exchange and a 35-second stream across report ticks.
- Normal termination, tunnel retirement and complete owned fixture-ledger cleanup.
- Existing host device ID, absent tags and empty Serve configuration preserved.
  Metadata contained no auth key or `tailscaled.state`.

The first fixture identified a fresh-Service discovery problem for a client that
was already running before advertisement. New tunnels now target the canonical
Service FQDN; embedded transport resolves tailnet names through its own LocalAPI,
then calls the same server's Dial using Tailscale's dual-family racing helper.
It preserves the SDK HTTP transport's keep-alive/protocol settings and does not
use OS DNS or a different host identity for these names. An explicit LocalAPI
socket also disables macOS GUI fallback, so another daemon cannot be selected.

**Remaining limitation:** the existing-client run still needed several minutes
for initial Service reachability before passing. Do not interpret automatic host
approval or instruction generation as immediate public readiness. This test used
real Tailscale networking but synthetic DNS and simulated load-balancer reports;
it did not publish a public hostname or validate new public ACME issuance.
Live control, public routing and ACL policy were not changed.
