# Working on gopcda

## Scope and layout

- Use English for documentation, code comments, identifiers, and test descriptions.
  Keep Unicode protocol fixtures language-neutral and use escapes where appropriate.

- This is an importable Go library: module `github.com/dalugm/gopcda`, root package
  `opcda`. Edge calls the API directly. Keep optional executables under `cmd/`.
- The API is in active development; coordinated changes are allowed. Update callers,
  tests and README together when changing a contract. Keep implementation and tests adjacent;
  `*_wire.go` contains NDR encoding/decoding. See README for the current file map
  and supported operations. Do not duplicate feature status or session history here.
- Use the toolchain and dependency versions declared in go.mod. Do not patch the
  module cache or add vendor/replace/fork workarounds without discussing the need.

- Keep the library silent: return errors to callers instead of initializing logging
  frameworks, reading logging environment variables, or writing to stdout/stderr.
  Output belongs to cmd/ or the embedding application.
- Use conventional initialisms (CLSID, IPID, OID) and meaningful connection names
  (conn, serverConn, browseConn); short locals such as ctx, req and resp are fine.

## Protocol and lifecycle contracts

- IOPCServer calls use its bound connection/IPID. Other COM interfaces need the
  matching QueryInterface result, IPID and RPC presentation context.
- Preserve NDR pointer deferral and array layout. Add wire regression fixtures for
  protocol changes, including malformed/truncated data and partial HRESULTs.
- Connect context controls setup only; Close owns successful session teardown.
  Per-item Error fields are error values; preserve HRESULTs and causes for errors.Is/As.
- Persistent group transports outlive individual setup/call contexts. Operation
  cancellation must not accidentally cancel the reusable transport.
- Serialize operations within a group with cancellable waiting. Keep registry and
  health locks out of network calls; Close must coordinate in-flight lifecycle work.
- Preserve per-item errors and original quality/timestamps. RPC success is not
  evidence of Good quality or fresh device sampling.
- Never automatically retry writes. Partial success and unknown write outcomes
  must remain visible. Return cleanup errors from Server.Close.

## Validation

```sh
golangci-lint fmt ./...
golangci-lint run ./...
go test -race ./...
go build ./...
git diff --check
```

The v2 lint configuration applies to the library and tests. Do not blanket-disable
checks to make a change pass; any nolint must name the rule and explain why.
`just fmt`, `just lint` and `just check` provide the same local entry points.

Use focused tests while editing, then run the above before committing code changes.
Default tests are offline. Live group tests require `OPCDA_LIVE=1`, connection
variables and `OPCDA_ITEMS_FILE` containing exactly 1000 complete ItemIDs.
Live automated tests are read-only; do not add writes to them. Keep credentials,
real point lists and generated artifacts out of commits. State clearly whether a
result is from offline tests, a live run, or the user's manual verification.
