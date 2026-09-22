package opcda

import (
	"context"
	"errors"
	"fmt"
	"time"

	dcom "github.com/oiweiwei/go-msrpc/msrpc/dcom"
	"github.com/oiweiwei/go-msrpc/ndr"
	iopcserver "github.com/oiweiwei/go-opcda/opc/opcda/iopcserver/v0"
)

type addGroupReq struct {
	ORPCThis        *dcom.ORPCThis
	Name            string
	Active          int32
	ReqUpdateRate   uint32
	ClientHandle    int32
	TimeBias        *int32
	PercentDeadband *float32
	LCID            uint32
	RIID            *dcom.IID
}

type addGroupResp struct {
	ORPCThat     *dcom.ORPCThat
	ServerHandle int32
	RevisedRate  uint32
	GroupIface   *dcom.InterfacePointer
	Return       int32
}

func (r *addGroupReq) MarshalNDR(ctx context.Context, w ndr.Writer) error {
	this := r.ORPCThis
	if this == nil {
		this = &dcom.ORPCThis{
			Version: &dcom.COMVersion{MajorVersion: 5, MinorVersion: 7},
			CID:     &dcom.CID{},
		}
	}
	riid := r.RIID
	if riid == nil {
		riid = iopcItemMgtIID
	}
	request := &iopcserver.AddGroupRequest{
		This:                this,
		Name:                r.Name,
		Active:              r.Active != 0,
		RequestedUpdateRate: r.ReqUpdateRate,
		ClientGroup:         uint32(r.ClientHandle),
		LCID:                r.LCID,
		RIID:                riid,
	}
	if r.TimeBias != nil {
		request.TimeBias = *r.TimeBias
	} else {
		request.NullMask |= iopcserver.AddGroupNullMaskTimeBias
	}
	if r.PercentDeadband != nil {
		request.PercentDeadband = *r.PercentDeadband
	} else {
		request.NullMask |= iopcserver.AddGroupNullMaskPercentDeadband
	}
	return request.MarshalNDR(ctx, bindingsWriter{w})
}

func (r *addGroupResp) UnmarshalNDR(ctx context.Context, rd ndr.Reader) error {
	response := &iopcserver.AddGroupResponse{}
	if err := response.UnmarshalNDR(ctx, bindingsReader{Reader: rd}); err != nil {
		return err
	}
	*r = addGroupResp{
		ORPCThat:     response.That,
		ServerHandle: int32(response.ServerGroup),
		RevisedRate:  response.RevisedUpdateRate,
		Return:       response.Return,
	}
	if response.Unknown != nil {
		r.GroupIface = response.Unknown.InterfacePointer()
	}
	return nil
}

func (r *addGroupResp) MarshalNDR(ctx context.Context, w ndr.Writer) error {
	var unknown *dcom.Unknown
	if r.GroupIface != nil {
		unknown = &dcom.Unknown{DataCount: r.GroupIface.DataCount, Data: r.GroupIface.Data}
	}
	return (&iopcserver.AddGroupResponse{
		That:              r.ORPCThat,
		ServerGroup:       uint32(r.ServerHandle),
		RevisedUpdateRate: r.RevisedRate,
		Unknown:           unknown,
		Return:            r.Return,
	}).MarshalNDR(ctx, bindingsWriter{w})
}

// ── IOPCServer::GetStatus (wire opnum 6, including IUnknown methods) ────────

type getStatusReq struct {
	ORPCThis *dcom.ORPCThis
}

func (r *getStatusReq) MarshalNDR(ctx context.Context, w ndr.Writer) error {
	this := r.ORPCThis
	if this == nil {
		this = orpcThis()
	}
	return (&iopcserver.GetStatusRequest{This: this}).MarshalNDR(ctx, bindingsWriter{w})
}

func (r *getStatusReq) UnmarshalNDR(ctx context.Context, rd ndr.Reader) error {
	request := &iopcserver.GetStatusRequest{}
	if err := request.UnmarshalNDR(ctx, bindingsReader{Reader: rd}); err != nil {
		return err
	}
	r.ORPCThis = request.This
	return nil
}

// serverStatusWire follows OPCSERVERSTATUS in opcda.idl. FILETIME is two
// DWORDs (4-byte alignment), not an NDR hyper integer (8-byte alignment).
type serverStatusWire struct {
	Times                         [6]uint32
	State                         uint16
	Groups, Bandwidth             uint32
	Major, Minor, Build, Reserved uint16
	Vendor                        string
}

type getStatusResp struct {
	ORPCThat   *dcom.ORPCThat
	StatusData *serverStatusWire
	Return     int32
}

func (r *getStatusResp) UnmarshalNDR(ctx context.Context, rd ndr.Reader) error {
	response := &iopcserver.GetStatusResponse{}
	if err := response.UnmarshalNDR(ctx, bindingsReader{Reader: rd}); err != nil {
		return err
	}
	*r = getStatusResp{ORPCThat: response.That, Return: response.Return}
	if response.ServerStatus == nil {
		return nil
	}
	status := response.ServerStatus
	r.StatusData = &serverStatusWire{
		State:     uint16(status.ServerState),
		Groups:    status.GroupCount,
		Bandwidth: status.Bandwidth,
		Major:     status.MajorVersion,
		Minor:     status.MinorVersion,
		Build:     status.BuildNumber,
		Vendor:    status.VendorInformation,
	}
	if status.StartTime != nil {
		r.StatusData.Times[0], r.StatusData.Times[1] = status.StartTime.LowDateTime, status.StartTime.HighDateTime
	}
	if status.CurrentTime != nil {
		r.StatusData.Times[2], r.StatusData.Times[3] = status.CurrentTime.LowDateTime, status.CurrentTime.HighDateTime
	}
	if status.LastUpdateTime != nil {
		r.StatusData.Times[4], r.StatusData.Times[5] = status.LastUpdateTime.LowDateTime, status.LastUpdateTime.HighDateTime
	}
	return nil
}

func (r *getStatusResp) serverStatus() (*ServerStatus, error) {
	if r.Return < 0 {
		return nil, hresultError("GetStatus", "", r.Return)
	}
	if r.StatusData == nil {
		return nil, errors.New("GetStatus: successful response contains null status")
	}
	s := r.StatusData
	filetime := func(i int) time.Time {
		ticks := uint64(s.Times[i]) | uint64(s.Times[i+1])<<32
		return time.Unix(int64(ticks/10000000)-11644473600, int64(ticks%10000000)*100).UTC()
	}
	return &ServerStatus{
		StartTime:      filetime(0),
		CurrentTime:    filetime(2),
		LastUpdateTime: filetime(4),
		VendorInfo:     s.Vendor,
		ProductVersion: fmt.Sprintf("%d.%d.%d", s.Major, s.Minor, s.Build),
		State:          int(s.State),
	}, nil
}

const getStatusOpnum = 6

func (c *dcomConn) getServerStatus(ctx context.Context) (*ServerStatus, error) {
	if err := c.keepaliveError(); err != nil {
		return nil, err
	}
	req := &getStatusReq{
		ORPCThis: &dcom.ORPCThis{Version: &dcom.COMVersion{MajorVersion: 5, MinorVersion: 7}},
	}
	resp := &getStatusResp{}
	if err := c.invokeDCOM(ctx, iopcServerIID.GUID(), getStatusOpnum, req, resp); err != nil {
		return nil, fmt.Errorf("GetStatus (opnum 6): %w", err)
	}
	return resp.serverStatus()
}
