package opcda

import (
	"context"
	"fmt"

	"github.com/oiweiwei/go-msrpc/msrpc/dtyp"
	"github.com/oiweiwei/go-msrpc/ndr"
	iopcserverlist "github.com/oiweiwei/go-opcda/opc/opccomn/iopcserverlist/v0"
)

type progIDRequest struct{ progID string }

func (r *progIDRequest) MarshalNDR(ctx context.Context, w ndr.Writer) error {
	return (&iopcserverlist.ClassIDFromProgrammaticIDRequest{
		This:           orpcThis(),
		ProgrammaticID: r.progID,
	}).MarshalNDR(ctx, w)
}

type progIDResponse struct {
	clsid   dtyp.GUID
	hresult int32
}

func (r *progIDResponse) UnmarshalNDR(ctx context.Context, w ndr.Reader) error {
	*r = progIDResponse{}
	resp := &iopcserverlist.ClassIDFromProgrammaticIDResponse{}
	if err := resp.UnmarshalNDR(ctx, w); err != nil {
		return err
	}
	if resp.ClassID != nil {
		r.clsid = *resp.ClassID.GUID()
	}
	r.hresult = resp.Return
	return nil
}

func (r *progIDResponse) result(progID string) (string, error) {
	if r.hresult < 0 {
		return "", hresultError("OPCEnum CLSIDFromProgID", progID, r.hresult)
	}
	clsid := r.clsid.UUID().String()
	if _, err := parseGUID(clsid); err != nil {
		return "", fmt.Errorf("OPCEnum returned invalid CLSID: %w", err)
	}
	return clsid, nil
}
