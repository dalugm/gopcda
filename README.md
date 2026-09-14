# gopcda

A high-level, pure Go OPC DA 2 client built on
[go-opcda](https://github.com/oiweiwei/go-opcda) bindings and
[go-msrpc](https://github.com/oiweiwei/go-msrpc) RPC/DCOM transport.
Browse items, read values, and write scalars through DCOM without a Windows COM
runtime or a sidecar.

Early-stage library: the API may change. **Synchronous operations only;
subscriptions and automatic reconnection are not implemented.**

## Installation

Requires Go 1.27.0 or later.

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
supported scalar types, CLI commands, and development checks.

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

No subscriptions, array values, DATE conversion, DA3 or hierarchical browsing,
or cross-exporter enumeration. Use complete server ItemIDs; suffixes are not inferred.

Field validation is limited to short SUPCON runs, mainly browsing and reads.
Batch writes and several scalar types have offline tests only. Intermittent
activation failures were observed; this is not a production-reliability guarantee.
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
length-counted BSTRs, UTF-16 string lengths, optional group-state fields and
strict response-array validation. See [binding compatibility](docs/bindings.md).

## License

[MIT](LICENSE). Dependencies retain their own licenses.
