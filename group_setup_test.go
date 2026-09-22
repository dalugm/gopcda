package opcda

import (
	"context"
	"errors"
	"testing"
	"testing/synctest"

	"github.com/oiweiwei/go-msrpc/dcerpc"
	"github.com/oiweiwei/go-msrpc/midl/uuid"
	"github.com/oiweiwei/go-msrpc/msrpc/dcom"
	rem "github.com/oiweiwei/go-msrpc/msrpc/dcom/iremunknown/v0"
	"github.com/oiweiwei/go-msrpc/ndr"
)

type groupSetupConn struct {
	dcerpc.Conn
	lifetime       context.Context
	groupReference *dcom.InterfacePointer
	removed        int
	released       int
	closed         bool
}

func (c *groupSetupConn) Bind(
	context.Context,
	...dcerpc.Option,
) (dcerpc.Conn, error) {
	return c, nil
}

func (c *groupSetupConn) Close(
	context.Context,
) error {
	c.closed = true
	return nil
}

func (c *groupSetupConn) Invoke(
	ctx context.Context,
	op dcerpc.Operation,
	_ ...dcerpc.CallOption,
) error {
	if err := errors.Join(ctx.Err(), c.lifetime.Err()); err != nil {
		return err
	}
	if local, ok := op.(*opcOp); ok {
		switch r := local.resp.(type) {
		case *addGroupResp:
			*r = addGroupResp{ServerHandle: 3, RevisedRate: 500, GroupIface: c.groupReference}
		case *hresultResponse:
			c.removed++
		default:
			return errors.New("unexpected group setup operation")
		}
		return nil
	}
	switch op.OpNum() {
	case 3:
		return operationResponse(op, &rem.RemoteQueryInterfaceResponse{
			IIDsCount: 2,
			QueryInterfaceResults: []*dcom.RemoteQueryInterfaceResult{
				{
					Std: &dcom.StdObjectReference{
						IPID:                  &dcom.IPID{Data1: 11},
						PublicReferencesCount: 1,
					},
				},
				{
					Std: &dcom.StdObjectReference{
						IPID:                  &dcom.IPID{Data1: 12},
						PublicReferencesCount: 1,
					},
				},
			},
		})
	case 5:
		data, err := ndr.Marshal(ndr.MarshalNDRFunc(op.MarshalNDRRequest))
		if err != nil {
			return err
		}
		var request rem.RemoteReleaseRequest
		if err := ndr.Unmarshal(data, &request); err != nil {
			return err
		}
		c.released += len(request.InterfaceReferences)
		return operationResponse(op, &rem.RemoteReleaseResponse{})
	default:
		return errors.New("unexpected remote reference operation")
	}
}

func groupSetupReference(t *testing.T) *dcom.InterfacePointer {
	t.Helper()
	data, err := ndr.Marshal(&dcom.ObjectReference{
		Signature: []byte("MEOW"),
		Flags:     1,
		IID:       iopcItemMgtIID,
		ObjectReference: &dcom.ObjectReference_ObjectReference{
			Value: &dcom.ObjectReference_Standard{
				Standard: &dcom.ObjectReferenceStandard{
					ResolverAddr: &dcom.DualStringArray{},
					Std: &dcom.StdObjectReference{
						Flags:                 0x1000,
						OXID:                  1,
						OID:                   2,
						IPID:                  &dcom.IPID{Data1: 10},
						PublicReferencesCount: 1,
					},
				},
			},
		},
	}, ndr.Opaque)
	if err != nil {
		t.Fatal(err)
	}
	ptr := &dcom.InterfacePointer{Data: data, DataCount: uint32(len(data))}
	if _, err := decodeStandardReference(ptr, iopcItemMgtIID.GUID().UUID()); err != nil {
		t.Fatalf("invalid group fixture: %v", err)
	}
	return ptr
}

func TestPersistentGroupSetupCancellationReleasesReferences(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		setup, cancel := context.WithCancel(t.Context())
		defer cancel()
		server := &groupSetupConn{lifetime: t.Context(), groupReference: groupSetupReference(t)}
		c := &dcomConn{
			rpcCtx:        t.Context(),
			serverConn:    server,
			serverOXID:    1,
			remoteUnknown: &dcom.IPID{Data1: 9},
		}
		var remote *groupSetupConn
		_, err := c.openPersistentGroup(
			setup,
			"test",
			500,
			0,
			func(ctx context.Context, iid *uuid.UUID) (dcerpc.Conn, error) {
				if iid.Equals(iopcGroupStateMgtIID.GUID().UUID()) {
					cancel()
					<-ctx.Done()
					return nil, ctx.Err()
				}
				conn := &groupSetupConn{lifetime: ctx}
				if iid.Equals(rem.RemoteUnknownSyntaxV0_0.IfUUID) {
					remote = conn
				}
				return conn, nil
			},
		)
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
		if remote == nil || remote.released != 3 || !remote.closed ||
			remote.lifetime.Err() == nil ||
			server.removed != 1 {
			t.Fatalf(
				"cancellation skipped reference cleanup: remote=%+v removed=%d err=%v",
				remote,
				server.removed,
				err,
			)
		}
	})
}

func TestPersistentGroupTransportsSurviveSetupContext(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		setup, cancel := context.WithCancel(t.Context())
		defer cancel()
		server := &groupSetupConn{lifetime: t.Context(), groupReference: groupSetupReference(t)}
		c := &dcomConn{
			rpcCtx:        t.Context(),
			serverConn:    server,
			serverOXID:    1,
			remoteUnknown: &dcom.IPID{Data1: 9},
		}
		var conns []*groupSetupConn
		g, err := c.openPersistentGroup(
			setup,
			"test",
			500,
			0,
			func(ctx context.Context, _ *uuid.UUID) (dcerpc.Conn, error) {
				conn := &groupSetupConn{lifetime: ctx}
				conns = append(conns, conn)
				return conn, nil
			},
		)
		if err != nil {
			t.Fatal(err)
		}
		cancel()
		synctest.Wait()
		for _, conn := range conns {
			if conn.lifetime.Err() != nil {
				t.Fatal("setup cancellation killed an established group transport")
			}
		}
		if err := c.disposeGroup(t.Context(), g, true); err != nil {
			t.Fatal(err)
		}
		for _, conn := range conns {
			if !conn.closed || conn.lifetime.Err() == nil {
				t.Fatal("group transport outlived cleanup")
			}
		}
	})
}
