package opcda

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"

	"github.com/oiweiwei/go-msrpc/dcerpc"
	"github.com/oiweiwei/go-msrpc/midl/uuid"
	"github.com/oiweiwei/go-msrpc/msrpc/dcom"
	rem "github.com/oiweiwei/go-msrpc/msrpc/dcom/iremunknown/v0"
)

type registeredItem struct {
	handle, client, rights uint32
	canonical              uint16
}
type persistentGroup struct {
	bindCtx                                context.Context
	bindCancel                             context.CancelFunc
	remCancel                              context.CancelFunc
	gate                                   chan struct{}
	closed                                 bool
	handle                                 int
	nextClientHandle                       uint32
	items                                  map[string]registeredItem
	itemConn, syncConn, stateConn, remConn dcerpc.Conn
	itemIPID, syncIPID, stateIPID          *dcom.IPID
	remoteClient                           rem.RemoteUnknownClient
	refs                                   []*dcom.RemoteInterfaceReference
	oid                                    uint64
	pinged                                 bool
	rate                                   uint32
}

func newPersistentGroup() *persistentGroup {
	g := &persistentGroup{gate: make(chan struct{}, 1), items: make(map[string]registeredItem)}
	g.gate <- struct{}{}
	return g
}

func (g *persistentGroup) acquire(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-g.gate:
	}
	if err := ctx.Err(); err != nil {
		g.release()
		return err
	}
	if g.closed {
		g.release()
		return ErrGroupClosed
	}
	return nil
}
func (g *persistentGroup) release() { g.gate <- struct{}{} }
func (c *dcomConn) getGroup(handle int) (*persistentGroup, error) {
	c.groupsMu.Lock()
	defer c.groupsMu.Unlock()
	g := c.groups[handle]
	if g == nil {
		return nil, fmt.Errorf("%w: %d", ErrGroupClosed, handle)
	}
	return g, nil
}

func (c *dcomConn) addGroup(
	ctx context.Context,
	name string,
	rate int64,
	deadband float32,
) (*Group, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := c.keepaliveError(); err != nil {
		return nil, err
	}
	if rate <= 0 || uint64(rate) > math.MaxUint32 || math.IsNaN(float64(deadband)) ||
		deadband < 0 ||
		deadband > 100 ||
		strings.ContainsRune(name, 0) {
		return nil, errors.New("invalid group name, update rate or deadband")
	}
	c.groupsMu.Lock()
	if c.closed {
		c.groupsMu.Unlock()
		return nil, ErrClosed
	}
	c.groupOps.Add(1)
	c.groupsMu.Unlock()
	defer c.groupOps.Done()
	g, err := c.openPersistentGroup(ctx, name, rate, deadband, c.bindObjectInterface)
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		cleanup, cancel := cleanupContext()
		defer cancel()
		return nil, errors.Join(err, c.disposeGroup(cleanup, g, true))
	}
	c.groupsMu.Lock()
	if c.closed {
		c.groupsMu.Unlock()
		cleanup, cancel := cleanupContext()
		defer cancel()
		return nil, errors.Join(ErrClosed, c.disposeGroup(cleanup, g, true))
	}
	defer c.groupsMu.Unlock()
	if c.groups == nil {
		c.groups = make(map[int]*persistentGroup)
	}
	c.groups[g.handle] = g
	return &Group{handle: g.handle, updateRateMs: g.rate}, nil
}

func (c *dcomConn) openPersistentGroup(
	ctx context.Context,
	name string,
	rate int64,
	deadband float32,
	bind func(context.Context, *uuid.UUID) (dcerpc.Conn, error),
) (g *persistentGroup, retErr error) {
	g = newPersistentGroup()
	g.bindCtx, g.bindCancel = context.WithCancel(c.rpcCtx)
	stopInit := context.AfterFunc(ctx, g.bindCancel)
	defer stopInit()
	created := false
	defer func() {
		if retErr != nil {
			cleanup, cancel := cleanupContext()
			defer cancel()
			retErr = errors.Join(retErr, c.disposeGroup(cleanup, g, created))
		}
	}()
	if c.remoteUnknown == nil {
		return g, errors.New("activation omitted IRemUnknown")
	}
	var err error
	// RemRelease must remain usable if a later setup call is canceled.
	g.remConn, g.remCancel, err = bindCleanupTransport(
		ctx,
		c.rpcCtx,
		func(bindCtx context.Context) (dcerpc.Conn, error) {
			return bind(bindCtx, rem.RemoteUnknownSyntaxV0_0.IfUUID)
		},
	)
	if err != nil {
		return g, err
	}
	g.remoteClient, err = rem.NewRemoteUnknownClient(ctx, g.remConn, dcerpc.WithNoBind(g.remConn))
	if err != nil {
		return g, err
	}
	g.remoteClient = g.remoteClient.IPID(ctx, c.remoteUnknown)
	response := &addGroupResp{}
	if err := c.invokeDCOM(
		ctx,
		iopcServerIID.GUID(),
		3,
		&addGroupReq{
			Name:            name,
			Active:          1,
			ReqUpdateRate:   uint32(rate),
			PercentDeadband: &deadband,
			RIID:            iopcItemMgtIID,
		},
		response,
	); err != nil {
		return g, err
	}
	if response.Return < 0 {
		return g, hresultError("AddGroup", "", response.Return)
	}
	created = true
	g.handle = int(response.ServerHandle)
	g.rate = response.RevisedRate
	ref, err := decodeStandardReference(response.GroupIface, iopcItemMgtIID.GUID().UUID())
	if err != nil {
		return g, err
	}
	g.refs = append(
		g.refs,
		&dcom.RemoteInterfaceReference{
			IPID:                  ref.IPID,
			PublicReferencesCount: ref.PublicReferencesCount,
		},
	)
	g.itemIPID = ref.IPID
	g.oid = ref.OID
	if ref.OXID != c.serverOXID {
		return g, errors.New("group belongs to another object exporter")
	}
	qi, err := g.remoteClient.RemoteQueryInterface(
		ctx,
		&rem.RemoteQueryInterfaceRequest{
			This:            orpcThis(),
			IPID:            ref.IPID.GUID(),
			ReferencesCount: 1,
			IIDsCount:       2,
			IIDs:            []*dcom.IID{iopcSyncIOIID, iopcGroupStateMgtIID},
		},
	)
	if err != nil {
		return g, err
	}
	if len(qi.QueryInterfaceResults) != 2 {
		return g, errors.New("missing group interface results")
	}
	for _, q := range qi.QueryInterfaceResults {
		if q != nil && q.HResult == 0 && q.Std != nil && q.Std.IPID != nil {
			g.refs = append(
				g.refs,
				&dcom.RemoteInterfaceReference{
					IPID:                  q.Std.IPID,
					PublicReferencesCount: q.Std.PublicReferencesCount,
				},
			)
		}
	}
	for _, q := range qi.QueryInterfaceResults {
		if q != nil && q.HResult != 0 {
			return g, hresultError("QueryInterface group", "", q.HResult)
		}
		if q == nil || q.Std == nil || q.Std.IPID == nil {
			return g, errors.New("IOPCSyncIO or IOPCGroupStateMgt unavailable")
		}
	}
	g.syncIPID = qi.QueryInterfaceResults[0].Std.IPID
	g.stateIPID = qi.QueryInterfaceResults[1].Std.IPID
	g.itemConn, err = bind(g.bindCtx, iopcItemMgtIID.GUID().UUID())
	if err != nil {
		return g, err
	}
	g.syncConn, err = bind(g.bindCtx, iopcSyncIOIID.GUID().UUID())
	if err != nil {
		return g, err
	}
	g.stateConn, err = bind(g.bindCtx, iopcGroupStateMgtIID.GUID().UUID())
	if err != nil {
		return g, err
	}
	if err := c.retainObject(ctx, g.oid, ref.Flags); err != nil {
		return g, fmt.Errorf("group keepalive: %w", err)
	}
	g.pinged = ref.Flags&0x1000 == 0
	return g, nil
}

func (c *dcomConn) disposeGroup(ctx context.Context, g *persistentGroup, created bool) error {
	if g == nil {
		return nil
	}
	if g.bindCancel != nil {
		defer g.bindCancel()
	}
	if g.remCancel != nil {
		defer g.remCancel()
	}
	var errs []error
	for _, conn := range []dcerpc.Conn{g.stateConn, g.syncConn, g.itemConn} {
		if conn != nil {
			errs = append(errs, conn.Close(ctx))
		}
	}

	if len(g.refs) > 0 && g.remoteClient != nil {
		_, err := g.remoteClient.RemoteRelease(
			ctx,
			&rem.RemoteReleaseRequest{
				This:                     orpcThis(),
				InterfaceReferencesCount: uint16(len(g.refs)),
				InterfaceReferences:      g.refs,
			},
		)
		errs = append(errs, err)
	}
	if created {
		err := c.removeRemoteGroup(ctx, g.handle)
		c.groupsMu.Lock()
		if err != nil {
			if c.pendingRemovals == nil {
				c.pendingRemovals = map[int]bool{}
			}
			c.pendingRemovals[g.handle] = true
		} else {
			delete(c.pendingRemovals, g.handle)
		}
		c.groupsMu.Unlock()
		errs = append(errs, err)
	}
	if g.pinged {
		errs = append(errs, c.releaseObject(ctx, g.oid))
	}
	for _, conn := range []dcerpc.Conn{g.remConn} {
		if conn != nil {
			errs = append(errs, conn.Close(ctx))
		}
	}
	return errors.Join(errs...)
}

func (c *dcomConn) removeGroup(ctx context.Context, handle int) error {
	c.groupsMu.Lock()
	if c.closed {
		c.groupsMu.Unlock()
		return ErrClosed
	}
	c.groupOps.Add(1)
	c.groupsMu.Unlock()
	defer c.groupOps.Done()
	return c.removeGroupInternal(ctx, handle)
}

func (c *dcomConn) removeGroupInternal(ctx context.Context, handle int) error {
	g, err := c.getGroup(handle)
	if err != nil {
		return err
	}
	if err := g.acquire(ctx); err != nil {
		return err
	}
	defer g.release()
	g.closed = true
	c.groupsMu.Lock()
	delete(c.groups, handle)
	c.groupsMu.Unlock()
	return c.disposeGroup(ctx, g, true)
}

func (c *dcomConn) setGroupActive(
	ctx context.Context,
	handle int,
	active bool,
) error {
	g, err := c.getGroup(handle)
	if err != nil {
		return err
	}
	if err := g.acquire(ctx); err != nil {
		return err
	}
	defer g.release()
	r := &groupStateResponse{}
	if err := c.keepaliveError(); err != nil {
		return err
	}
	if err := g.stateConn.Invoke(
		ctx,
		&opcOp{
			opNum:       4,
			interfaceID: iopcGroupStateMgtIID.GUID().UUID(),
			req:         &groupStateRequest{active: active},
			resp:        r,
		},
		dcom.WithIPID(g.stateIPID),
	); err != nil {
		return err
	}
	if r.hresult < 0 {
		return hresultError("SetState", "", r.hresult)
	}
	return nil
}

// Retry only RemoveGroup: replaying RemoteRelease could decrement references twice.
func (c *dcomConn) removeRemoteGroup(ctx context.Context, handle int) error {
	r := &hresultResponse{}
	err := c.invokeDCOM(
		ctx,
		iopcServerIID.GUID(),
		7,
		&removeReadGroupRequest{handle: uint32(handle)},
		r,
	)
	if err == nil && r.hresult != 0 {
		err = hresultError("RemoveGroup", "", r.hresult)
	}
	return err
}
