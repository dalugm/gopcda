# OPC binding compatibility

The library uses the published `github.com/oiweiwei/go-opcda v0.3.0` module
with `github.com/oiweiwei/go-msrpc v1.5.4`. No local replacement, fork or
module-cache patch is required.

## Responsibilities

`Server`, `Group` and the public result/error types remain owned by gopcda.
Activation, QueryInterface, matching connections/IPIDs, DCOM keepalive,
cancellable group serialization, registration state and cleanup also remain
here. The generated OPC request/response types supply their NDR codecs through
the existing bound RPC operation envelope. This integration deliberately does
not use the generated convenience clients' nonzero-HRESULT error conversion:
gopcda continues to distinguish failed HRESULTs from successful status codes
and preserve per-item results.

Generated codecs cover AddGroup, GetStatus, RemoveGroup, BrowseOPCItemIDs,
CLSIDFromProgID, AddItems, RemoveItems, synchronous Read/Write and the SetState
response. The IEnumString codec comes from go-msrpc. Single-item operations
reuse the batch adapters.

## Local compatibility code

The following small adapters preserve existing wire contracts:

- **BSTR:** go-msrpc v1.5.4 uses the null-BSTR byte-count marker for an empty
  string and trims trailing NUL code units on decoding. The local scalar codec
  retains an empty, non-null BSTR on writes and preserves length-counted data
  on reads, including embedded/trailing NULs and surrogate pairs. Invalid lengths
  are rejected. `bindingsWriter` and `bindingsReader` apply it to generated
  deferred VARIANT pointers.
- **UTF-16:** the upstream NDR string length helper counts UTF-8 bytes rather
  than UTF-16 code units. Request adapters correct string counts. An explicit
  string terminator selects a non-null empty access path in generated
  ItemDefinition encoding.
- **Optional pointers:** AddGroup's generated scalar fields cannot distinguish
  a null time-bias/deadband pointer from a pointer to zero. A request-specific
  writer preserves that distinction. SetState's small request codec remains
  handwritten so unspecified time bias, locale and client handle remain null
  (no change), while false/zero active and deadband values are sent explicitly.
- **Response counts:** generated result arrays are bounded by the requested
  count before allocation, with a maximum batch size of 1000. Blob validation
  checks both the original conformant count and declared blob size, rejecting
  the generated decoder's zero-count fallback. Enumeration keeps
  its conformant/varying count and fetched-count checks. Null arrays remain
  distinguishable from valid results; per-item and overall HRESULT validation
  stays in the operation adapters.

These exceptions should be revisited when upstream bindings change. Remove an
adapter only after its independent wire fixtures pass against the replacement.
Do not bypass an incompatibility by editing generated code or the module cache.

## Validation

Offline tests cover independent wire fixtures, truncation/malformed responses,
partial HRESULTs, scalar/BSTR values, cancellation, registration and cleanup.
The migration does not establish live compatibility or production reliability.
SUPCON browsing and persistent-group reads need a new read-only validation run
when equipment is available; earlier field runs used the previous codecs.
