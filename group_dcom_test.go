package opcda

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/oiweiwei/go-msrpc/dcerpc"
	"github.com/oiweiwei/go-msrpc/msrpc/dcom"
)

type batchConn struct {
	dcerpc.Conn
	calls     atomic.Int32
	active    atomic.Int32
	overlap   atomic.Bool
	fail      error
	lastWrite []uint32
}

func (c *batchConn) Invoke(
	ctx context.Context,
	op dcerpc.Operation,
	opts ...dcerpc.CallOption,
) error {
	if c.active.Add(1) != 1 {
		c.overlap.Store(true)
	}
	defer c.active.Add(-1)
	c.calls.Add(1)
	if c.fail != nil {
		return c.fail
	}
	call := op.(*opcOp)
	switch r := call.resp.(type) {
	case *batchReadResponse:
		req := call.req.(*batchReadRequest)
		r.errors = make([]int32, len(req.handles))
		r.states = make([]readOneResponse, len(req.handles))
		for i, h := range req.handles {
			v, _ := writeVariant(float32(h))
			r.states[i] = readOneResponse{variant: v, quality: 0xc0}
		}
	case *batchWriteResponse:
		req := call.req.(*batchWriteRequest)
		c.lastWrite = append([]uint32{}, req.handles...)
		r.errors = make([]int32, len(req.handles))
		if len(r.errors) > 1 {
			r.errors[1] = int32(-1073479674)
			r.hresult = 1
		}
	case *batchAddResponse:
		req := call.req.(*batchAddRequest)
		r.items = make([]addOneResponse, len(req.ids))
		r.errors = make([]int32, len(req.ids))
		for i, id := range req.ids {
			r.items[i] = addOneResponse{handle: req.clients[i], rights: 3}
			if id == "missing" {
				r.errors[i] = -1
				r.hresult = 1
			}
		}
	}
	return nil
}

func testPersistent() (*dcomConn, *persistentGroup, *batchConn) {
	wire := &batchConn{}
	g := newPersistentGroup()
	g.handle = 1
	g.syncConn = wire
	g.itemConn = wire
	g.syncIPID = &dcom.IPID{}
	g.itemIPID = &dcom.IPID{}
	c := &dcomConn{groups: map[int]*persistentGroup{1: g}}
	return c, g, wire
}

func TestPersistentReadsReuseOneBatch(t *testing.T) {
	c, g, wire := testPersistent()
	ids := make([]string, 1000)
	for i := range ids {
		ids[i] = fmt.Sprintf("tag%d", i)
		g.items[ids[i]] = registeredItem{handle: uint32(i + 1), rights: 3}
	}
	for range 5 {
		values, err := c.read(context.Background(), 1, ids, true)
		if err != nil || len(values) != 1000 || values[999].Value != float32(1000) {
			t.Fatalf("values=%d err=%v", len(values), err)
		}
	}
	if wire.calls.Load() != 5 {
		t.Fatal("expected one RPC per 1000-point cycle")
	}
}

func TestGroupReadWriteSerialization(t *testing.T) {
	c, g, wire := testPersistent()
	g.items["A"] = registeredItem{handle: 1, rights: 3}
	var wg sync.WaitGroup
	for i := range 40 {
		wg.Go(func() {
			if i%2 == 0 {
				if _, err := c.read(context.Background(), 1, []string{"A"}, true); err != nil {
					t.Error(err)
				}
			} else {
				if _, err := c.write(
					context.Background(),
					1,
					map[string]any{"A": float32(1)},
				); err != nil {
					t.Error(err)
				}
			}
		})
	}
	wg.Wait()
	if wire.overlap.Load() || wire.calls.Load() != 40 {
		t.Fatal("group calls overlapped or were lost")
	}
}

func TestBatchAddPartialAndDuplicate(t *testing.T) {
	c, g, wire := testPersistent()
	out, err := c.addItems(context.Background(), 1, []string{"A", "missing", "A", ""})
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 4 || out[0].Error != nil || out[1].Error == nil ||
		out[2].ServerHandle != out[0].ServerHandle ||
		out[3].Error == nil {
		t.Fatalf("%+v", out)
	}
	out[0].ServerHandle = 999 // exported metadata must not mutate transport handles.
	again, err := c.addItems(context.Background(), 1, []string{"A"})
	if err != nil || again[0].ServerHandle != int(g.items["A"].handle) || wire.calls.Load() != 1 {
		t.Fatal("duplicate re-added or internal handle changed")
	}
}

func TestBatchWritePartialAndNoRetry(t *testing.T) {
	c, g, wire := testPersistent()
	g.items["A"] = registeredItem{handle: 1, rights: 3}
	g.items["B"] = registeredItem{handle: 2, rights: 3}
	g.items["C"] = registeredItem{handle: 3, rights: 1}
	result, err := c.write(
		context.Background(),
		1,
		map[string]any{"B": float64(2), "A": float32(1), "C": float32(1), "missing": float32(1)},
	)
	if err != nil || result["A"] != nil || result["B"] == nil || result["C"] != nil ||
		result["missing"] == nil {
		t.Fatalf("%v %v", result, err)
	}
	if len(wire.lastWrite) != 3 || wire.lastWrite[0] != 1 || wire.lastWrite[1] != 2 ||
		wire.lastWrite[2] != 3 {
		t.Fatal("write ordering or filtering incorrect")
	}
	wire.fail = errors.New("transport timeout")
	before := wire.calls.Load()
	result, err = c.write(context.Background(), 1, map[string]any{"A": float32(1)})
	if !errors.Is(err, wire.fail) || result["A"] == nil || wire.calls.Load() != before+1 {
		t.Fatal("lost failure or retried write")
	}
}

func TestRemovedGroupAndCancelledRead(t *testing.T) {
	c, g, wire := testPersistent()
	g.closed = true
	if _, err := c.read(context.Background(), 1, []string{"A"}, true); err == nil {
		t.Fatal("read closed group")
	}
	g.closed = false
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := c.read(ctx, 1, []string{"A"}, true); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if wire.calls.Load() != 0 {
		t.Fatal("sent canceled read")
	}
}

type groupLifecycleConn struct {
	connection
	started chan struct{}
	closed  bool
}

func (c *groupLifecycleConn) addGroup(
	ctx context.Context,
	_ string,
	_ int64,
	_ float32,
) (*Group, error) {
	close(c.started)
	<-ctx.Done()
	return nil, ctx.Err()
}
func (c *groupLifecycleConn) closeContext(context.Context) error { c.closed = true; return nil }
func TestCloseCancelsGroupCreation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	c := &groupLifecycleConn{started: make(chan struct{})}
	s := &Server{ctx: ctx, cancel: cancel, conn: c}
	done := make(chan error, 1)
	go func() { _, err := s.AddGroup(context.Background(), "test", 500*time.Millisecond, 0); done <- err }()
	<-c.started
	if err := s.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if !c.closed {
		t.Fatal("transport was not closed")
	}
}

func (c *batchConn) Close(context.Context) error { return nil }

func TestFailedRemovalRetainedForClose(t *testing.T) {
	c, g, wire := testPersistent()
	c.serverConn = wire
	c.serverIPID = &dcom.IPID{}
	wire.fail = context.DeadlineExceeded
	if err := c.removeGroup(context.Background(), g.handle); err == nil {
		t.Fatal("expected removal failure")
	}
	if !c.pendingRemovals[g.handle] {
		t.Fatal("lost pending server removal")
	}
	wire.fail = nil
	before := wire.calls.Load()
	if err := c.closeContext(context.Background()); err != nil {
		t.Fatal(err)
	}
	if wire.calls.Load() != before+1 {
		t.Fatal("Close did not retry server removal")
	}
}

type removalConn struct {
	dcerpc.Conn
	entered         chan struct{}
	resume          chan struct{}
	transportClosed chan struct{}
}

func (c *removalConn) Invoke(
	ctx context.Context,
	op dcerpc.Operation,
	opts ...dcerpc.CallOption,
) error {
	close(c.entered)
	<-c.resume
	return nil
}
func (c *removalConn) Close(context.Context) error { close(c.transportClosed); return nil }
func TestCloseWaitsForInFlightRemoval(t *testing.T) {
	wire := &removalConn{
		entered:         make(chan struct{}),
		resume:          make(chan struct{}),
		transportClosed: make(chan struct{}),
	}
	g := newPersistentGroup()
	g.handle = 1
	c := &dcomConn{
		groups:     map[int]*persistentGroup{1: g},
		serverConn: wire,
		serverIPID: &dcom.IPID{},
	}
	removed := make(chan error, 1)
	go func() { removed <- c.removeGroup(context.Background(), 1) }()
	<-wire.entered
	closed := make(chan error, 1)
	go func() { closed <- c.closeContext(context.Background()) }()
	select {
	case <-wire.transportClosed:
		close(wire.resume)
		t.Fatal("transport closed while removal still in flight")
	case <-time.After(50 * time.Millisecond):
	}
	close(wire.resume)
	if err := <-removed; err != nil {
		t.Fatal(err)
	}
	if err := <-closed; err != nil {
		t.Fatal(err)
	}
}
