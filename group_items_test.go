package opcda

import (
	"context"
	"errors"
	"fmt"
	"io"
	"slices"
	"testing"
	"testing/synctest"

	"github.com/oiweiwei/go-msrpc/dcerpc"
)

type itemRemovalConn struct {
	*batchConn
	removeCalls int
	remove      func(*batchRemoveResponse)
	removed     []uint32
}

func (c *itemRemovalConn) Invoke(
	ctx context.Context,
	op dcerpc.Operation,
	opts ...dcerpc.CallOption,
) error {
	a := op.(*opcOp)
	r, ok := a.resp.(*batchRemoveResponse)
	if !ok {
		return c.batchConn.Invoke(ctx, op, opts...)
	}
	if a.opNum != 5 {
		return fmt.Errorf("RemoveItems opnum = %d, want 5", a.opNum)
	}
	c.removeCalls++
	c.removed = append(c.removed, a.req.(*batchRemoveRequest).handles...)
	if c.fail != nil {
		return c.fail
	}
	r.errors = make([]int32, r.count)
	if c.remove != nil {
		c.remove(r)
	}
	return nil
}

func removalTestGroup(t *testing.T) (*Group, *persistentGroup, *itemRemovalConn) {
	t.Helper()
	c, persistent, wire := testPersistent()
	transport := &itemRemovalConn{batchConn: wire}
	persistent.itemConn = transport
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	return &Group{
		server: &Server{conn: c, ctx: ctx, cancel: cancel},
		handle: 1,
	}, persistent, transport
}

func TestGroupRemoveItemsUpdatesReadAndRegistration(t *testing.T) {
	g, persistent, wire := removalTestGroup(t)
	items, err := g.AddItems(context.Background(), []string{"A", "B", "C"})
	if err != nil {
		t.Fatal(err)
	}
	oldHandle := items[0].ServerHandle
	out, err := g.RemoveItems(context.Background(), []string{"A", "absent", "A"})
	if err != nil || len(out) != 2 || out["A"] != nil || out["absent"] != nil {
		t.Fatalf("RemoveItems = %v, %v", out, err)
	}
	if !slices.Equal(wire.removed, []uint32{uint32(oldHandle)}) {
		t.Fatalf("removed handles = %v", wire.removed)
	}
	if _, exists := persistent.items["A"]; exists {
		t.Fatal("removed item still registered")
	}
	read, err := g.Read(context.Background(), true)
	if err != nil || len(read) != 2 || read[0].ItemID != "B" || read[1].ItemID != "C" {
		t.Fatalf("Read = %+v, %v", read, err)
	}
	again, err := g.AddItems(context.Background(), []string{"A", "B"})
	if err != nil || again[0].ServerHandle == oldHandle ||
		again[1].ServerHandle != items[1].ServerHandle {
		t.Fatalf("AddItems = %+v, %v", again, err)
	}
}

func TestGroupRemoveItemsKeepsPerItemFailures(t *testing.T) {
	g, persistent, wire := removalTestGroup(t)
	if _, err := g.AddItems(context.Background(), []string{"A", "B"}); err != nil {
		t.Fatal(err)
	}
	wire.remove = func(r *batchRemoveResponse) { r.errors[1] = -1073479679; r.hresult = 1 }
	out, err := g.RemoveItems(context.Background(), []string{"A", "B"})
	var hr *HRESULTError
	if err != nil || out["A"] != nil || !errors.As(out["B"], &hr) {
		t.Fatalf("RemoveItems = %v, %v", out, err)
	}
	if _, exists := persistent.items["A"]; exists {
		t.Fatal("successful removal retained")
	}
	if _, exists := persistent.items["B"]; !exists {
		t.Fatal("failed removal discarded")
	}
}

func TestGroupRemoveItemsPreservesCompletedBatches(t *testing.T) {
	for _, failure := range []string{"transport", "missing", "hresult"} {
		t.Run(failure, func(t *testing.T) {
			g, persistent, wire := removalTestGroup(t)
			ids := make([]string, 2001)
			for i := range ids {
				ids[i] = fmt.Sprintf("P%04d", i)
				persistent.items[ids[i]] = registeredItem{
					handle: uint32(i + 1),
					client: uint32(i + 1),
					rights: 3,
				}
			}
			wire.remove = func(r *batchRemoveResponse) {
				if wire.removeCalls == 1 && failure == "transport" {
					wire.fail = io.EOF
				}
				if wire.removeCalls == 2 && failure == "missing" {
					r.errors = nil
				}
				if wire.removeCalls == 2 && failure == "hresult" {
					r.hresult = -1
				}
			}
			out, err := g.RemoveItems(context.Background(), ids)
			if err == nil || len(out) != len(ids) || wire.removeCalls != 2 {
				t.Fatalf("RemoveItems len=%d calls=%d err=%v", len(out), wire.removeCalls, err)
			}
			if failure == "transport" && !errors.Is(err, io.EOF) {
				t.Fatalf("lost cause: %v", err)
			}
			for i, id := range ids {
				_, registered := persistent.items[id]
				if i < 1000 && (out[id] != nil || registered) {
					t.Fatalf("lost confirmed removal: %s %v", id, out[id])
				}
				if i >= 1000 && (out[id] == nil || !registered) {
					t.Fatalf("unconfirmed removal: %s %v", id, out[id])
				}
			}
		})
	}
}

func TestGroupRemoveItemsCancellationAndEmptyRequest(t *testing.T) {
	g, _, wire := removalTestGroup(t)
	if _, err := g.AddItems(context.Background(), []string{"A"}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	out, err := g.RemoveItems(ctx, []string{"A"})
	if !errors.Is(err, context.Canceled) || !errors.Is(out["A"], context.Canceled) ||
		wire.removeCalls != 0 {
		t.Fatalf("RemoveItems = %v, %v", out, err)
	}
	if out, err := g.RemoveItems(
		context.Background(),
		nil,
	); err != nil || len(out) != 0 ||
		wire.removeCalls != 0 {
		t.Fatalf("empty removal = %v, %v", out, err)
	}
}

func TestGroupRemoveItemsCancelsWhileWaiting(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		g, persistent, wire := removalTestGroup(t)
		if err := persistent.acquire(context.Background()); err != nil {
			t.Fatal(err)
		}
		defer persistent.release()
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		done := make(chan error, 1)
		go func() { _, err := g.RemoveItems(ctx, []string{"A"}); done <- err }()
		synctest.Wait()
		cancel()
		if err := <-done; !errors.Is(err, context.Canceled) {
			t.Fatalf("RemoveItems error = %v", err)
		}
		if wire.removeCalls != 0 {
			t.Fatal("canceled removal issued RPC")
		}
	})
}
