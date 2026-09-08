package opcda

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"strings"
	"testing"

	"github.com/oiweiwei/go-msrpc/dcerpc"
)

type outcomeBatchConn struct {
	*batchConn
	afterInvoke func(dcerpc.Operation, int32) error
}

func (c *outcomeBatchConn) Invoke(
	ctx context.Context,
	op dcerpc.Operation,
	opts ...dcerpc.CallOption,
) error {
	if err := c.batchConn.Invoke(ctx, op, opts...); err != nil {
		return err
	}
	return c.afterInvoke(op, c.calls.Load())
}

func TestBatchReadFailurePreservesObservedResults(t *testing.T) {
	for _, failure := range []string{"transport", "missing arrays", "call HRESULT"} {
		t.Run(failure, func(t *testing.T) {
			c, g, wire := testPersistent()
			g.syncConn = &outcomeBatchConn{
				batchConn: wire,
				afterInvoke: func(op dcerpc.Operation, call int32) error {
					response := op.(*opcOp).resp.(*batchReadResponse)
					if call == 1 {
						response.errors[7] = -1
						response.states[8].quality = 0x44
						return nil
					}
					switch failure {
					case "transport":
						return io.ErrUnexpectedEOF
					case "missing arrays":
						response.states = nil
					case "call HRESULT":
						response.hresult = -1
					}
					return nil
				},
			}
			ids := []string{"missing before"}
			wantIDs := []string{"missing before"}
			for i := range 2001 {
				id := fmt.Sprintf("tag%04d", 2000-i)
				g.items[id] = registeredItem{handle: uint32(i + 1), rights: 3}
				ids = append(ids, id)
				if i < 1000 {
					wantIDs = append(wantIDs, id)
				}
				if i == 499 || i == 999 || i == 1499 {
					missing := fmt.Sprintf("missing after %d", i)
					ids = append(ids, missing)
					wantIDs = append(wantIDs, missing)
				}
			}
			ids = append(ids, "missing after")
			wantIDs = append(wantIDs, "missing after")

			result, err := c.read(t.Context(), 1, ids, true)
			if err == nil {
				t.Fatal("expected second-batch failure")
			}
			switch failure {
			case "transport":
				if !errors.Is(err, io.ErrUnexpectedEOF) {
					t.Fatalf("lost transport cause: %v", err)
				}
			case "missing arrays":
				if !strings.Contains(err.Error(), "missing result arrays") {
					t.Fatalf("lost decode failure: %v", err)
				}
			case "call HRESULT":
				var hr *HRESULTError
				if !errors.As(err, &hr) || hr.Code != 0xffffffff {
					t.Fatalf("lost call HRESULT: %v", err)
				}
			}
			if len(result) != len(wantIDs) {
				t.Fatalf("got %d results, want %d observed results", len(result), len(wantIDs))
			}
			for i, wantID := range wantIDs {
				got := result[i]
				if got.ItemID != wantID {
					t.Fatalf("result %d ItemID = %q, want %q", i, got.ItemID, wantID)
				}
				if strings.HasPrefix(wantID, "missing") {
					if !errors.Is(got.Error, ErrItemNotRegistered) {
						t.Fatalf("%s lost registration failure: %v", wantID, got.Error)
					}
					continue
				}
				handle := g.items[wantID].handle
				if handle == 8 {
					var hr *HRESULTError
					if !errors.As(got.Error, &hr) || hr.Code != 0xffffffff || hr.ItemID != wantID {
						t.Fatalf("%s lost item HRESULT: %v", wantID, got.Error)
					}
					continue
				}
				quality := int16(0xc0)
				if handle == 9 {
					quality = 0x44
				}
				if got.Error != nil || got.Value != float32(handle) || got.Quality != quality ||
					got.SourceTimestampMs != -11644473600000 {
					t.Fatalf("%s lost observed data: %+v", wantID, got)
				}
			}
			if wire.calls.Load() != 2 {
				t.Fatalf("sent %d RPCs, want 2 without retry or later batches", wire.calls.Load())
			}
		})
	}
}

func TestBatchWritePreflightReturnsNotAttempted(t *testing.T) {
	for _, failure := range []string{"missing group", "closed group", "canceled", "keepalive"} {
		t.Run(failure, func(t *testing.T) {
			c, g, wire := testPersistent()
			ctx := t.Context()
			cause := ErrGroupClosed
			switch failure {
			case "missing group":
				delete(c.groups, 1)
			case "closed group":
				g.closed = true
			case "canceled":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
				cause = context.Canceled
			case "keepalive":
				cause = io.ErrUnexpectedEOF
				pinger := &objectPinger{err: cause}
				c.pingHealth.Store(pinger)
			}
			values := map[string]any{"A": float32(1), "B": true}
			result, err := c.write(ctx, 1, values)
			if !errors.Is(err, cause) {
				t.Fatalf("call error = %v, want cause %v", err, cause)
			}
			if len(result) != len(values) {
				t.Fatalf("got %d results, want %d", len(result), len(values))
			}
			for id := range values {
				if !errors.Is(result[id], ErrWriteNotAttempted) || !errors.Is(result[id], cause) {
					t.Fatalf(
						"%s error = %v, want not attempted and cause %v",
						id,
						result[id],
						cause,
					)
				}
			}
			if wire.calls.Load() != 0 {
				t.Fatal("preflight failure attempted a write RPC")
			}
		})
	}
}

func TestBatchWriteLocalFailuresAreNotAttempted(t *testing.T) {
	c, g, wire := testPersistent()
	g.items["nonfinite"] = registeredItem{handle: 2, rights: 3}
	g.items["unsupported"] = registeredItem{handle: 3, rights: 3}
	g.items["invalid string"] = registeredItem{handle: 4, rights: 3}
	g.items["valid"] = registeredItem{handle: 5, rights: 3}
	result, err := c.write(t.Context(), 1, map[string]any{
		"missing":        true,
		"nonfinite":      math.Inf(1),
		"unsupported":    struct{}{},
		"invalid string": "\xff",
		"valid":          float32(2),
	})
	if err != nil {
		t.Fatal(err)
	}
	for id, cause := range map[string]string{
		"missing":        "item is not registered",
		"nonfinite":      "must be finite",
		"unsupported":    "unsupported write type",
		"invalid string": "valid UTF-8",
	} {
		if !errors.Is(result[id], ErrWriteNotAttempted) ||
			!strings.Contains(result[id].Error(), cause) {
			t.Errorf("%s error = %v, want not attempted and cause %q", id, result[id], cause)
		}
	}
	if !errors.Is(result["missing"], ErrItemNotRegistered) {
		t.Errorf("lost registration cause: %v", result["missing"])
	}
	if len(result) != 5 || result["valid"] != nil {
		t.Fatalf("unexpected write outcomes: %v", result)
	}
	if wire.calls.Load() != 1 || len(wire.lastWrite) != 1 || wire.lastWrite[0] != 5 {
		t.Fatal("locally rejected items reached the write RPC")
	}
}

func TestBatchWriteUsesServerOutcomeDespiteAccessRightsHint(t *testing.T) {
	for _, test := range []struct {
		name      string
		hresult   int32
		transport error
	}{
		{name: "accepted"},
		{name: "server rejects bad rights", hresult: -1073479674},
		{name: "transport failure", transport: io.ErrUnexpectedEOF},
	} {
		t.Run(test.name, func(t *testing.T) {
			c, g, wire := testPersistent()
			conn := &outcomeBatchConn{
				batchConn: wire,
				afterInvoke: func(op dcerpc.Operation, _ int32) error {
					switch response := op.(*opcOp).resp.(type) {
					case *batchAddResponse:
						response.items[0].rights = 1
					case *batchWriteResponse:
						if test.transport != nil {
							return test.transport
						}
						response.errors[0] = test.hresult
						if test.hresult < 0 {
							response.hresult = 1
						}
					}
					return nil
				},
			}
			g.itemConn, g.syncConn = conn, conn
			items, err := c.addItems(t.Context(), 1, []string{"A"})
			if err != nil || len(items) != 1 || items[0].Error != nil {
				t.Fatalf("registering item: %v, %v", items, err)
			}
			before := wire.calls.Load()
			result, err := c.write(t.Context(), 1, map[string]any{"A": float32(1)})
			itemErr, ok := result["A"]
			if !ok || len(result) != 1 {
				t.Fatalf("missing write outcome: %v", result)
			}
			switch {
			case test.transport != nil:
				if !errors.Is(err, test.transport) || !errors.Is(err, ErrWriteOutcomeUnknown) ||
					!errors.Is(
						itemErr,
						test.transport,
					) || !errors.Is(itemErr, ErrWriteOutcomeUnknown) {
					t.Errorf("lost uncertain write outcome: %v, %v", itemErr, err)
				}
			case test.hresult < 0:
				var hr *HRESULTError
				if err != nil || !errors.As(itemErr, &hr) || hr.Code != 0xc0040006 ||
					hr.ItemID != "A" || hr.Operation != "Write" {
					t.Errorf("lost server bad-rights HRESULT: %v, %v", itemErr, err)
				}
			case err != nil || itemErr != nil:
				t.Errorf("server-accepted write failed: %v, %v", itemErr, err)
			}
			if errors.Is(itemErr, ErrWriteNotAttempted) {
				t.Errorf("explicit write was rejected by stale access rights: %v", itemErr)
			}
			if wire.calls.Load() != before+1 || len(wire.lastWrite) != 1 || wire.lastWrite[0] != 1 {
				t.Errorf("explicit write must send exactly one RPC: calls = %d, handles = %v",
					wire.calls.Load()-before, wire.lastWrite)
			}
		})
	}
}

func TestBatchReadGroupUsesCurrentRegistrationOrder(t *testing.T) {
	c, g, wire := testPersistent()
	g.items["gamma"] = registeredItem{handle: 30, client: 2, rights: 3}
	g.items["alpha"] = registeredItem{handle: 10, client: 3, rights: 3}
	g.items["beta"] = registeredItem{handle: 20, client: 1, rights: 3}
	result, err := c.readGroup(t.Context(), 1, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(result) != 3 || result[0].ItemID != "beta" || result[1].ItemID != "gamma" ||
		result[2].ItemID != "alpha" {
		t.Fatalf("read did not follow registration order: %+v", result)
	}
	delete(g.items, "gamma")
	g.items["delta"] = registeredItem{handle: 40, client: 4, rights: 3}
	result, err = c.readGroup(t.Context(), 1, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(result) != 3 || result[0].ItemID != "beta" || result[1].ItemID != "alpha" ||
		result[2].ItemID != "delta" {
		t.Fatalf("read did not use the current registry: %+v", result)
	}
	result, err = c.read(t.Context(), 1, nil, true)
	if err != nil || len(result) != 0 || wire.calls.Load() != 2 {
		t.Fatalf("empty explicit selection read registered items: %+v, %v", result, err)
	}
}
