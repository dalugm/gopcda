# gopcda

A high-level, pure Go OPC DA 2 client built on
[go-opcda](https://github.com/oiweiwei/go-opcda) bindings and
[go-msrpc](https://github.com/oiweiwei/go-msrpc) RPC/DCOM transport.
Browse items, read values, and write values through DCOM without a Windows COM
runtime or a sidecar.

Query item metadata separately from value reads:

```sh
go run ./cmd/opcda properties 'Pump.Speed'
```

Uses the same `OPCDA_*` connection environment variables as `read`. The library
API is `server.ItemProperties(ctx, itemID)`. It enumerates advertised properties
and reads their values through `IOPCItemProperties`, without creating a group.
Property 101 is the optional item description; 100 is engineering units and
102/103 are the high/low engineering limits. The property's name is a label,
not the item's description: read the value of property 101.

CLI `descriptionStatus` distinguishes `available`, `empty`, `notProvided` and
`error`. Per-property failures remain in the JSON output and cause a nonzero
exit status; interface or transport failures are reported on stderr. Property
values use the same conversions as `ReadItem`. Known unsupported values are
reported per property; malformed or unknown wire types can fail the containing
RPC. The server may omit optional metadata entirely.

Early-stage library: the API may change. **Synchronous operations only;
subscriptions and automatic reconnection are not implemented.**

## Installation

Requires Go 1.26.0 or later. Use the latest patch release of a supported Go version.

```sh
go get github.com/dalugm/gopcda
```

The server must permit authenticated DCOM activation and access. Allow TCP port
135 and the object endpoint returned by activation. Supply a CLSID, or a ProgID
resolved through the server's OPCEnum service; CLSID takes precedence.

## Quick start

This example reads one item using a temporary group. Supply your connection
settings through `ServerConfig`. The library does not read environment variables;
the calling application owns configuration and secret storage.

```go
package main

import (
    "context"
    "errors"
    "fmt"
    "os"
    "time"

    opcda "github.com/dalugm/gopcda"
)

func main() {
    if err := run(); err != nil {
        fmt.Fprintln(os.Stderr, err)
        os.Exit(1)
    }
}

func run() (err error) {
    setup, cancel := context.WithTimeout(context.Background(), 10*time.Second)
    server, err := opcda.Connect(setup, opcda.ServerConfig{
        Host:     "192.0.2.10",
        Domain:   "YOUR_DOMAIN",
        Username: "YOUR_USER",
        Password: "YOUR_PASSWORD",
        CLSID:    "YOUR_SERVER_CLSID",
    })
    cancel()
    if err != nil {
        return err
    }
    defer func() {
        cleanup, cancel := context.WithTimeout(context.Background(), 10*time.Second)
        defer cancel()
        err = errors.Join(err, server.Close(cleanup))
    }()

    ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
    defer cancel()
    value, err := server.ReadItem(ctx, "Pump.Speed")
    if err != nil {
        return err
    }
    if !opcda.QualityIsGood(value.Quality) {
        return fmt.Errorf("item quality is 0x%04X", uint16(value.Quality))
    }
    fmt.Printf("%s = %v (%T)\n", value.ItemID, value.Value, value.Value)
    return nil
}
```

`Connect` uses its context only during setup. Canceling that context after a
successful connection does not end the session. Pass an operation context to each
call and always close the server. `Disconnect` is a convenience method that
ignores cleanup errors; use `Close(ctx)` when those errors matter.

## Periodic acquisition

Reuse one server session and a persistent group for periodic reads and writes:
create it with `AddGroupContext`, register ItemIDs once with `AddItems`, then call
`Read` on your application's timer. The group owns and reuses registration handles.

- `Read(ctx, true)` reads the server cache; `false` requests device values.
- Check `RevisedUpdateRate()`: the requested group interval may be revised.
- Polling is not a subscription, and a cache read does not imply a fresh device sample.
- Use `ReadItems` for a registered subset, `Write` for explicit writes, and
  `RemoveItems` to unregister items without deleting server points.

See the [usage guide](docs/usage.md) for complete examples, batch behavior, errors,
supported value types, CLI commands, and development checks.

## Value types

The following mappings apply to item reads, property values and writes. Named
constants such as `opcda.VTR8`, `opcda.VTArray` and `opcda.VTByRef` match the
Automation definitions; tests check their values against the upstream bindings.

| Automation type | Code | Go read value | Go write input |
| --- | --- | --- | --- |
| `VT_EMPTY` | `0x0000` | `nil` | `Variant{Type: VTEmpty}` |
| `VT_NULL` | `0x0001` | `nil` | `Variant{Type: VTNull}` |
| `VT_I1` / `VT_UI1` | `0x0010` / `0x0011` | `int8` / `uint8` | Same |
| `VT_I2` / `VT_UI2` | `0x0002` / `0x0012` | `int16` / `uint16` | Same |
| `VT_I4` / `VT_UI4` | `0x0003` / `0x0013` | `int32` / `uint32` | Same |
| `VT_I8` / `VT_UI8` | `0x0014` / `0x0015` | `int64` / `uint64` | Same |
| `VT_INT` / `VT_UINT` | `0x0016` / `0x0017` | `int32` / `uint32` | `Variant{Type: VTInt, Value: int32(n)}` / `Variant{Type: VTUint, Value: uint32(n)}` |
| `VT_R4` / `VT_R8` | `0x0004` / `0x0005` | `float32` / `float64` | Same; finite values only |
| `VT_CY` | `0x0006` | `Currency` (scaled `int64`, units of 1/10000) | Same |
| `VT_DATE` | `0x0007` | `time.Time` (milliseconds, UTC convention) | `time.Time` |
| `VT_BSTR` | `0x0008` | `string` | UTF-8 `string`, encoded as UTF-16 |
| `VT_ERROR` | `0x000A` | `ErrorCode` (`uint32` data, not `error`) | Same |
| `VT_BOOL` | `0x000B` | `bool` | Same |
| `VT_DECIMAL` | `0x000E` | `Decimal` (96-bit coefficient, sign, scale) | Same |
| `VT_ARRAY \| T` | `0x2000 \| T` | `Array` (element type, bounds, flat values) | Same |
| `VT_BYREF \| T` | `0x4000 \| T` | `Variant` retaining the type tag and value | Same |
| `VT_VARIANT` | `0x000C` | `Variant` inside a VARIANT array or BYREF wrapper | Same contexts; not a standalone scalar |
| `VT_DISPATCH` / `VT_UNKNOWN` / `VT_RECORD` | `0x0009` / `0x000D` / `0x0024` | Per-item unsupported-value error | Not supported |

Types and constants in the table belong to `opcda`, except standard Go types.
Currency/Decimal JSON values are exact decimal strings. Arrays retain lower
bounds and dimensions; native DECIMAL arrays use VARIANT elements instead.
Ordinary EMPTY/NULL reads both return nil; their tags remain distinct inside
VARIANT arrays and BYREF values. Other VARENUM entries used only in type
descriptions or property sets are not OPC DA VARIANT data types.

See [value details and examples](docs/usage.md#value-types) for array ordering,
DATE timezone semantics and CLI input formats.

## Important contracts

- Inspect both operation errors and per-item results: batches can partially succeed.
- Preserve quality and server timestamps. RPC success does not imply Good quality.
  Use `QualityIsGood` and `QualityDescription` when interpreting samples.
- Writes are never automatically retried. Distinguish `ErrWriteNotAttempted` from
  `ErrWriteOutcomeUnknown`; the latter may already have changed the device.
- Calls within a group are serialized, without a FIFO guarantee. The caller owns
  scheduling, logging, secret storage, and reconnection.
- Always close sessions; `Close(ctx)` reports cleanup failures.

## Limitations

Standard scalar values, exact currency/decimal values, DATE, SAFEARRAYs and
BYREF values are supported. See [value types](docs/usage.md#value-types) for
representations, array bounds and write examples. DATE carries no timezone;
UTC is a representation convention, not inferred from the server.

No COM object values (`VT_UNKNOWN`/`VT_DISPATCH`), custom records (`VT_RECORD`),
subscriptions, DA3 or hierarchical browsing,
or cross-exporter enumeration. Use complete server ItemIDs; suffixes are not inferred.

This project is under active development and may still contain undiscovered issues.
See [validation details](docs/usage.md#compatibility-and-limitations) and
[security notes](SECURITY.md).

## Implementation

`gopcda` owns the application-facing server/group API, persistent registrations,
cancellable operations, per-item errors and session cleanup. Generated
`go-opcda` bindings provide OPC request/response codecs; `go-msrpc` provides the
underlying RPC/DCOM runtime. The existing bound connection and interface IPID
remain responsible for routing each call.

The adapters interpret HRESULTs in this library, preserving successful results
when an operation reports partial success (`S_FALSE`). Small compatibility
codecs remain where generated bindings cannot express existing contracts:
length-counted BSTR reads, BYREF values and strict VARIANT/array validation.
UTF-16 strings and optional group-state fields use the upstream codecs.

## License

[MIT](LICENSE). Dependencies retain their own licenses.
