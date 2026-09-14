package opcda

import (
	"context"
	"fmt"
	"unicode/utf16"

	"github.com/oiweiwei/go-msrpc/msrpc/dtyp"
	"github.com/oiweiwei/go-msrpc/ndr"
	iopcserverlist "github.com/oiweiwei/go-opcda/opc/opccomn/iopcserverlist/v0"
)

type progIDRequest struct{ progID string }

func (r *progIDRequest) MarshalNDR(ctx context.Context, w ndr.Writer) error {
	units := utf16.Encode([]rune(r.progID))
	return (&iopcserverlist.ClassIDFromProgrammaticIDRequest{
		This:           orpcThis(),
		ProgrammaticID: r.progID,
	}).MarshalNDR(ctx, &progIDWriter{Writer: w, units: uint64(len(units) + 1)})
}

// progIDWriter corrects go-msrpc v1.5.4's UTF16NLen byte-based count while
// leaving the generated CLSIDFromProgID operation layout in control.
type progIDWriter struct {
	ndr.Writer
	units uint64
	size  int
}

func (w *progIDWriter) WriteSize(size uint64) error {
	if w.size == 0 || w.size == 2 {
		size = w.units
	}
	w.size++
	return w.Writer.WriteSize(size)
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
