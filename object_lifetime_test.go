package opcda

import (
	"context"
	"errors"
	"testing"
	"testing/synctest"
)

func TestCloseWaitsForObjectCleanup(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		lifetime, cancel := context.WithCancel(t.Context())
		defer cancel()
		wire := &cleanupTransport{ctx: lifetime}
		c := &dcomConn{rpcCtx: lifetime, rpcCancel: cancel, serverConn: wire}
		if err := c.beginObjectOperation(); err != nil {
			t.Fatal(err)
		}
		closed := make(chan error, 1)
		go func() { closed <- c.closeContext(t.Context()) }()
		synctest.Wait()
		if wire.closed || lifetime.Err() != nil {
			t.Fatal("session closed before object cleanup")
		}
		if err := c.beginObjectOperation(); !errors.Is(err, ErrClosed) {
			t.Fatalf("operation admitted during close: %v", err)
		}
		cleanup, done := cleanupContext()
		if err := wire.Invoke(cleanup, nil); err != nil {
			t.Fatal(err)
		}
		done()
		c.groupOps.Done()
		if err := <-closed; err != nil {
			t.Fatal(err)
		}
		if !wire.closed || !errors.Is(lifetime.Err(), context.Canceled) {
			t.Fatal("session was not closed after cleanup")
		}
	})
}

func TestObjectOperationsRejectClosedSessionAndReleaseAdmission(t *testing.T) {
	for _, operation := range []string{"read", "write", "browse", "properties"} {
		t.Run(operation, func(t *testing.T) {
			c := &dcomConn{closed: true}
			call := func(ctx context.Context) error {
				switch operation {
				case "read":
					_, err := c.readItem(ctx, "Item")
					return err
				case "write":
					return c.writeItem(ctx, "Item", int32(1))
				case "browse":
					_, err := c.browseItemIDs(ctx)
					return err
				default:
					_, err := c.itemProperties(ctx, "Item")
					return err
				}
			}
			if err := call(t.Context()); !errors.Is(err, ErrClosed) {
				t.Fatalf("closed session: %v", err)
			}
			c.closed = false
			ctx, cancel := context.WithCancel(t.Context())
			cancel()
			if err := call(ctx); !errors.Is(err, context.Canceled) {
				t.Fatalf("canceled operation: %v", err)
			}
			c.groupOps.Wait()
		})
	}
}
