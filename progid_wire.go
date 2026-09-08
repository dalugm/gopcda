package opcda

import (
	"context"
	"fmt"

	"github.com/oiweiwei/go-msrpc/msrpc/dtyp"
	"github.com/oiweiwei/go-msrpc/ndr"
)

type progIDRequest struct{ progID string }

func (r *progIDRequest) MarshalNDR(ctx context.Context, w ndr.Writer) error {
	if err := writeORPC(ctx, w); err != nil {
		return err
	}
	// LPCOLESTR is a top-level reference to a conformant varying UTF-16 string.
	return writeUTF16String(w, r.progID)
}

type progIDResponse struct {
	clsid   dtyp.GUID
	hresult int32
}

func (r *progIDResponse) UnmarshalNDR(ctx context.Context, w ndr.Reader) error {
	*r = progIDResponse{}
	if err := readORPC(ctx, w); err != nil {
		return err
	}
	if err := r.clsid.UnmarshalNDR(ctx, w); err != nil {
		return err
	}
	return w.ReadData(&r.hresult)
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
