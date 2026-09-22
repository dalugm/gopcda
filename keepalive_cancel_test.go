package opcda

import (
	"context"
	"errors"
	"io"
	"testing"
	"testing/synctest"

	"github.com/oiweiwei/go-msrpc/dcerpc"
)

func startTestKeepalive(t *testing.T, c *dcomConn) *lifecyclePingConn {
	t.Helper()
	lifetime, cancel := context.WithCancel(context.WithoutCancel(c.rpcCtx))
	c.rpcCtx = lifetime
	t.Cleanup(cancel)
	wire := &lifecyclePingConn{}
	if err := c.startKeepalive(t.Context(), func(ctx context.Context) (dcerpc.Conn, error) {
		wire.lifetime = ctx
		return wire, nil
	}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		wire.changeErr = nil
		ctx, cancel := cleanupContext()
		defer cancel()
		if err := c.stopKeepalive(ctx); err != nil {
			t.Error(err)
		}
	})
	return wire
}

func TestCanceledKeepaliveChangePreservesSession(t *testing.T) {
	c := &dcomConn{rpcCtx: t.Context(), serverOID: 7}
	wire := startTestKeepalive(t, c)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	for name, call := range map[string]func() error{
		"retain new object":      func() error { return c.retainObject(ctx, 8, 0) },
		"retain existing object": func() error { return c.retainObject(ctx, 7, 0) },
		"release object":         func() error { return c.releaseObject(ctx, 7) },
	} {
		t.Run(name, func(t *testing.T) {
			if err := call(); !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
		})
	}
	if err := c.keepaliveError(); err != nil || len(wire.changes) != 1 ||
		len(c.pinger.objects) != 1 || c.pinger.objects[7] != 1 {
		t.Fatalf(
			"cancellation changed session state: health=%v changes=%d objects=%v",
			err,
			len(wire.changes),
			c.pinger.objects,
		)
	}
	if err := c.retainObject(t.Context(), 8, 0); err != nil {
		t.Fatal(err)
	}
	if len(wire.changes) != 2 || wire.changes[1].SequenceNum != 2 {
		t.Fatal("canceled operation consumed a ping sequence number")
	}
}

func TestKeepaliveWaitIsCancellable(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		c := &dcomConn{rpcCtx: t.Context(), serverOID: 7}
		wire := startTestKeepalive(t, c)
		if err := c.pinger.acquire(t.Context()); err != nil {
			t.Fatal(err)
		}
		defer c.pinger.release()
		for _, call := range []func(context.Context) error{
			func(ctx context.Context) error { return c.retainObject(ctx, 8, 0) },
			func(ctx context.Context) error { return c.releaseObject(ctx, 7) },
		} {
			ctx, cancel := context.WithCancel(t.Context())
			done := make(chan error, 1)
			go func() { done <- call(ctx) }()
			synctest.Wait()
			cancel()
			synctest.Wait()
			select {
			case err := <-done:
				if !errors.Is(err, context.Canceled) {
					t.Fatal(err)
				}
			default:
				t.Fatal("canceled operation is still waiting for the ping gate")
			}
		}
		if c.keepaliveError() != nil || len(wire.changes) != 1 {
			t.Fatal("canceled wait changed keepalive state")
		}
	})
}

func TestDispatchedPingFailureStillInvalidatesSession(t *testing.T) {
	for _, cause := range []error{io.ErrUnexpectedEOF, context.Canceled, context.DeadlineExceeded} {
		t.Run(cause.Error(), func(t *testing.T) {
			c := &dcomConn{rpcCtx: t.Context(), serverOID: 7}
			wire := startTestKeepalive(t, c)
			wire.changeErr = cause
			if err := c.releaseObject(t.Context(), 7); !errors.Is(err, cause) {
				t.Fatal(err)
			}
			if !errors.Is(c.keepaliveError(), cause) || len(wire.changes) != 2 {
				t.Fatal("dispatched ping failure was hidden")
			}
			if c.pinger.objects[7] != 1 {
				t.Fatal("unconfirmed deletion was lost before session cleanup")
			}
		})
	}
}
