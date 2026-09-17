# Usage guide

[Back to README](../README.md)

## Persistent groups

Use a persistent group for periodic reads and on-demand writes. Register items
once, then reuse the group's connections and server handles:

```go
group, err := server.AddGroupContext(ctx, "process", 500, 0)
if err != nil {
    return err
}
fmt.Println("Server update interval:", group.RevisedUpdateRate())

items, err := group.AddItems(ctx, []string{"Pump.Speed", "Tank.Level"})
if err != nil {
    return err
}
for _, item := range items {
    if item.Error != nil {
        return fmt.Errorf("add %s: %w", item.ItemID, item.Error)
    }
}

values, err := group.Read(ctx, true) // true: server cache; false: device
if err != nil {
    return err
}
for _, value := range values {
    if value.Error != nil {
        return fmt.Errorf("read %s: %w", value.ItemID, value.Error)
    }
    if opcda.QualityIsGood(value.Quality) {
        fmt.Println(value.ItemID, value.Value, value.SourceTimestampMs)
    }
}
```

Repeat `Read` on your application's timer with a per-call timeout. Calls within a
group are serialized with cancellable waiting; independent groups can operate
concurrently. There is no FIFO guarantee between concurrent callers. Use an
ordered application queue if write order matters.

Batches contain at most 1,000 items per RPC; larger requests are split. Duplicate
ItemIDs and repeated `AddItems` calls reuse existing handles without another RPC.
The library owns the registration cache; callers do not need a second one.
A requested update interval is not guaranteed:
inspect `RevisedUpdateRate()` and the returned timestamps. Cache polling frequency
does not establish the device's sampling frequency.

`ReadItems` reads a subset of registered items. If a later batch fails, `Read` and
`ReadItems` return earlier observations alongside the operation error, in request
order. Uncompleted registered items are omitted, not reported as missing or good.
Locally detected unregistered-item errors remain in the results. Always process
the results as well as the call error when partial observations matter.

`RemoveItems(ctx, itemIDs)` removes registrations from the existing group. It
returns a map keyed by distinct ItemID: nil means removed or already unregistered;
an error means removal was not confirmed. Confirmed removals are excluded from
future `Read` calls and may be registered again with `AddItems`. Per-item HRESULT
failures retain their handles. An operation error preserves earlier confirmed
removals but can leave remote state uncertain: close the group/session before
reuse. Removal does not delete server points or write device values.

`SetActive` changes the group's active state. `Remove(ctx)` releases a group;
`Server.Close(ctx)` cancels in-flight group operations, including removal, and
releases remaining groups and the activated server reference before closing the
session. Removed groups cannot be reused. Persistent objects are kept alive
through DCOM ping sets. Cleanup failures are returned, not silently retried.

## Writing values

Writes accept the value types below. Integers retain their exact bit width and
precision; no conversion through floating point occurs. Add each target item to the
group before writing:

```go
results, err := group.Write(ctx, map[string]any{
    "Pump.Speed": float32(12.5),
    "Tank.Level": float64(3.25),
})
for itemID, itemErr := range results {
    if itemErr != nil {
        fmt.Printf("%s: %v\n", itemID, itemErr)
    }
}
if err != nil {
    return err
}
```

Access rights returned by `AddItems` are a server hint, not a client-side write
authorization rule. Explicit writes use the server's current per-item HRESULT;
cached access rights never suppress a write request.

Always inspect both the result map and the call error. A returned entry with a nil error confirms
that item was acknowledged by the server. Batch writes are not transactions:
earlier batches may succeed before a later batch fails. Local validation failures
are reported per item; other valid items may still be written. Preflight and local
validation failures wrap `ErrWriteNotAttempted` and preserve the original cause.
This is distinct from `ErrWriteOutcomeUnknown`, where an RPC may have executed.

The library never retries writes automatically. A timeout or broken connection
can leave the outcome unknown. Verify the result before deciding whether to retry.
For occasional single-item writes, use `server.WriteItem(ctx, itemID, value)`.
Neither method performs automatic readback or restores the previous value.

## Errors and data quality

Use `errors.Is` and `errors.As` instead of matching error text:

```go
var hr *opcda.HRESULTError
if errors.As(err, &hr) {
    fmt.Printf("%s: HRESULT 0x%08X, item %q\n", hr.Operation, hr.Code, hr.ItemID)
}
if errors.Is(err, opcda.ErrWriteOutcomeUnknown) {
    // The write may have executed. Verify before retrying.
}
```

| Error | Meaning |
| --- | --- |
| `*HRESULTError` | Server COM result, including the operation, raw code, and item ID when known |
| `ErrClosed` / `ErrGroupClosed` | Session or group is no longer available |
| `ErrItemNotRegistered` | The group has no handle for the requested item |
| `ErrWriteOutcomeUnknown` | A write was attempted but its outcome is uncertain |
| `ErrWriteNotAttempted` | No write RPC was attempted for the item |
| `ErrWriteAcknowledged` | A single-item write succeeded but subsequent cleanup failed |

Transport errors retain their causes. Context cancellation and deadline errors
remain available through `errors.Is`. Concurrent `Close` callers wait for the same
cleanup, can cancel their own wait, and receive the original cleanup result.

`Item.Error` and `ReadResult.Error` are per-item errors. These error fields are
excluded from JSON; applications should explicitly encode the error details they
need. Successful RPC calls can still return Bad or Uncertain quality. Preserve
quality codes and server-provided timestamps rather than treating every returned
value as a valid measurement.

Use `QualityIsGood`, `QualityIsBad`, and `QualityIsUncertain` for classification,
and `QualityDescription` for the standard class/sub-status description. These
helpers ignore vendor and limit bits; retain the original quality WORD for those
details. The library returns non-Good samples unchanged. Filtering, storage,
alerts, and connection recovery remain application decisions.

The library is silent: it does not initialize a logger, read logging environment
variables, or write to stdout/stderr. Logging and reconnection policy belong to the
calling application.

## Other operations

- `server.GetServerStatusContext(ctx)` queries server status with a per-call deadline.
- `server.BrowseItemIDs(ctx)` returns sorted, unique DA2 flat browse results.
- `server.ReadItem(ctx, itemID)` performs one synchronous device read using a temporary group.

Browse results and value ItemIDs depend on the server. Some servers require a
property suffix such as `.VALUE`; the library does not append or infer suffixes.

## Server identification

Provide `CLSID` for direct activation, or leave it empty and set `ProgID`:

```go
cfg := opcda.ServerConfig{
    Host: "192.0.2.10",
    Domain: "YOUR_DOMAIN",
    Username: "YOUR_USER",
    Password: "YOUR_PASSWORD",
    ProgID: "Vendor.Server.1",
}
server, err := opcda.Connect(ctx, cfg)
```

When both are set, `CLSID` takes precedence and OPCEnum is not contacted.
To resolve a name without connecting to the OPC DA server, use
`opcda.ResolveProgID(ctx, cfg, "Vendor.Server.1")`. Resolution uses the target
machine's OPCEnum service and returns a CLSID string. It does not consult the
client's local registry. If OPCEnum is unavailable or access is denied, supply
an independently verified CLSID to connect directly.

## Value types

| Go type | COM VARIANT type |
| --- | --- |
| `bool` | `VT_BOOL` |
| `int8`, `int16`, `int32`, `int64` | `VT_I1`, `VT_I2`, `VT_I4`, `VT_I8` |
| `uint8`, `uint16`, `uint32`, `uint64` | `VT_UI1`, `VT_UI2`, `VT_UI4`, `VT_UI8` |
| `float32`, `float64` | `VT_R4`, `VT_R8` |
| `string` | `VT_BSTR` |
| `time.Time` | `VT_DATE` |
| `opcda.Currency` | `VT_CY`, signed integer in units of 1/10000 |
| `opcda.Decimal` | `VT_DECIMAL`, exact 96-bit coefficient, scale 0..28 and sign |
| `opcda.ErrorCode` | `VT_ERROR`, a data value, not an operation error |
| `opcda.Variant{Type: opcda.VTEmpty}` / `{Type: opcda.VTNull}` | Explicit `VT_EMPTY` / `VT_NULL` writes |
| `opcda.Variant{Type: opcda.VTInt, Value: int32(n)}` / `{Type: opcda.VTUint, Value: uint32(n)}` | Explicit `VT_INT` / `VT_UINT` writes |
| `opcda.Array` | `VT_ARRAY` with element type, bounds and flat values |
| `opcda.Variant{Type: baseType \| opcda.VTByRef, Value: value}` | `VT_BYREF` wrapper |

These types are supported by single-item and persistent-group reads and writes.
Strings must be valid UTF-8 and are encoded as UTF-16, including embedded NULs.
An empty Go string writes an empty BSTR. Floats must be finite. Writes reject
`int`, `uint`, pointers, slices, and other types; convert to an explicit supported
type first. The server still decides whether a value can be written or converted
to a point's canonical type, and reports failures through per-item HRESULTs.

DATE is read and written at millisecond precision, in years 100..9999. It carries
no timezone: returned `time.Time` uses UTC as a representation convention. Writes
normalize the supplied time to UTC; callers must apply any server-specific wall
clock convention themselves. Currency and decimal JSON values are exact decimal
strings, not floating-point approximations. `ErrorCode` does not implement
`error`; inspect the operation/per-item error and quality separately.

Ordinary EMPTY/NULL reads both return nil; INT/UINT return int32/uint32. Within
VARIANT arrays and BYREF values, `Variant.Type` retains the original type tag.
BYREF is a value snapshot, not a usable pointer into the server's memory.
`VT_VARIANT|VT_BYREF` requires a nested `Variant` value.

### Arrays

`Array.Bounds` uses SAFEARRAY wire order: the rightmost, fastest-changing dimension
comes first. `Values` is flattened in that order. Lower bounds need not be zero.
The number of values must equal the product of the dimension counts. For example:

```go
value := opcda.Array{
    ElementType: opcda.VTR8,
    Bounds: []opcda.ArrayBound{{Lower: -1, Count: 3}},
    Values: []any{float64(1.5), float64(2.5), float64(3.5)},
}
err := server.WriteItem(ctx, "Pump.History", value)
```

Supported element types are integers, floats, BOOL, BSTR, CY, DATE, ERROR and
VARIANT. Use `ElementType: opcda.VTVariant` and `Variant` elements for mixed values or DECIMAL:
MS-OAUT does not permit a native DECIMAL SAFEARRAY. Nested arrays are represented
inside VARIANT elements. Nil bounds and nil values together denote a null array;
a dimension with count zero denotes an empty array. Limits are 32 dimensions,
1,048,576 elements per array and 32 nesting levels. COM objects and custom records
are not exposed or written: known payloads are consumed and reported as per-item
unsupported-value errors, preserving valid siblings and server HRESULTs.
Malformed or unknown wire encodings can fail the containing RPC.

```go
amount := opcda.Decimal{Lo: 12345, Scale: 2} // 123.45, without float rounding
reference := opcda.Variant{Type: opcda.VTI4 | opcda.VTByRef, Value: int32(42)}
```

## Command-line tool

From a checkout, configure the connection and run the optional manual tool:

```sh
export OPCDA_HOST=192.0.2.10
export OPCDA_DOMAIN=YOUR_DOMAIN
export OPCDA_USERNAME=YOUR_USER
export OPCDA_CLSID=YOUR_SERVER_CLSID
# Set OPCDA_PASSWORD securely in your local environment.

go run ./cmd/opcda resolve "Example.Server.1"
go run ./cmd/opcda status
go run ./cmd/opcda browse
go run ./cmd/opcda read "Pump.Speed"
go run ./cmd/opcda properties "Pump.Speed"
go run ./cmd/opcda write "Pump.Speed" 12.5 float32
go run ./cmd/opcda write "Pump.Enabled" true bool
go run ./cmd/opcda write "Counter.Total" 18446744073709551615 uint64
go run ./cmd/opcda write "Batch.Name" "Batch A" string
go run ./cmd/opcda write "Batch.Timestamp" "2026-09-15T10:00:00Z" date
go run ./cmd/opcda write "Batch.Amount" "123.4500" currency
go run ./cmd/opcda write "Batch.PreciseAmount" "123.456789" decimal
go run ./cmd/opcda poll items.txt 500ms 180 cache
```

`OPCDA_*` variables belong to this optional tool and the opt-in live tests,
not the library API. Set `OPCDA_PROGID` instead of `OPCDA_CLSID` to resolve a
server name. If both are set, CLSID takes precedence. The write type defaults
to `float32`. CLI types are bool, int8/16/32/64, uint8/16/32/64, float32/64,
string, date (RFC3339), currency, decimal, error (decimal or hex code),
int/uint (explicit 32-bit VT_INT/VT_UINT), empty (`""`) and null (`null`).
Currency/decimal inputs use plain base-10 notation without exponents. Array and
BYREF writes are available through the Go API, not through CLI text parsing.

`resolve PROGID` queries the remote OPCEnum service and prints only the CLSID
followed by a newline to stdout. It requires `OPCDA_HOST`, uses the same credentials
and `OPCDA_TIMEOUT`, and ignores `OPCDA_CLSID` and `OPCDA_PROGID`. The target
OPC DA server is not activated. Diagnostics go to stderr. To reuse the result:

```sh
OPCDA_CLSID="$(go run ./cmd/opcda resolve 'Example.Server.1')" && export OPCDA_CLSID
```

Known HRESULTs include their symbolic name and meaning in the error text.
The library describes OPC-specific errors and uses go-msrpc for standard HRESULTs;
unknown codes retain their hexadecimal value. The CLI adds write outcome details on
stderr, for example:

```text
opcda: write "Device.Tag": AddItems: HRESULT 0xc0040007 (OPC_E_UNKNOWNITEMID): the ItemID is not available in the server address space
Write not attempted: item registration failed.
```

A failed AddItems registration precedes the write request. A transport failure
during writing instead reports an unknown outcome and asks you to verify the
value before retrying. Acknowledged writes with cleanup failures remain distinct.
The CLI never retries a write automatically. Library callers
should continue using `errors.As` / `errors.Is`, not parsing the error text.

`items.txt` contains one complete ItemID per line. Polling reports the requested
and revised intervals, read latency, overruns, quality counts, and item failures.
The default command timeout is 180 seconds; override it with `OPCDA_TIMEOUT=10m`.
The tool prints results to stdout and diagnostics to stderr. Libraries embedding
this package do not need to invoke it.

## Compatibility and limitations

- Scalar and array reads and writes are supported, including native `VT_UI8` as
  `uint64`. Preserve integer types downstream to avoid floating-point precision loss.
- DATE values use `time.Time` at millisecond precision, including property
  timestamps. DATE contains no timezone; UTC is a representation convention.
- COM object values and custom records are not supported.
- DA3 browsing, hierarchical browsing,
  cross-exporter enumeration, subscriptions, and automatic reconnection are not implemented.

This project is under development and may still contain undiscovered issues.

## Development

Use golangci-lint v2 (validated with 2.13.2). Tests run offline by default.

```sh
just fmt    # gofumpt and goimports
just check  # lint, race tests, and build
```

Without just:

```sh
golangci-lint fmt ./...
golangci-lint run ./...
go test -race ./...
go build ./...
```

The optional live test requires connection variables and a file containing exactly
1,000 complete ItemIDs. It reads values and does not write them:

```sh
OPCDA_LIVE=1 OPCDA_ITEMS_FILE=items.txt go test -run '^TestLivePersistentRead$' -v -count=1 .
```

Keep credentials and real point lists out of commits. Source, comments, tests,
and documentation use English. See [AGENTS.md](../AGENTS.md) for maintenance guidance.
