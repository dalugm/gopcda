package opcda

import (
	"context"
	"errors"
	"fmt"

	"github.com/oiweiwei/go-msrpc/dcerpc"
	"github.com/oiweiwei/go-msrpc/midl/uuid"
	"github.com/oiweiwei/go-msrpc/msrpc/dcom"
	"github.com/oiweiwei/go-msrpc/msrpc/dtyp"
	"github.com/oiweiwei/go-msrpc/ndr"
)

// opcOp sends generated OPC codecs over the connection and IPID already
// selected by the session. HRESULT interpretation stays in the adapters so
// successful status codes (including S_FALSE) retain their per-item results.
type opcOp struct {
	opNum       int
	interfaceID *uuid.UUID
	req         ndr.Marshaler
	resp        ndr.Unmarshaler
}

func (o *opcOp) OpNum() int     { return o.opNum }
func (o *opcOp) OpName() string { return fmt.Sprintf("/IOPC/v0/Op%d", o.opNum) }
func (o *opcOp) MarshalNDRRequest(ctx context.Context, w ndr.Writer) error {
	return o.req.MarshalNDR(ctx, w)
}
func (o *opcOp) UnmarshalNDRRequest(ctx context.Context, r ndr.Reader) error { return nil }
func (o *opcOp) MarshalNDRResponse(ctx context.Context, w ndr.Writer) error  { return nil }
func (o *opcOp) UnmarshalNDRResponse(ctx context.Context, r ndr.Reader) error {
	return o.resp.UnmarshalNDR(ctx, r)
}

func (c *dcomConn) invokeDCOM(
	ctx context.Context,
	interfaceID *dtyp.GUID,
	opNum int,
	req ndr.Marshaler,
	resp ndr.Unmarshaler,
) error {
	op := &opcOp{opNum: opNum, interfaceID: guidToUUID(interfaceID), req: req, resp: resp}
	// This connection and IPID are already bound to IOPCServer. Other
	// interfaces need their own QueryInterface result and presentation context.
	if !op.interfaceID.Equals(guidToUUID(iopcServerIID.GUID())) {
		return fmt.Errorf("dcom: interface %s is not bound", op.interfaceID)
	}
	return c.serverConn.Invoke(ctx, op, dcom.WithIPID(c.serverIPID))
}

func guidToUUID(g *dtyp.GUID) *uuid.UUID {
	if g == nil {
		return nil
	}
	return &uuid.UUID{
		TimeLow: g.Data1, TimeMid: g.Data2, TimeHiAndVersion: g.Data3,
		ClockSeqHiAndReserved: g.Data4[0], ClockSeqLow: g.Data4[1],
		Node: [6]byte{
			g.Data4[2], g.Data4[3], g.Data4[4], g.Data4[5], g.Data4[6], g.Data4[7],
		},
	}
}

// closeRPC uses a fresh, bounded context so call cancellation does not skip cleanup.
func closeRPC(conn dcerpc.Conn) error {
	ctx, cancel := cleanupContext()
	defer cancel()
	return conn.Close(ctx)
}

// beginObjectOperation reserves the session until the caller finishes remote
// cleanup and calls groupOps.Done. Admission and Close use the same lock.
func (c *dcomConn) beginObjectOperation() error {
	c.groupsMu.Lock()
	defer c.groupsMu.Unlock()
	if c.closed {
		return ErrClosed
	}
	if err := c.keepaliveError(); err != nil {
		return err
	}
	c.groupOps.Add(1)
	return nil
}

// bindCleanupTransport forwards operation cancellation only while binding. The
// transport then follows lifetime until cancel is called after remote cleanup.
func bindCleanupTransport(
	ctx context.Context,
	lifetime context.Context,
	bind func(context.Context) (dcerpc.Conn, error),
) (dcerpc.Conn, context.CancelFunc, error) {
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	transportCtx, cancel := context.WithCancel(lifetime)
	stopBind := context.AfterFunc(ctx, cancel)
	conn, err := bind(transportCtx)
	stopBind()
	if err == nil {
		err = errors.Join(ctx.Err(), transportCtx.Err())
	}
	if err != nil {
		if conn != nil {
			err = errors.Join(err, closeRPC(conn))
		}
		cancel()
		return nil, nil, err
	}
	return conn, cancel, nil
}
