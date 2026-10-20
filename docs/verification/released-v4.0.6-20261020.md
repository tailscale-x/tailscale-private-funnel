# v4.0.6 released-build rollout — 2026-10-20

## Artifacts and checks

- Release: [v4.0.6](https://github.com/tailscale-x/tailscale-private-funnel/releases/tag/v4.0.6).
- Source commit: `4f0d1d5011127ffc83a39ec3e1438bc0acfd3bf3`.
- [Release CI](https://github.com/tailscale-x/tailscale-private-funnel/actions/runs/39457823174) passed every job: generated contracts/assets and operator checks, both Go suites, vet, PostgreSQL 16/18, native binaries, container startup, multi-architecture image, OCI chart, and release publication.
- Image: `ghcr.io/tailscale-x/tailscale-private-funnel@sha256:02d807039c655103adeffa9e0ab5441301141cef8edb4933c5163f5a436598cc`.
- Linux amd64 binary SHA256: `fefc4c36bc430c302fe3263e4d9ad5c7a78c77b85a1971a8619139fd0e9459ec`.
- Every downloaded release checksum passed. Anonymous OCI chart retrieval matched the release chart package. Version-only image checks ran on all nine cluster nodes; those Pods never joined Tailscale and were removed afterward.

The release contains the v4.0.5 report and Service-lifecycle fixes plus the
replacement control-host discovery policy. No existing tag was rewritten.

## Deployment and persistence

Only prebuilt released artifacts were deployed:

- Native public edge on xvps; native Docker gateways on Tiny and 5090.
- DLKN control, using its existing PostgreSQL database and material Secret.
- Kubernetes gateway agents, forwarder DaemonSets, and operators in DLKN,
  m090dev, and m090prd. GitOps sources retain the pinned release image/chart.

All workloads reached ready state at the released digest. All three native
executables matched the released checksum and retained their configuration and
Tailscale addresses. Ordinary Kubernetes replacement created disposable network
instances as designed; logical component/application bindings remained stable.

Post-rollout comparison against the pre-v4.0.5 baseline verified:

- All **11 DNS-ledger rows** and **44 exposure bindings** unchanged.
- Control material Secret UID and contents unchanged.
- Authenticated decryption confirmed the same stored credentials; provider and
  managed-zone assignments were unchanged. Random-nonce re-encryption on startup
  is not credential rotation.
- All eight monitored TLS certificate fingerprints unchanged, with valid chains.
- All 15 active reporting runtime instances had fresh reports and live leases.
- The DLKN public Gateway reported `Accepted=True` and `Programmed=True`.

No identity reset, database transfer, DNS adoption, or ownership reassignment was
performed. No edge recovery restart or extra diagnostic Tailscale client was
used during the Kubernetes host replacements.

## Approved policy and control replacement

The explicitly approved ICMP-only runtime-to-control peer-discovery grant was
applied with an ETag precondition. A semantic comparison verified exactly that
one additional grant and no other policy changes. It opens no TCP/UDP port and
changes no tag ownership, OAuth permissions, or Service auto-approval policy.

A real control Pod replacement, then the released control update, both produced
replacement control hosts visible to the already-running edge. The edge retained
its identity and connected through Tailscale before its planned binary update.
See [control replacement evidence](control-peer-discovery-20261020.md).

## Public acceptance and observed interruptions

Final checks passed through normal DNS and through the WAN address with genuine
hostname/TLS verification:

| Service | Checked result |
| --- | --- |
| Admissions frontend and API | HTTP 200 |
| Admissions SSO | HTTP 200 |
| eOffice | Expected HTTP 302 login redirect |
| Voice | HTTP 200 health |
| Harbor | HTTP 200 health |
| Metube | HTTP 200 |
| LuongGia and SSO | HTTP 200 readiness |
| Existing harness preview root | HTTP 404 before and after rollout; valid TLS, unchanged baseline |

HTTP redirects to HTTPS. The Facebook verification challenge returned the exact
challenge with HTTP 200; invalid signatures remained rejected, and a signed
empty event was rejected without enqueuing application work.

This was **not a zero-downtime rollout**. The planned edge binary replacement
briefly interrupted connections. Voice and Harbor had temporary failures while
their sole eligible forwarding host was replaced; they recovered automatically
with the edge left unchanged.

Separately, before the final rollout, Kubernetes recorded `dlkntalos2` NotReady
around 16:33–16:34 UTC (23:33–23:34 UTC+7), affecting PostgreSQL, Harbor, and other
workloads. Its forwarder fenced on lease loss and reacquired the same identity
when connectivity returned. Later brief LuongGia readiness failures coincided
with application `database_pool_error` events. These recovered without Funnel
resets. This acceptance does not establish that the underlying cluster/database
instability is fixed.
