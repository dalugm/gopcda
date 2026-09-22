package opcda

import (
	"context"
	"errors"
	"io"
	"testing"
	"testing/synctest"
	"time"

	"github.com/oiweiwei/go-msrpc/dcerpc"
	exporter "github.com/oiweiwei/go-msrpc/msrpc/dcom/iobjectexporter/v0"
	"github.com/oiweiwei/go-msrpc/ndr"
)

// lifecyclePingConn exercises the generated client and records the actual RPCs.
type lifecyclePingConn struct {
	dcerpc.Conn
	lifetime context.Context
	changes  []*exporter.ComplexPingRequest
	pings    int
	closed   bool
	afterAdd func()
}

func (c *lifecyclePingConn) Bind(context.Context, ...dcerpc.Option) (dcerpc.Conn, error) {
	return c, nil
}
func (c *lifecyclePingConn) Close(context.Context) error { c.closed = true; return nil }

func (c *lifecyclePingConn) Invoke(
	ctx context.Context,
	op dcerpc.Operation,
	_ ...dcerpc.CallOption,
) error {
	if err := errors.Join(ctx.Err(), c.lifetime.Err()); err != nil {
		return err
	}
	switch op.OpNum() {
	case 1:
		c.pings++
		return operationResponse(op, &exporter.SimplePingResponse{})
	case 2:
		data, err := ndr.Marshal(ndr.MarshalNDRFunc(op.MarshalNDRRequest))
		if err != nil {
			return err
		}
		r := &exporter.ComplexPingRequest{}
		if err := ndr.Unmarshal(data, r); err != nil {
			return err
		}
		c.changes = append(c.changes, r)
		if len(r.AddToSet) > 0 && c.afterAdd != nil {
			c.afterAdd()
		}
		return operationResponse(op, &exporter.ComplexPingResponse{SetID: 123})
	default:
		return errors.New("unexpected exporter operation")
	}
}

func operationResponse(op dcerpc.Operation, response ndr.Marshaler) error {
	data, err := ndr.Marshal(response)
	if err != nil {
		return err
	}
	return ndr.Unmarshal(data, ndr.UnmarshalNDRFunc(op.UnmarshalNDRResponse))
}

func TestServerKeepaliveWithoutGroups(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		setup, cancel := context.WithCancel(t.Context())
		defer cancel()
		c := &dcomConn{rpcCtx: t.Context(), serverOID: 7}
		wire := &lifecyclePingConn{}
		if err := c.startKeepalive(setup, func(ctx context.Context) (dcerpc.Conn, error) {
			wire.lifetime = ctx
			return wire, nil
		}); err != nil {
			t.Fatal(err)
		}
		if len(wire.changes) != 1 || len(wire.changes[0].AddToSet) != 1 ||
			wire.changes[0].AddToSet[0] != 7 {
			t.Fatalf("server was not retained before group creation: %+v", wire.changes)
		}
		cancel()
		synctest.Wait()
		for range 8 {
			time.Sleep(time.Minute)
			synctest.Wait()
		}
		if wire.pings != 8 || c.keepaliveError() != nil {
			t.Fatalf(
				"idle session stopped pinging: pings=%d health=%v",
				wire.pings,
				c.keepaliveError(),
			)
		}
		if err := c.closeContext(t.Context()); err != nil {
			t.Fatal(err)
		}
		if !wire.closed || wire.lifetime.Err() == nil || len(wire.changes) != 2 ||
			len(wire.changes[1].DeleteFromSet) != 1 || wire.changes[1].DeleteFromSet[0] != 7 {
			t.Fatalf("server ping reference or transport leaked: %+v", wire)
		}
	})
}

func TestKeepaliveStartupCancellationCleansConfirmedPingSet(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	c := &dcomConn{rpcCtx: t.Context(), serverOID: 7}
	wire := &lifecyclePingConn{afterAdd: cancel}
	err := c.startKeepalive(ctx, func(ctx context.Context) (dcerpc.Conn, error) {
		wire.lifetime = ctx
		return wire, nil
	})
	if !errors.Is(err, context.Canceled) || c.pinger != nil || c.pingHealth.Load() != nil ||
		!wire.closed || wire.lifetime.Err() == nil || len(wire.changes) != 2 {
		t.Fatalf("canceled keepalive startup leaked state: err=%v wire=%+v", err, wire)
	}
	if len(wire.changes[1].DeleteFromSet) != 1 || wire.changes[1].DeleteFromSet[0] != 7 {
		t.Fatal("confirmed server registration was not removed during cleanup")
	}
}

func TestKeepaliveBindingFailureDoesNotPublishPinger(t *testing.T) {
	c := &dcomConn{rpcCtx: t.Context(), serverOID: 7}
	err := c.startKeepalive(
		t.Context(),
		func(context.Context) (dcerpc.Conn, error) { return nil, io.EOF },
	)
	if !errors.Is(err, io.EOF) || c.pinger != nil || c.pingHealth.Load() != nil {
		t.Fatal(err)
	}
}

func TestNoPingServerCanRetainGroups(t *testing.T) {
	c := &dcomConn{rpcCtx: t.Context(), serverOID: 7, serverFlags: 0x1000}
	wire := &lifecyclePingConn{}
	if err := c.startKeepalive(t.Context(), func(ctx context.Context) (dcerpc.Conn, error) {
		wire.lifetime = ctx
		return wire, nil
	}); err != nil {
		t.Fatal(err)
	}
	if len(wire.changes) != 0 {
		t.Fatal("registered a SORF_NOPING server")
	}
	if err := c.retainObject(t.Context(), 8, 0); err != nil {
		t.Fatal(err)
	}
	if err := c.releaseObject(t.Context(), 8); err != nil {
		t.Fatal(err)
	}
	if err := c.stopKeepalive(t.Context()); err != nil {
		t.Fatal(err)
	}
	if len(wire.changes) != 2 || wire.changes[0].AddToSet[0] != 8 ||
		wire.changes[1].DeleteFromSet[0] != 8 {
		t.Fatal("group keepalive followed the server's NOPING flag")
	}
}

func TestKeepaliveFailureRejectsServerOperations(t *testing.T) {
	c := &dcomConn{}
	c.pingHealth.Store(&objectPinger{err: io.EOF})
	s := &Server{ctx: t.Context(), conn: c}
	for name, call := range map[string]func() error{
		"status":     func() error { _, err := s.GetServerStatus(t.Context()); return err },
		"read":       func() error { _, err := s.ReadItem(t.Context(), "A"); return err },
		"write":      func() error { return s.WriteItem(t.Context(), "A", float32(1)) },
		"browse":     func() error { _, err := s.BrowseItemIDs(t.Context()); return err },
		"properties": func() error { _, err := s.ItemProperties(t.Context(), "A"); return err },
		"group":      func() error { _, err := s.AddGroup(t.Context(), "test", time.Second, 0); return err },
	} {
		t.Run(name, func(t *testing.T) {
			// No transport is installed: a call past the health check would panic.
			if err := call(); !errors.Is(err, io.EOF) {
				t.Fatalf("lost keepalive failure: %v", err)
			}
		})
	}
}
