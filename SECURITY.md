# Security notes

Run `govulncheck ./...` against the selected dependency graph before release.
Recheck after dependency, import, or platform changes; a clean call-graph scan is
not a guarantee that a module contains no vulnerable packages.

## Dependency review (2026-09-08)

- `golang.org/x/crypto` is pinned to at least `v0.56.0`, which fixes the SSH
  channel denial-of-service issues
  [GO-2026-6354](https://pkg.go.dev/vuln/GO-2026-6354) and
  [GO-2026-6355](https://pkg.go.dev/vuln/GO-2026-6355).
- [GO-2026-5932](https://pkg.go.dev/vuln/GO-2026-5932) concerns the unmaintained
  `golang.org/x/crypto/openpgp` packages and has no fixed version. They are not
  imported by this library or its CLI in the reviewed dependency graph. A
  module-level scan can still report this advisory; do not suppress it globally
  or introduce these packages. Applications must scan their own dependency graph.

## Operational safety

Use authenticated DCOM with appropriately restricted network access. Keep
credentials and real point lists out of source control. Writes can have partial
or unknown outcomes and must not be retried blindly. Do not use live writes as
automated release tests.
