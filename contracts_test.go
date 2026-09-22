package opcda

import (
	"context"
	"errors"
	"fmt"
	"io"
	"testing"
	"time"

	"github.com/oiweiwei/go-msrpc/dcerpc"
)

func TestHRESULTClassification(t *testing.T) {
	err := hresultError("Read", "A", int32(-1))
	var hr *HRESULTError
	if !errors.As(err, &hr) || hr.Code != 0xffffffff || hr.ItemID != "A" {
		t.Fatal(err)
	}
}

func TestWriteUnknownPreservesTransportCause(t *testing.T) {
	c, g, wire := testPersistent()
	g.items["A"] = registeredItem{handle: 1, rights: 3}
	wire.fail = io.EOF
	result, err := c.write(context.Background(), 1, map[string]any{"A": float32(1)})
	if !errors.Is(err, ErrWriteOutcomeUnknown) || !errors.Is(err, io.EOF) ||
		!errors.Is(result["A"], ErrWriteOutcomeUnknown) {
		t.Fatalf("%v %v", err, result)
	}
	if wire.calls.Load() != 1 {
		t.Fatal("write retried")
	}
}

type contractConn struct {
	connection
	status func(context.Context) (*ServerStatus, error)
	close  func(context.Context) error
}

func (c *contractConn) getServerStatus(ctx context.Context) (*ServerStatus, error) {
	return c.status(ctx)
}

func (c *contractConn) closeContext(ctx context.Context) error {
	if c.close != nil {
		return c.close(ctx)
	}
	return nil
}

func testConnect(ctx context.Context, c *contractConn) (*Server, error) {
	return connect(
		ctx,
		ServerConfig{},
		func(context.Context, ServerConfig) (connection, error) { return c, nil },
	)
}

func TestSetupCancellationDoesNotCancelEstablishedSession(t *testing.T) {
	for range 100 {
		setup, cancel := context.WithCancel(context.Background())
		closed := 0
		c := &contractConn{
			status: func(ctx context.Context) (*ServerStatus, error) { return &ServerStatus{}, ctx.Err() },
			close:  func(context.Context) error { closed++; return nil },
		}
		s, err := testConnect(setup, c)
		if err != nil {
			t.Fatal(err)
		}
		cancel()
		if _, err = s.GetServerStatus(context.Background()); err != nil {
			t.Fatal(err)
		}
		if err = s.Close(context.Background()); err != nil {
			t.Fatal(err)
		}
		if _, err = s.GetServerStatus(context.Background()); !errors.Is(err, ErrClosed) {
			t.Fatal(err)
		}
		if err = s.Close(context.Background()); err != nil || closed != 1 {
			t.Fatal("duplicate cleanup", err, closed)
		}
	}
}

func TestCancelledSetupDoesNotDial(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := connect(ctx, ServerConfig{}, func(context.Context, ServerConfig) (connection, error) {
		t.Fatal("dialed cancelled setup")
		return nil, nil
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestCancellationAtDialCompletionCleansConnection(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cleaned := false
	_, err := connect(ctx, ServerConfig{}, func(context.Context, ServerConfig) (connection, error) {
		cancel()
		return &contractConn{close: func(context.Context) error { cleaned = true; return nil }}, nil
	})
	if !cleaned || !errors.Is(err, context.Canceled) {
		t.Fatal(cleaned, err)
	}
}

func TestStatusDeadlineAndCloseCancellation(t *testing.T) {
	started := make(chan struct{}, 2)
	c := &contractConn{status: func(ctx context.Context) (*ServerStatus, error) {
		started <- struct{}{}
		<-ctx.Done()
		return nil, ctx.Err()
	}}
	s, err := testConnect(context.Background(), c)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, err = s.GetServerStatus(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
	<-started
	done := make(chan error, 1)
	go func() { _, err := s.GetServerStatus(context.Background()); done <- err }()
	<-started
	if err = s.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err = <-done; !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestConcurrentCloseHonorsWaiterDeadlineAndRetainsFailure(t *testing.T) {
	entered, resume := make(chan struct{}), make(chan struct{})
	c := &contractConn{
		close: func(context.Context) error { close(entered); <-resume; return io.ErrUnexpectedEOF },
	}
	s, err := testConnect(context.Background(), c)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- s.Close(context.Background()) }()
	<-entered
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err = s.Close(ctx); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	close(resume)
	if err = <-done; !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatal(err)
	}
	if err = s.Close(context.Background()); !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatal("cleanup failure hidden", err)
	}
}

func TestItemErrorsExposeHRESULT(t *testing.T) {
	c, _, _ := testPersistent()
	items, err := c.addItems(context.Background(), 1, []string{"missing"})
	if err != nil {
		t.Fatal(err)
	}
	var hr *HRESULTError
	if !errors.As(items[0].Error, &hr) || hr.ItemID != "missing" {
		t.Fatal(items[0].Error)
	}
	r := &batchReadResponse{count: 1, states: []readOneResponse{{}}, errors: []int32{-1}}
	values, err := r.results([]string{"A"})
	if err != nil {
		t.Fatal(err)
	}
	if !errors.As(values[0].Error, &hr) || hr.ItemID != "A" {
		t.Fatal(values[0].Error)
	}
}

func TestReadTransportFailureReleasesGroupGate(t *testing.T) {
	c, g, wire := testPersistent()
	g.items["A"] = registeredItem{handle: 1, rights: 3}
	wire.fail = io.EOF
	if _, err := c.read(context.Background(), 1, []string{"A"}, true); !errors.Is(err, io.EOF) {
		t.Fatal(err)
	}
	wire.fail = nil
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if _, err := c.read(ctx, 1, []string{"A"}, true); err != nil {
		t.Fatal(err)
	}
	if wire.calls.Load() != 2 {
		t.Fatal("unexpected implicit retry")
	}
}

type failingBatchConn struct {
	dcerpc.Conn
	calls int
}

func (c *failingBatchConn) Invoke(
	ctx context.Context,
	op dcerpc.Operation,
	opts ...dcerpc.CallOption,
) error {
	c.calls++
	if c.calls == 2 {
		return io.ErrUnexpectedEOF
	}
	call := op.(*opcOp)
	req := call.req.(*batchWriteRequest)
	r := call.resp.(*batchWriteResponse)
	r.errors = make([]int32, len(req.handles))
	return nil
}

func TestWriteFailurePreservesCompletedAndUnattemptedBatches(t *testing.T) {
	c, g, _ := testPersistent()
	wire := &failingBatchConn{}
	g.syncConn = wire
	values := make(map[string]any, 2001)
	for i := range 2001 {
		id := fmt.Sprintf("tag%04d", i)
		g.items[id] = registeredItem{handle: uint32(i + 1), rights: 3}
		values[id] = float32(i)
	}
	result, err := c.write(context.Background(), 1, values)
	if !errors.Is(err, ErrWriteOutcomeUnknown) || !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatal(err)
	}
	for i := range 2001 {
		e := result[fmt.Sprintf("tag%04d", i)]
		switch {
		case i < 1000:
			if e != nil {
				t.Fatal(i, e)
			}
		case i < 2000:
			if !errors.Is(e, ErrWriteOutcomeUnknown) {
				t.Fatal(i, e)
			}
		default:
			if !errors.Is(e, ErrWriteNotAttempted) {
				t.Fatal(i, e)
			}
		}
	}
	if wire.calls != 2 {
		t.Fatal("retried or sent later batch", wire.calls)
	}
}

func TestSingleWriteResultClassification(t *testing.T) {
	r := &writeOneResponse{hasError: true, itemError: -1}
	var hr *HRESULTError
	if err := r.check(); !errors.As(err, &hr) || hr.Code != 0xffffffff {
		t.Fatal(err)
	}
	if err := (&writeOneResponse{}).check(); !errors.Is(err, ErrWriteOutcomeUnknown) {
		t.Fatal(err)
	}
}

func TestClosedAndRemovedGroupClassification(t *testing.T) {
	c, g, _ := testPersistent()
	g.closed = true
	if _, err := c.read(
		context.Background(),
		1,
		[]string{"A"},
		true,
	); !errors.Is(
		err,
		ErrGroupClosed,
	) {
		t.Fatal(err)
	}
	if _, err := c.read(
		context.Background(),
		99,
		[]string{"A"},
		true,
	); !errors.Is(
		err,
		ErrGroupClosed,
	) {
		t.Fatal(err)
	}
}
