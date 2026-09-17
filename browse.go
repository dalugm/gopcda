package opcda

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/oiweiwei/go-msrpc/dcerpc"
	"github.com/oiweiwei/go-msrpc/midl/uuid"
	"github.com/oiweiwei/go-msrpc/msrpc/dcom"
	rem "github.com/oiweiwei/go-msrpc/msrpc/dcom/iremunknown/v0"
	"github.com/oiweiwei/go-msrpc/msrpc/dtyp"
	"github.com/oiweiwei/go-msrpc/ndr"
	"github.com/oiweiwei/go-msrpc/ssp"
	"github.com/oiweiwei/go-msrpc/ssp/credential"
	"github.com/oiweiwei/go-msrpc/ssp/gssapi"
)

var (
	browseIID     = uuid.MustParse("39c13a4f-011e-11d0-9675-0020afd8adb3")
	enumStringIID = uuid.MustParse("00000101-0000-0000-c000-000000000046")
)

// BrowseItemIDs returns sorted, unique, full ItemIDs using OPC DA 2 flat
// browsing (OPC_FLAT). It does not create groups or read/write point values.
// Servers without DA2 flat browsing return an error; no partial list is returned.
// ctx controls the operation; disconnecting the Server also cancels it.
func (s *Server) BrowseItemIDs(ctx context.Context) ([]string, error) {
	ctx, done := s.operationContext(ctx)
	defer done()
	if err := s.operationError(ctx); err != nil {
		return nil, err
	}
	ids, err := s.conn.browseItemIDs(ctx)
	if err != nil {
		return nil, fmt.Errorf("opcda: browse ItemIDs: %w", err)
	}
	return ids, nil
}

func collectItemIDs(
	ctx context.Context,
	next func(context.Context) (*enumNextResponse, error),
) ([]string, error) {
	seen := make(map[string]struct{})
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		batch, err := next(ctx)
		if err != nil {
			return nil, err
		}
		if batch == nil {
			return nil, errors.New("IEnumString: missing response")
		}
		if batch.hresult != 0 && batch.hresult != 1 {
			return nil, hresultError("IEnumString", "", batch.hresult)
		}
		if batch.fetched != uint32(len(batch.values)) {
			return nil, errors.New("IEnumString: inconsistent fetched count")
		}
		for _, id := range batch.values {
			if id == "" {
				return nil, errors.New("IEnumString: empty ItemID")
			}
			seen[id] = struct{}{}
		}
		// S_FALSE still carries the final partial batch.
		if batch.hresult == 1 {
			break
		}
		if batch.fetched == 0 {
			return nil, errors.New("IEnumString: enumeration made no progress")
		}
	}
	ids := make([]string, 0, len(seen))
	for id := range seen {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	return ids, nil
}

// Each object interface has its own presentation context and IPID. Separate
// connections avoid changing the existing IOPCServer connection's context.
func (c *dcomConn) bindObjectInterface(ctx context.Context, iid *uuid.UUID) (dcerpc.Conn, error) {
	ctx = gssapi.NewSecurityContext(
		ctx,
		gssapi.WithCredential(
			credential.NewFromPassword(c.cfg.Domain+"\\"+c.cfg.Username, c.cfg.Password),
		),
		gssapi.WithMechanismFactory(ssp.NTLM),
	)
	raw, err := dcerpc.Dial(
		ctx,
		c.cfg.Host,
		dcerpc.WithEndpoint("ncacn_ip_tcp:["+c.objectPort+"]"),
		dcerpc.WithTargetName(c.cfg.Host),
		dcerpc.WithMechanism(ssp.NTLM),
	)
	if err != nil {
		return nil, err
	}
	conn, err := raw.Bind(
		ctx,
		dcerpc.WithAbstractSyntax(&dcerpc.SyntaxID{IfUUID: iid}),
		dcerpc.WithSeal(),
		dcerpc.WithTargetName(c.cfg.Host),
	)
	if err != nil {
		return nil, errors.Join(err, closeRPC(raw))
	}
	return conn, nil
}

func (c *dcomConn) browseItemIDs(ctx context.Context) (ids []string, retErr error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if c.remoteUnknown == nil || c.remoteUnknown.UUID().Equals(&uuid.UUID{}) {
		return nil, errors.New("activation omitted IRemUnknown IPID")
	}
	remoteConn, err := c.bindObjectInterface(ctx, rem.RemoteUnknownSyntaxV0_0.IfUUID)
	if err != nil {
		return nil, fmt.Errorf("bind IRemUnknown: %w", err)
	}
	defer func() { retErr = errors.Join(retErr, closeRPC(remoteConn)) }()
	remoteClient, err := rem.NewRemoteUnknownClient(ctx, remoteConn, dcerpc.WithNoBind(remoteConn))
	if err != nil {
		return nil, err
	}
	remoteClient = remoteClient.IPID(ctx, c.remoteUnknown)
	qi, err := remoteClient.RemoteQueryInterface(
		ctx,
		&rem.RemoteQueryInterfaceRequest{
			This:            orpcThis(),
			IPID:            c.serverIPID.GUID(),
			ReferencesCount: 1,
			IIDsCount:       1,
			IIDs:            []*dcom.IID{(*dcom.IID)(dtyp.GUIDFromUUID(browseIID))},
		},
	)
	if err != nil {
		return nil, fmt.Errorf("QueryInterface DA2 browse: %w", err)
	}
	if len(qi.QueryInterfaceResults) != 1 || qi.QueryInterfaceResults[0] == nil {
		return nil, errors.New("QueryInterface DA2 browse: missing result")
	}
	result := qi.QueryInterfaceResults[0]
	if result.HResult != 0 {
		return nil, hresultError("DA2 browse unavailable", "", result.HResult)
	}
	if result.Std == nil || result.Std.IPID == nil {
		return nil, errors.New("DA2 browse: missing object reference")
	}
	refs := []*dcom.RemoteInterfaceReference{
		{IPID: result.Std.IPID, PublicReferencesCount: result.Std.PublicReferencesCount},
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, err := remoteClient.RemoteRelease(
			cleanup,
			&rem.RemoteReleaseRequest{
				This:                     orpcThis(),
				InterfaceReferencesCount: uint16(len(refs)),
				InterfaceReferences:      refs,
			},
		)
		if err != nil {
			ids = nil
			retErr = errors.Join(retErr, fmt.Errorf("release browse references: %w", err))
		}
	}()
	browseConn, err := c.bindObjectInterface(ctx, browseIID)
	if err != nil {
		return nil, fmt.Errorf("bind DA2 browse: %w", err)
	}
	defer func() { retErr = errors.Join(retErr, closeRPC(browseConn)) }()
	browse := &browseIDsResponse{}
	if err := browseConn.Invoke(
		ctx,
		&opcOp{opNum: 5, interfaceID: browseIID, req: &browseIDsRequest{}, resp: browse},
		dcom.WithIPID(result.Std.IPID),
	); err != nil {
		return nil, fmt.Errorf("BrowseOPCItemIDs: %w", err)
	}
	if browse.hresult != 0 {
		return nil, hresultError("BrowseOPCItemIDs", "", browse.hresult)
	}
	std, err := decodeStandardReference(browse.pointer, enumStringIID)
	if err != nil {
		return nil, fmt.Errorf("browse enumerator: %w", err)
	}
	if std.OXID != c.serverOXID {
		return nil, fmt.Errorf(
			"enumerator belongs to another object exporter; endpoint resolution is not implemented",
		)
	}
	refs = append(
		refs,
		&dcom.RemoteInterfaceReference{
			IPID:                  std.IPID,
			PublicReferencesCount: std.PublicReferencesCount,
		},
	)
	enumConn, err := c.bindObjectInterface(ctx, enumStringIID)
	if err != nil {
		return nil, fmt.Errorf("bind IEnumString: %w", err)
	}
	defer func() { retErr = errors.Join(retErr, closeRPC(enumConn)) }()
	return collectItemIDs(ctx, func(ctx context.Context) (*enumNextResponse, error) {
		const count = 500
		r := &enumNextResponse{requested: count}
		err := enumConn.Invoke(
			ctx,
			&opcOp{
				opNum:       3,
				interfaceID: enumStringIID,
				req:         &enumNextRequest{count: count},
				resp:        r,
			},
			dcom.WithIPID(std.IPID),
		)
		return r, err
	})
}

func decodeStandardReference(
	p *dcom.InterfacePointer,
	iid *uuid.UUID,
) (*dcom.StdObjectReference, error) {
	if p == nil {
		return nil, errors.New("null interface pointer")
	}
	obj := &dcom.ObjectReference{}
	if err := ndr.Unmarshal(p.Data, obj, ndr.Opaque); err != nil {
		return nil, err
	}
	if string(obj.Signature) != "MEOW" || obj.Flags != 1 || obj.IID == nil ||
		!obj.IID.GUID().UUID().Equals(iid) ||
		obj.ObjectReference == nil {
		return nil, errors.New("unexpected OBJREF signature, type or IID")
	}
	ref, ok := obj.ObjectReference.GetValue().(*dcom.ObjectReferenceStandard)
	if !ok || ref == nil || ref.Std == nil || ref.Std.IPID == nil ||
		ref.Std.IPID.UUID().Equals(&uuid.UUID{}) {
		return nil, errors.New("missing standard IPID")
	}
	return ref.Std, nil
}
