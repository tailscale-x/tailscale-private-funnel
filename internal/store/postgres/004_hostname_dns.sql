-- Independent of route membership: a temporary route cannot reset DNS policy.
CREATE TABLE hostname_dns (
  hostname TEXT PRIMARY KEY,
  cloudflare_proxied INTEGER NOT NULL DEFAULT 0,
  revision INTEGER NOT NULL DEFAULT 0
);
