package opcda

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/oiweiwei/go-msrpc/dcerpc"
	"github.com/oiweiwei/go-msrpc/midl/uuid"
	"github.com/oiweiwei/go-msrpc/msrpc/dcom"
	rem "github.com/oiweiwei/go-msrpc/msrpc/dcom/iremunknown/v0"
)

// ReadItem reads one full ItemID from the device using OPC DA 2 synchronous IO.
// It creates a temporary group and removes it before returning. Bad/uncertain
// quality is returned unchanged: callers must check QualityIsGood(result.Quality).
// Standard scalar, Array and BYREF Variant values are supported; unsupported
// VARIANT types return an error. No point values are written.
func (s *Server) ReadItem(ctx context.Context, itemID string) (*ReadResult, error) {
	if itemID == "" || strings.ContainsRune(itemID, 0) {
		return nil, errors.New("opcda: ItemID must be nonempty and contain no NUL")
	}
	ctx, done := s.operationContext(ctx)
	defer done()
	if err := s.operationError(ctx); err != nil {
		return nil, err
	}
	value, err := s.conn.readItem(ctx, itemID)
	if err != nil {
		return nil, fmt.Errorf("opcda: read %q: %w", itemID, err)
	}
	return value, nil
}

func (c *dcomConn) readItem(ctx context.Context, id string) (*ReadResult, error) {
	var value *ReadResult
	err := c.withSingleItem(
		ctx,
		id,
		func(conn dcerpc.Conn, ipid *dcom.IPID, item *addOneResponse) error {
			response := &readOneResponse{}
			if err := conn.Invoke(
				ctx,
				&opcOp{
					opNum:       3,
					interfaceID: iopcSyncIOIID.GUID().UUID(),
					req:         &readOneRequest{handle: item.handle},
					resp:        response,
				},
				dcom.WithIPID(ipid),
			); err != nil {
				return fmt.Errorf("IOPCSyncIO.Read: %w", err)
			}
			var err error
			value, err = response.result(id)
			return err
		},
	)
	if err != nil {
		return nil, err
	}
	return value, nil
}

func (c *dcomConn) withSingleItem(
	ctx context.Context,
	id string,
	action func(dcerpc.Conn, *dcom.IPID, *addOneResponse) error,
) (retErr error) {
	if err := c.beginObjectOperation(); err != nil {
		return err
	}
	defer c.groupOps.Done()
	if err := ctx.Err(); err != nil {
		return err
	}
	if c.remoteUnknown == nil || c.remoteUnknown.UUID().Equals(&uuid.UUID{}) {
		return errors.New("activation omitted IRemUnknown")
	}
	remoteConn, cancelRemote, err := bindCleanupTransport(
		ctx,
		c.rpcCtx,
		func(bindCtx context.Context) (dcerpc.Conn, error) {
			return c.bindObjectInterface(bindCtx, rem.RemoteUnknownSyntaxV0_0.IfUUID)
		},
	)
	if err != nil {
		return fmt.Errorf("bind IRemUnknown: %w", err)
	}
	defer cancelRemote()
	defer func() { retErr = errors.Join(retErr, closeRPC(remoteConn)) }()
	remoteClient, err := rem.NewRemoteUnknownClient(ctx, remoteConn, dcerpc.WithNoBind(remoteConn))
	if err != nil {
		return err
	}
	remoteClient = remoteClient.IPID(ctx, c.remoteUnknown)
	group := &addGroupResp{}
	req := &addGroupReq{
		Name:          "",
		Active:        0,
		ReqUpdateRate: 1000,
		ClientHandle:  1,
		RIID:          iopcItemMgtIID,
	}
	if err := c.invokeDCOM(ctx, iopcServerIID.GUID(), 3, req, group); err != nil {
		return fmt.Errorf("AddGroup: %w", err)
	}
	if group.Return < 0 {
		return hresultError("AddGroup", "", group.Return)
	}
	var refs []*dcom.RemoteInterfaceReference
	defer func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		var releaseErr error
		if len(refs) > 0 {
			_, releaseErr = remoteClient.RemoteRelease(
				cleanup,
				&rem.RemoteReleaseRequest{
					This:                     orpcThis(),
					InterfaceReferencesCount: uint16(len(refs)),
					InterfaceReferences:      refs,
				},
			)
			if releaseErr != nil {
				releaseErr = fmt.Errorf("release item interfaces: %w", releaseErr)
			}
		}
		removed := &hresultResponse{}
		removeErr := c.invokeDCOM(
			cleanup,
			iopcServerIID.GUID(),
			7,
			&removeReadGroupRequest{handle: uint32(group.ServerHandle)},
			removed,
		)
		if removeErr == nil && removed.hresult != 0 {
			removeErr = hresultError("RemoveGroup", "", removed.hresult)
		}
		if removeErr != nil {
			removeErr = fmt.Errorf("remove temporary item group: %w", removeErr)
		}
		if err := errors.Join(releaseErr, removeErr); err != nil {
			retErr = errors.Join(retErr, err)
		}
	}()
	itemRef, err := decodeStandardReference(group.GroupIface, iopcItemMgtIID.GUID().UUID())
	if err != nil {
		return fmt.Errorf("AddGroup interface: %w", err)
	}
	if itemRef.OXID != c.serverOXID {
		return errors.New("group uses a different object exporter")
	}
	refs = append(
		refs,
		&dcom.RemoteInterfaceReference{
			IPID:                  itemRef.IPID,
			PublicReferencesCount: itemRef.PublicReferencesCount,
		},
	)
	itemConn, err := c.bindObjectInterface(ctx, iopcItemMgtIID.GUID().UUID())
	if err != nil {
		return fmt.Errorf("bind IOPCItemMgt: %w", err)
	}
	defer func() { retErr = errors.Join(retErr, closeRPC(itemConn)) }()
	added := &addOneResponse{}
	if err := itemConn.Invoke(
		ctx,
		&opcOp{
			opNum:       3,
			interfaceID: iopcItemMgtIID.GUID().UUID(),
			req:         &addOneRequest{id: id},
			resp:        added,
		},
		dcom.WithIPID(itemRef.IPID),
	); err != nil {
		return fmt.Errorf("AddItems: %w", err)
	}
	if err := added.check(); err != nil {
		return err
	}
	qi, err := remoteClient.RemoteQueryInterface(
		ctx,
		&rem.RemoteQueryInterfaceRequest{
			This:            orpcThis(),
			IPID:            itemRef.IPID.GUID(),
			ReferencesCount: 1,
			IIDsCount:       1,
			IIDs:            []*dcom.IID{iopcSyncIOIID},
		},
	)
	if err != nil {
		return fmt.Errorf("QueryInterface IOPCSyncIO: %w", err)
	}
	if len(qi.QueryInterfaceResults) != 1 || qi.QueryInterfaceResults[0] == nil {
		return errors.New("missing IOPCSyncIO result")
	}
	q := qi.QueryInterfaceResults[0]
	if q.HResult != 0 {
		return hresultError("IOPCSyncIO", "", q.HResult)
	}
	if q.Std == nil || q.Std.IPID == nil {
		return errors.New("missing IOPCSyncIO IPID")
	}
	refs = append(
		refs,
		&dcom.RemoteInterfaceReference{
			IPID:                  q.Std.IPID,
			PublicReferencesCount: q.Std.PublicReferencesCount,
		},
	)
	syncConn, err := c.bindObjectInterface(ctx, iopcSyncIOIID.GUID().UUID())
	if err != nil {
		return fmt.Errorf("bind IOPCSyncIO: %w", err)
	}
	defer func() { retErr = errors.Join(retErr, closeRPC(syncConn)) }()
	return action(syncConn, q.Std.IPID, added)
}
