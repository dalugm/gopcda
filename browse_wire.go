package opcda

import (
	"context"
	"fmt"

	"github.com/oiweiwei/go-msrpc/msrpc/dcom"
	ienumstring "github.com/oiweiwei/go-msrpc/msrpc/dcom/urlmon/ienumstring/v0"
	"github.com/oiweiwei/go-msrpc/ndr"
	opcdabinding "github.com/oiweiwei/go-opcda/opc/opcda"
	iopcbrowse "github.com/oiweiwei/go-opcda/opc/opcda/iopcbrowseserveraddressspace/v0"
)

func orpcThis() *dcom.ORPCThis {
	return &dcom.ORPCThis{Version: &dcom.COMVersion{MajorVersion: 5, MinorVersion: 7}}
}

func writeORPC(ctx context.Context, w ndr.Writer) error {
	if err := orpcThis().MarshalNDR(ctx, w); err != nil {
		return err
	}
	return w.WriteDeferred()
}

type browseIDsRequest struct{}

func (*browseIDsRequest) MarshalNDR(ctx context.Context, w ndr.Writer) error {
	return (&iopcbrowse.BrowseOPCItemIDsRequest{
		This:             orpcThis(),
		BrowseFilterType: opcdabinding.BrowseTypeFlat,
	}).MarshalNDR(ctx, w)
}

type browseIDsResponse struct {
	pointer *dcom.InterfacePointer
	hresult int32
}

func (r *browseIDsResponse) UnmarshalNDR(ctx context.Context, w ndr.Reader) error {
	*r = browseIDsResponse{}
	resp := &iopcbrowse.BrowseOPCItemIDsResponse{}
	if err := resp.UnmarshalNDR(ctx, w); err != nil {
		return err
	}
	if resp.IEnumString != nil {
		r.pointer = resp.IEnumString.InterfacePointer()
	}
	r.hresult = resp.Return
	return nil
}

type enumNextRequest struct{ count uint32 }

func (r *enumNextRequest) MarshalNDR(ctx context.Context, w ndr.Writer) error {
	return (&ienumstring.NextRequest{This: orpcThis(), Count: r.count}).MarshalNDR(ctx, w)
}

type enumNextResponse struct {
	requested uint32
	values    []string
	fetched   uint32
	hresult   int32
}

func (r *enumNextResponse) UnmarshalNDR(ctx context.Context, w ndr.Reader) error {
	*r = enumNextResponse{requested: r.requested}
	resp := &ienumstring.NextResponse{Count: r.requested}
	reader := &enumNextReader{Reader: w, requested: r.requested}
	if err := resp.UnmarshalNDR(ctx, reader); err != nil {
		return err
	}
	r.values, r.fetched, r.hresult = resp.Entries, resp.Fetched, resp.Return
	if r.fetched != uint32(len(r.values)) || r.fetched > r.requested {
		return fmt.Errorf("IEnumString: fetched count does not match array")
	}
	if r.hresult == 0 && r.fetched != r.requested {
		return fmt.Errorf("IEnumString: S_OK without a full batch")
	}
	if r.hresult == 1 && r.fetched >= r.requested {
		return fmt.Errorf("IEnumString: S_FALSE without a partial batch")
	}
	return nil
}

// enumNextReader keeps the generated decoder from allocating an array whose
// conformant-varying counts contradict the requested batch size. The binding
// validates only that the final count fits in the remaining byte buffer.
type enumNextReader struct {
	ndr.Reader
	requested uint32
	sizes     [3]uint64
	read      int
}

func (r *enumNextReader) ReadSize(size *uint64) error {
	if err := r.Reader.ReadSize(size); err != nil {
		return err
	}
	if r.read >= len(r.sizes) {
		return nil
	}
	r.sizes[r.read] = *size
	r.read++
	maxCount, offset, actual := r.sizes[0], r.sizes[1], r.sizes[2]
	invalid := maxCount > uint64(r.requested)
	if r.read >= 2 {
		invalid = invalid || offset != 0
	}
	if r.read == len(r.sizes) {
		invalid = invalid || actual > maxCount || actual > uint64(r.Len()/4)
	}
	if invalid {
		return fmt.Errorf(
			"IEnumString: invalid array counts (%d,%d,%d), requested %d",
			maxCount,
			offset,
			actual,
			r.requested,
		)
	}
	return nil
}
