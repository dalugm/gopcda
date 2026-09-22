package opcda

import (
	"context"
	"errors"
	"testing"
	"testing/synctest"
)

type cancelRemovalConnection struct {
	connection
	entered chan struct{}
}

func (c *cancelRemovalConnection) removeGroup(ctx context.Context, _ int) error {
	close(c.entered)
	<-ctx.Done()
	return ctx.Err()
}

func (*cancelRemovalConnection) closeContext(context.Context) error { return nil }

func TestServerCloseCancelsPublicGroupRemoval(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		conn := &cancelRemovalConnection{entered: make(chan struct{})}
		s, err := connect(
			context.Background(),
			ServerConfig{},
			func(context.Context, ServerConfig) (connection, error) { return conn, nil },
		)
		if err != nil {
			t.Fatal(err)
		}
		g := &Group{server: s, id: 1}
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		removed := make(chan error, 1)
		go func() { removed <- g.Remove(ctx) }()
		<-conn.entered
		if err := s.Close(context.Background()); err != nil {
			t.Fatal(err)
		}
		synctest.Wait()
		select {
		case err := <-removed:
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("Remove error = %v", err)
			}
		default:
			t.Error("Server.Close did not cancel Group.Remove")
			cancel()
			<-removed
		}
	})
}

func TestGroupWritePreflightReturnsPerItemNotAttempted(t *testing.T) {
	for _, state := range []string{"canceled", "closed", "removed"} {
		t.Run(state, func(t *testing.T) {
			g, persistent, wire := removalTestGroup(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			var want error
			switch state {
			case "canceled":
				cancel()
				want = context.Canceled
			case "closed":
				g.server.closed = true
				want = ErrClosed
			case "removed":
				persistent.closed = true
				want = ErrGroupClosed
			}
			out, err := g.Write(ctx, map[string]any{"A": float32(1), "B": true})
			if !errors.Is(err, want) || len(out) != 2 || wire.calls.Load() != 0 {
				t.Fatalf("Write = %v, %v; RPCs = %d", out, err, wire.calls.Load())
			}
			for id, itemErr := range out {
				if !errors.Is(itemErr, ErrWriteNotAttempted) || !errors.Is(itemErr, want) {
					t.Fatalf("%s: lost not-attempted marker or cause: %v", id, itemErr)
				}
			}
		})
	}
}
