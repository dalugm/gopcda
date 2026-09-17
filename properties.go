package opcda

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/oiweiwei/go-msrpc/dcerpc"
	"github.com/oiweiwei/go-msrpc/msrpc/dcom"
	rem "github.com/oiweiwei/go-msrpc/msrpc/dcom/iremunknown/v0"
	props "github.com/oiweiwei/go-opcda/opc/opcda/iopcitemproperties/v0"
)

// PropertyItemDescription is the optional OPC DA item description property.
const PropertyItemDescription uint32 = 101

// ItemProperty holds one advertised property and its independently read value.
// Name describes the property (for example "Item Description"), not the item.
// DataType is the server-advertised VARIANT type. Error preserves property-level
// HRESULTs or unsupported value types; Value must not be used when Error is set.
type ItemProperty struct {
	ID       uint32
	Name     string
	DataType uint16
	Value    any
	Error    error `json:"-"`
}

// ItemProperties queries the properties advertised for a full ItemID, then reads
// their values. It creates no group and does not change periodic acquisition.
// Missing properties are absent from the slice; an empty description is a
// successful string value. Unsupported value types have per-property errors.
// Interface, transport and malformed-response failures are returned separately.
func (s *Server) ItemProperties(ctx context.Context, id string) ([]ItemProperty, error) {
	if id == "" || strings.ContainsRune(id, 0) || !utf8.ValidString(id) {
		return nil, errors.New("opcda: ItemID must be nonempty valid UTF-8 without NUL")
	}
	ctx, done := s.operationContext(ctx)
	defer done()
	if err := s.operationError(ctx); err != nil {
		return nil, err
	}
	result, err := s.conn.itemProperties(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("opcda: properties %q: %w", id, err)
	}
	return result, nil
}

func (c *dcomConn) itemProperties(ctx context.Context, id string) (_ []ItemProperty, retErr error) {
	c.groupsMu.Lock()
	if c.closed {
		c.groupsMu.Unlock()
		return nil, ErrClosed
	}
	c.groupOps.Add(1)
	c.groupsMu.Unlock()
	defer c.groupOps.Done()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if c.remoteUnknown == nil {
		return nil, errors.New("activation omitted IRemUnknown")
	}
	remoteConn, cancelRemote, err := bindCleanupTransport(
		ctx, c.rpcCtx, func(bindCtx context.Context) (dcerpc.Conn, error) {
			return c.bindObjectInterface(bindCtx, rem.RemoteUnknownSyntaxV0_0.IfUUID)
		},
	)
	if err != nil {
		return nil, fmt.Errorf("bind IRemUnknown: %w", err)
	}
	defer cancelRemote()
	defer func() { retErr = errors.Join(retErr, closeRPC(remoteConn)) }()
	remote, err := rem.NewRemoteUnknownClient(ctx, remoteConn, dcerpc.WithNoBind(remoteConn))
	if err != nil {
		return nil, err
	}
	remote = remote.IPID(ctx, c.remoteUnknown)
	qi, err := remote.RemoteQueryInterface(
		ctx,
		&rem.RemoteQueryInterfaceRequest{
			This:            orpcThis(),
			IPID:            c.serverIPID.GUID(),
			ReferencesCount: 1,
			IIDsCount:       1,
			IIDs:            []*dcom.IID{props.ItemPropertiesIID},
		},
	)
	if err != nil {
		return nil, fmt.Errorf("QueryInterface IOPCItemProperties: %w", err)
	}
	if qi == nil || len(qi.QueryInterfaceResults) != 1 || qi.QueryInterfaceResults[0] == nil {
		return nil, errors.New("IOPCItemProperties: missing QueryInterface result")
	}
	result := qi.QueryInterfaceResults[0]
	if result.HResult < 0 {
		return nil, hresultError("IOPCItemProperties unavailable", id, result.HResult)
	}
	if result.Std == nil || result.Std.IPID == nil {
		return nil, errors.New("IOPCItemProperties: missing object reference")
	}
	defer func() {
		cleanup, cancel := cleanupContext()
		defer cancel()
		response, err := remote.RemoteRelease(
			cleanup,
			&rem.RemoteReleaseRequest{
				This:                     orpcThis(),
				InterfaceReferencesCount: 1,
				InterfaceReferences: []*dcom.RemoteInterfaceReference{
					{
						IPID:                  result.Std.IPID,
						PublicReferencesCount: result.Std.PublicReferencesCount,
					},
				},
			},
		)
		if err == nil && response != nil && response.Return < 0 {
			err = hresultError("release IOPCItemProperties", id, response.Return)
		}
		if err != nil {
			retErr = errors.Join(retErr, fmt.Errorf("release IOPCItemProperties: %w", err))
		}
	}()
	if result.Std.OXID != c.serverOXID {
		return nil, errors.New("IOPCItemProperties uses a different object exporter")
	}
	conn, err := c.bindObjectInterface(ctx, props.ItemPropertiesSyntaxUUID)
	if err != nil {
		return nil, fmt.Errorf("bind IOPCItemProperties: %w", err)
	}
	defer func() { retErr = errors.Join(retErr, closeRPC(conn)) }()
	return readProperties(ctx, conn, result.Std.IPID, id)
}

func readProperties(
	ctx context.Context,
	conn dcerpc.Conn,
	ipid *dcom.IPID,
	id string,
) ([]ItemProperty, error) {
	available := &availablePropertiesResponse{}
	if err := conn.Invoke(
		ctx,
		&opcOp{
			opNum:       3,
			interfaceID: props.ItemPropertiesSyntaxUUID,
			req:         &propertyRequest{id: id},
			resp:        available,
		},
		dcom.WithIPID(ipid),
	); err != nil {
		return nil, fmt.Errorf("QueryAvailableProperties: %w", err)
	}
	if available.Return < 0 {
		return nil, hresultError("QueryAvailableProperties", id, available.Return)
	}
	result := make([]ItemProperty, len(available.PropertyIDs))
	if len(result) == 0 {
		return result, nil
	}
	values := &propertyValuesResponse{count: len(result)}
	if err := conn.Invoke(
		ctx,
		&opcOp{
			opNum:       4,
			interfaceID: props.ItemPropertiesSyntaxUUID,
			req:         &propertyRequest{id: id, ids: available.PropertyIDs},
			resp:        values,
		},
		dcom.WithIPID(ipid),
	); err != nil {
		return nil, fmt.Errorf("GetItemProperties: %w", err)
	}
	if values.Return < 0 {
		return nil, hresultError("GetItemProperties", id, values.Return)
	}
	for i, propertyID := range available.PropertyIDs {
		p := ItemProperty{
			ID:       propertyID,
			Name:     available.Descriptions[i],
			DataType: available.DataTypes[i],
		}
		if values.Errors[i] < 0 {
			p.Error = hresultError(
				fmt.Sprintf("GetItemProperties property %d", propertyID),
				id,
				values.Errors[i],
			)
		} else if values.Data[i] == nil {
			p.Error = fmt.Errorf("property %d: missing VARIANT", propertyID)
		} else {
			p.Value, p.Error = scalarValue(values.Data[i])
		}
		result[i] = p
	}
	return result, nil
}
