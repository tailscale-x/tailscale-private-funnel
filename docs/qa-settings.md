# Control settings and enrollment QA

Verified 2026-10-08.

- Current tailnet identity is read-only. OAuth and enrollment defaults have separate
  forms, results and clear statements about their scope.
- OAuth replacements validate Devices Read and users:read before atomic encrypted
  storage. Blank replacements preserve secrets. Test only never saves.
- Invalid URLs, unsupported fields and unpublished release versions are rejected.
  Clearing management URL is supported and prevents new enrollments.
- Public IP overrides are node-specific, under Load balancers → Inspect settings.
  Clearing restores automatic discovery. Migration retains existing overrides and
  does not alter credentials, zone assignments or the DNS ownership ledger.
- Enrollment collects the public edge hostname, certificate email for load
  balancers, and label aliases/local Docker deny patterns for ingress. Generated
  native deployment scripts use these choices and verified release versions.
- Node configuration and provisioning use the stored node-specific settings.
  Corrupt settings fail closed, including DNS reconciliation.
- Viewers see no settings/enrollment mutation forms. Administrators are enforced
  server-side. Existing local certificate/network changes require service updates.

Both Go test suites, vet, diff checks and reproducible UI asset builds passed.
Tests cover OAuth testing/rotation/failure, clearing, node isolation, validation,
role visibility, generated commands, migration and atomic credential rollback.

Chrome desktop (1440px) and mobile (390px) checks passed: separate form error
feedback, private-address rejection, enrollment fields, viewer visibility and no
horizontal overflow. Screenshots were inspected using synthetic fixture data.
The viewer page had no console warnings or errors; intentional invalid requests
returned the expected HTTP errors.

Deployed only the locally prebuilt control development artifact:
`dev-settings-20261008`, digest
`sha256:09cdfa4c52bb4591ffbce5601d54bd3152c04578bc7db156863676c558630558`.
No stable tag or GitHub release was created. Load balancer and ingress binaries
were not replaced.

Live health returned 204. OAuth Test only returned success. The preserved override
appears on the existing load-balancer page. Zone assignments, encrypted provider
credentials and every ledger entry matched the protected baseline after rollout.
Llama health returned 200 and Flue returned its existing backend 404 response.
These probes do not establish resolution of previously observed intermittent
upstream failures; that remains a separate runtime diagnostic issue.

Final result: passed for settings and enrollment scope.
