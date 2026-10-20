# Replacement control-Service peer discovery — 2026-10-20

During the v4.0.5 release rollout, the existing edge lacked the replacement
control Service host in its peer map. Public cached routes remained available.
The previously approved application-host discovery grant did not cover control.

After explicit approval, one ICMP-only grant was added from this installation's
load-balancer and three gateway roles to its control-role devices. Policy update
used an ETag precondition; semantic comparison verified exactly one added grant
and no other policy changes. No TCP/UDP access was added.

A further real control Pod replacement was then performed while the existing
edge and gateways remained running. The edge retained its process and persistent
identity, learned the new disposable control host, selected the Tailscale route,
and connected directly. No extra diagnostic Tailscale identity was joined and no
edge session-reset or restart was used for this test.

Post-rollout checks compare all 11 DNS-ledger rows and 44 exposure bindings,
control encryption material, provider and zone assignments, native addresses,
and validated public TLS certificate fingerprints. Encrypted credential values
are compared after authenticated decryption: startup may re-encrypt unchanged
credentials with new random nonces.

The generated policy, CLI wizard, and web policy guidance include both discovery
rules in v4.0.6. Existing broad tailnet grants can still weaken isolation. Review
and merge the installation-specific template rather than replacing unrelated
policy.
