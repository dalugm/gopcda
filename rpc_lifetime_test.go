package opcda

import (
	"context"
	"errors"
	"io"
	"testing"
	"testing/synctest"

	"github.com/oiweiwei/go-msrpc/dcerpc"
)

type cleanupTransport struct {
	dcerpc.Conn
	ctx    context.Context
	closed bool
}

func (c *cleanupTransport) Close(ctx context.Context) error {
	c.closed = true
	return ctx.Err()
}

func (c *cleanupTransport) Invoke(
	ctx context.Context,
	_ dcerpc.Operation,
	_ ...dcerpc.CallOption,
) error {
	if _, ok := ctx.Deadline(); !ok {
		return errors.New("cleanup call has no deadline")
	}
	return errors.Join(ctx.Err(), c.ctx.Err())
}

func TestCleanupTransportSurvivesOperationCancellation(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		var transport *cleanupTransport
		conn, closeTransport, err := bindCleanupTransport(
			ctx,
			t.Context(),
			func(bindCtx context.Context) (dcerpc.Conn, error) {
				transport = &cleanupTransport{ctx: bindCtx}
				return transport, nil
			},
		)
		if err != nil {
			t.Fatal(err)
		}
		defer closeTransport()
		cancel()
		synctest.Wait()
		cleanup, done := cleanupContext()
		defer done()
		if err := conn.Invoke(cleanup, nil); err != nil {
			t.Fatalf("operation cancellation prevented cleanup: %v", err)
		}
		if err := closeRPC(conn); err != nil {
			t.Fatal(err)
		}
		closeTransport()
		if !transport.closed || !errors.Is(transport.ctx.Err(), context.Canceled) {
			t.Fatal("transport lifetime did not end after cleanup")
		}
	})
}

func TestCleanupTransportBindingIsCancellable(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		started := make(chan struct{})
		go func() {
			<-started
			cancel()
		}()
		_, _, err := bindCleanupTransport(
			ctx,
			t.Context(),
			func(bindCtx context.Context) (dcerpc.Conn, error) {
				close(started)
				<-bindCtx.Done()
				return nil, bindCtx.Err()
			},
		)
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("bind cancellation: %v", err)
		}
	})
}

func TestCleanupTransportRejectsCancelledBindSuccess(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	var transport *cleanupTransport
	conn, done, err := bindCleanupTransport(
		ctx,
		t.Context(),
		func(bindCtx context.Context) (dcerpc.Conn, error) {
			transport = &cleanupTransport{ctx: bindCtx}
			cancel()
			return transport, nil
		},
	)
	if done != nil {
		done()
	}
	if conn != nil || !errors.Is(err, context.Canceled) || !transport.closed {
		t.Fatalf(
			"accepted or leaked canceled bind: conn=%v err=%v closed=%v",
			conn,
			err,
			transport.closed,
		)
	}
}

func TestCleanupTransportCancelsAfterBindFailure(t *testing.T) {
	var transportCtx context.Context
	_, _, err := bindCleanupTransport(
		t.Context(),
		t.Context(),
		func(bindCtx context.Context) (dcerpc.Conn, error) {
			transportCtx = bindCtx
			return nil, io.EOF
		},
	)
	if !errors.Is(err, io.EOF) || !errors.Is(transportCtx.Err(), context.Canceled) {
		t.Fatalf("failed bind lifetime: err=%v context=%v", err, transportCtx.Err())
	}
}
