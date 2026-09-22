package opcda

import (
	"context"
	"errors"
	"fmt"
	"io"
	"slices"
	"testing"

	"github.com/oiweiwei/go-msrpc/dcerpc"
)

type groupRemovalRecorder struct {
	batchConn
	handles []uint32
	fail    bool
}

func (c *groupRemovalRecorder) Invoke(
	ctx context.Context,
	op dcerpc.Operation,
	opts ...dcerpc.CallOption,
) error {
	if req, ok := op.(*opcOp).req.(*removeReadGroupRequest); ok {
		c.handles = append(c.handles, req.handle)
		if c.fail {
			return io.ErrUnexpectedEOF
		}
	}
	return c.batchConn.Invoke(ctx, op, opts...)
}

func registerTestGroup(t *testing.T, s *Server, handle int) (*Group, *batchConn) {
	t.Helper()
	p := newPersistentGroup()
	p.handle = handle
	wire := &batchConn{}
	p.itemConn, p.syncConn, p.stateConn = wire, wire, wire
	p.items["A"] = registeredItem{handle: 42, client: 1, rights: 3}
	g, err := s.conn.(*dcomConn).registerGroup(p)
	if err != nil {
		t.Fatal(err)
	}
	g.server = s
	return g, wire
}

func TestRemovedGroupRejectsOperationsAfterServerHandleReuse(t *testing.T) {
	serverWire := &groupRemovalRecorder{}
	c := &dcomConn{serverConn: serverWire}
	s := &Server{ctx: t.Context(), conn: c}
	old, _ := registerTestGroup(t, s, 73)
	copyOfOld := *old
	if err := old.Remove(t.Context()); err != nil {
		t.Fatal(err)
	}
	replacement, wire := registerTestGroup(t, s, 73)
	for name, call := range map[string]func() error{
		"add":  func() error { _, err := old.AddItems(t.Context(), []string{"B"}); return err },
		"read": func() error { _, err := old.Read(t.Context(), SourceCache); return err },
		"read subset": func() error {
			_, err := old.ReadItems(t.Context(), []string{"A"}, SourceDevice)
			return err
		},
		"write copied group": func() error {
			out, err := copyOfOld.Write(t.Context(), map[string]any{"A": float32(7)})
			if !errors.Is(out["A"], ErrWriteNotAttempted) || !errors.Is(out["A"], ErrGroupClosed) {
				return fmt.Errorf("removed group lost the unsent write outcome: %v", out)
			}
			return err
		},
		"set active": func() error { return old.SetActive(t.Context(), false) },
		"remove items": func() error {
			out, err := old.RemoveItems(t.Context(), []string{"A"})
			if !errors.Is(out["A"], ErrGroupClosed) {
				return fmt.Errorf("removed group lost the per-item failure: %v", out)
			}
			return err
		},
		"remove group": func() error { return old.Remove(t.Context()) },
	} {
		t.Run(name, func(t *testing.T) {
			if err := call(); !errors.Is(err, ErrGroupClosed) {
				t.Fatalf("removed group accessed its replacement: %v", err)
			}
		})
	}
	if wire.calls.Load() != 0 || !slices.Equal(serverWire.handles, []uint32{73}) {
		t.Fatalf(
			"stale group sent RPCs: calls=%d removals=%v",
			wire.calls.Load(),
			serverWire.handles,
		)
	}
	values, err := replacement.Read(t.Context(), SourceCache)
	if err != nil || len(values) != 1 || values[0].Value != float32(42) {
		t.Fatalf("replacement group is unusable: %v %v", values, err)
	}
	if err := replacement.Remove(t.Context()); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(serverWire.handles, []uint32{73, 73}) {
		t.Fatalf("local group identity leaked into the wire handle: %v", serverWire.handles)
	}
}

func TestCloseDoesNotReplayStaleRemovalAgainstReusedServerHandle(t *testing.T) {
	serverWire := &groupRemovalRecorder{fail: true}
	c := &dcomConn{serverConn: serverWire}
	s := &Server{ctx: t.Context(), conn: c}
	old, _ := registerTestGroup(t, s, 73)
	if err := old.Remove(t.Context()); !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatal(err)
	}
	// The server removed the old group, but its response was lost. A later
	// group can therefore have that handle despite the pending local cleanup.
	serverWire.fail = false
	_, _ = registerTestGroup(t, s, 73)
	if err := c.closeContext(t.Context()); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(serverWire.handles, []uint32{73, 73}) || len(c.pendingRemovals) != 0 {
		t.Fatalf(
			"stale removal was replayed: handles=%v pending=%v",
			serverWire.handles,
			c.pendingRemovals,
		)
	}
}
