package opcda

import (
	"context"
	"fmt"
	"time"

	dcom "github.com/oiweiwei/go-msrpc/msrpc/dcom"
	"github.com/oiweiwei/go-msrpc/ndr"
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
	if r.ORPCThis != nil {
		if err := r.ORPCThis.MarshalNDR(ctx, w); err != nil {
			return err
		}
	} else {
		if err := (&dcom.ORPCThis{Version: &dcom.COMVersion{MajorVersion: 5, MinorVersion: 7}, CID: &dcom.CID{}}).MarshalNDR(
			ctx,
			w,
		); err != nil {
			return err
		}
	}
	if err := w.WriteDeferred(); err != nil {
		return err
	}

	// szName is a top-level [ref,string], including when the name is empty.
	if err := ndr.WriteUTF16NString(ctx, w, r.Name); err != nil {
		return err
	}

	if err := w.WriteData(r.Active); err != nil {
		return err
	}
	if err := w.WriteData(r.ReqUpdateRate); err != nil {
		return err
	}
	if err := w.WriteData(r.ClientHandle); err != nil {
		return err
	}

	// pTimeBias: [in, unique] LONG*
	if r.TimeBias != nil {
		ptrBias := ndr.MarshalNDRFunc(func(ctx context.Context, w ndr.Writer) error {
			return w.WriteData(*r.TimeBias)
		})
		if err := w.WritePointer(r.TimeBias, ptrBias); err != nil {
			return err
		}
	} else {
		if err := w.WritePointer(nil); err != nil {
			return err
		}
	}
	if err := w.WriteDeferred(); err != nil {
		return err
	}

	// pPercentDeadband: [in, unique] FLOAT*
	if r.PercentDeadband != nil {
		ptrDb := ndr.MarshalNDRFunc(func(ctx context.Context, w ndr.Writer) error {
			return w.WriteData(*r.PercentDeadband)
		})
		if err := w.WritePointer(r.PercentDeadband, ptrDb); err != nil {
			return err
		}
	} else {
		if err := w.WritePointer(nil); err != nil {
			return err
		}
	}
	if err := w.WriteDeferred(); err != nil {
		return err
	}

	if err := w.WriteData(r.LCID); err != nil {
		return err
	}
	// riid: [in] REFIID — reference pointer to IID, marshaled inline
	if r.RIID != nil {
		if err := r.RIID.MarshalNDR(ctx, w); err != nil {
			return err
		}
	} else {
		if err := iopcItemMgtIID.MarshalNDR(ctx, w); err != nil {
			return err
		}
	}
	if err := w.WriteDeferred(); err != nil {
		return err
	}

	return nil
}

func (r *addGroupResp) UnmarshalNDR(ctx context.Context, rd ndr.Reader) error {
	return readGroupResponse(ctx, rd, r)
}

func (r *addGroupResp) MarshalNDR(ctx context.Context, w ndr.Writer) error { return nil }

// ── IOPCServer::GetStatus (wire opnum 6, including IUnknown methods) ────────

type getStatusReq struct {
	ORPCThis *dcom.ORPCThis
}

func (r *getStatusReq) MarshalNDR(ctx context.Context, w ndr.Writer) error {
	if r.ORPCThis != nil {
		if err := r.ORPCThis.MarshalNDR(ctx, w); err != nil {
			return err
		}
	} else {
		if err := (&dcom.ORPCThis{Version: &dcom.COMVersion{MajorVersion: 5, MinorVersion: 7}}).MarshalNDR(
			ctx,
			w,
		); err != nil {
			return err
		}
	}
	return w.WriteDeferred()
}

func (r *getStatusReq) UnmarshalNDR(ctx context.Context, rd ndr.Reader) error { return nil }

// serverStatusWire follows OPCSERVERSTATUS in opcda.idl. FILETIME is two
// DWORDs (4-byte alignment), not an NDR hyper integer (8-byte alignment).
type serverStatusWire struct {
	Times                         [6]uint32
	State                         uint16
	Groups, Bandwidth             uint32
	Major, Minor, Build, Reserved uint16
	Vendor                        string
}

func (s *serverStatusWire) UnmarshalNDR(ctx context.Context, r ndr.Reader) error {
	if err := r.ReadAlign(4); err != nil {
		return err
	}
	for i := range s.Times {
		if err := r.ReadData(&s.Times[i]); err != nil {
			return err
		}
	}
	for _, v := range []any{&s.State, &s.Groups, &s.Bandwidth, &s.Major, &s.Minor, &s.Build, &s.Reserved} {
		if err := r.ReadData(v); err != nil {
			return err
		}
	}
	vendor := ndr.UnmarshalNDRFunc(
		func(ctx context.Context, r ndr.Reader) error { return ndr.ReadUTF16NString(ctx, r, &s.Vendor) },
	)
	return r.ReadPointer(&s.Vendor, func(v any) { s.Vendor = *v.(*string) }, vendor)
}

type getStatusResp struct {
	ORPCThat   *dcom.ORPCThat
	StatusData *serverStatusWire
	Return     int32
}

func (r *getStatusResp) UnmarshalNDR(ctx context.Context, rd ndr.Reader) error {
	*r = getStatusResp{ORPCThat: &dcom.ORPCThat{}}
	if err := r.ORPCThat.UnmarshalNDR(ctx, rd); err != nil {
		return err
	}
	if err := rd.ReadDeferred(); err != nil {
		return err
	}
	body := ndr.UnmarshalNDRFunc(func(ctx context.Context, rd ndr.Reader) error {
		r.StatusData = &serverStatusWire{}
		return r.StatusData.UnmarshalNDR(ctx, rd)
	})
	if err := rd.ReadPointer(
		&r.StatusData,
		func(v any) { r.StatusData = *v.(**serverStatusWire) },
		body,
	); err != nil {
		return err
	}
	if err := rd.ReadDeferred(); err != nil {
		return err
	}
	return rd.ReadData(&r.Return)
}

func (r *getStatusResp) serverStatus() (*ServerStatus, error) {
	if r.Return < 0 {
		return nil, hresultError("GetStatus", "", r.Return)
	}
	if r.StatusData == nil {
		return nil, fmt.Errorf("GetStatus: successful response contains null status")
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
	req := &getStatusReq{
		ORPCThis: &dcom.ORPCThis{Version: &dcom.COMVersion{MajorVersion: 5, MinorVersion: 7}},
	}
	resp := &getStatusResp{}
	if err := c.invokeDCOM(ctx, iopcServerIID.GUID(), getStatusOpnum, req, resp); err != nil {
		return nil, fmt.Errorf("GetStatus (opnum 6): %w", err)
	}
	return resp.serverStatus()
}
