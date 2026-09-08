package opcda

import (
	"context"
	"fmt"

	"github.com/oiweiwei/go-msrpc/msrpc/dcom"
	"github.com/oiweiwei/go-msrpc/ndr"
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

func readORPC(ctx context.Context, r ndr.Reader) error {
	if err := (&dcom.ORPCThat{}).UnmarshalNDR(ctx, r); err != nil {
		return err
	}
	return r.ReadDeferred()
}

// OPC_FLAT, empty [ref,string] filter, VT_EMPTY, no access-rights filter.
// The top-level reference string has no unique-pointer referent on the wire.
type browseIDsRequest struct{}

func (*browseIDsRequest) MarshalNDR(ctx context.Context, w ndr.Writer) error {
	if err := writeORPC(ctx, w); err != nil {
		return err
	}
	if err := w.WriteData(uint16(3)); err != nil {
		return err
	}
	if err := ndr.WriteUTF16NString(ctx, w, ""); err != nil {
		return err
	}
	if err := w.WriteData(uint16(0)); err != nil {
		return err
	}
	return w.WriteData(uint32(0))
}

type browseIDsResponse struct {
	pointer *dcom.InterfacePointer
	hresult int32
}

func (r *browseIDsResponse) UnmarshalNDR(ctx context.Context, w ndr.Reader) error {
	*r = browseIDsResponse{}
	if err := readORPC(ctx, w); err != nil {
		return err
	}
	body := ndr.UnmarshalNDRFunc(func(ctx context.Context, w ndr.Reader) error {
		r.pointer = &dcom.InterfacePointer{}
		return r.pointer.UnmarshalNDR(ctx, w)
	})
	if err := w.ReadPointer(
		&r.pointer,
		func(v any) { r.pointer = *v.(**dcom.InterfacePointer) },
		body,
	); err != nil {
		return err
	}
	if err := w.ReadDeferred(); err != nil {
		return err
	}
	return w.ReadData(&r.hresult)
}

type enumNextRequest struct{ count uint32 }

func (r *enumNextRequest) MarshalNDR(ctx context.Context, w ndr.Writer) error {
	if err := writeORPC(ctx, w); err != nil {
		return err
	}
	return w.WriteData(r.count)
}

type enumNextResponse struct {
	requested uint32
	values    []string
	fetched   uint32
	hresult   int32
}

func (r *enumNextResponse) UnmarshalNDR(ctx context.Context, w ndr.Reader) error {
	*r = enumNextResponse{requested: r.requested}
	if err := readORPC(ctx, w); err != nil {
		return err
	}
	var maxCount, offset, actual uint64
	for _, v := range []*uint64{&maxCount, &offset, &actual} {
		if err := w.ReadSize(v); err != nil {
			return err
		}
	}
	if maxCount > uint64(r.requested) || offset != 0 || actual > maxCount ||
		actual > uint64(w.Len()/4) {
		return fmt.Errorf(
			"IEnumString: invalid array counts (%d,%d,%d), requested %d",
			maxCount,
			offset,
			actual,
			r.requested,
		)
	}
	r.values = make([]string, int(actual))
	for i := range r.values {
		body := ndr.UnmarshalNDRFunc(
			func(ctx context.Context, w ndr.Reader) error { return ndr.ReadUTF16NString(ctx, w, &r.values[i]) },
		)
		if err := w.ReadPointer(
			&r.values[i],
			func(v any) { r.values[i] = *v.(*string) },
			body,
		); err != nil {
			return err
		}
	}
	if err := w.ReadDeferred(); err != nil {
		return err
	}
	if err := w.ReadData(&r.fetched); err != nil {
		return err
	}
	if err := w.ReadData(&r.hresult); err != nil {
		return err
	}
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
