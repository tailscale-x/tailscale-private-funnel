# Display-name and resource-ID verification

Development artifact: `dev-resource-names-20261009-r1`.

- Providers and managed-zone assignments receive server-generated IDs. Provider
  labels accept Unicode and duplicates. Name-only changes preserve credentials.
- Node, enrollment, application and rule references use stable IDs. Node and
  application aliases do not modify operational hostnames or Service names.
- Ingress enrollment, signed configuration, reports and policy selectors use
  load-balancer IDs. Reports cannot substitute a different load balancer.
- DNS operations, zone read tests and removal address zone assignment IDs.
- Original signed report envelopes remain intact; reconciliation reads a
  control-owned normalized projection. Migration is transactional.
- Automated tests cover duplicate labels, immutable IDs, missing references,
  administrator authorization, consumed enrollment preservation, migration
  rollback, credential ciphertext preservation, report signature preservation,
  zone references and case-sensitive policy IDs.
- Both Go suites and vet pass. CSS rebuilds reproduce the same checksum; templ
  generation and diff checks pass.
- Chrome desktop and 390px checks cover provider inline rename/save/cancel,
  blank secrets, Add zone discovery, assignment IDs, policy selectors,
  enrollment fields and DNS operations. No horizontal overflow or console
  errors were observed.
- A private copy of the deployed database passed migration preflight: one
  provider, two domain assignments, three unchanged ledger records and one
  unchanged original signed report; encrypted credentials were unchanged.

Live rollout passed on the xvps control/load-balancer and 5090 ingress.

- Image digest: `sha256:6c596f0a49c43faee93610758260c11240df933d0d1aaf929a4d9af65e9fd209`.
- Native Linux amd64 checksum: `2d8e31b79601faa0b2fa58a85c59419a2550b56c9b87a5cd117ab103369c7f6c`.
- Provider rename and restoration through Chrome passed: ID unchanged, editor
  closed, secrets stayed blank. Credential ciphertext was unchanged.
- Both domain assignments retained their provider, with generated assignment IDs.
- Node IDs, tailnet identities, signing keys, application IDs, Service targets,
  and all three ledger record IDs/values/ownership entries matched the baseline.
- A fresh signed report contained two exposures with the enrolled load-balancer
  ID and no hostname reference. Both systemd services were active.
- Public Llama `/health` and Flue `/api/ping` returned successful JSON responses
  after the coordinated update. Flue's root and `/health` already returned 404
  before the update; `/api/ping` is its configured health endpoint.
- No stable tag or GitHub release was created.

final result: passed
