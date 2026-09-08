package opcda

import (
	"context"
	"fmt"

	"github.com/oiweiwei/go-msrpc/msrpc/dcom/oaut"
	"github.com/oiweiwei/go-msrpc/ndr"
)

type writeOneRequest struct {
	handle uint32
	value  *oaut.Variant
}

func (r *writeOneRequest) MarshalNDR(ctx context.Context, w ndr.Writer) error {
	if err := writeORPC(ctx, w); err != nil {
		return err
	}
	// count, conformant handle array, conformant VARIANT pointer array.
	for _, v := range []uint32{1, 1, r.handle, 1} {
		if err := w.WriteData(v); err != nil {
			return err
		}
	}
	body := ndr.MarshalNDRFunc(
		func(ctx context.Context, w ndr.Writer) error { return marshalWriteVariant(ctx, w, r.value) },
	)
	if err := w.WritePointer(r.value, body); err != nil {
		return err
	}
	return w.WriteDeferred()
}

type writeOneResponse struct {
	hasError           bool
	itemError, hresult int32
}

func (r *writeOneResponse) UnmarshalNDR(ctx context.Context, w ndr.Reader) error {
	*r = writeOneResponse{}
	if err := readORPC(ctx, w); err != nil {
		return err
	}
	if err := readSingleArray(
		ctx,
		w,
		&r.hasError,
		ndr.UnmarshalNDRFunc(
			func(ctx context.Context, w ndr.Reader) error { return w.ReadData(&r.itemError) },
		),
	); err != nil {
		return err
	}
	return w.ReadData(&r.hresult)
}

func (r *writeOneResponse) check() error {
	if r.hresult < 0 {
		return hresultError("Write", "", r.hresult)
	}
	if !r.hasError {
		return unknownWrite(fmt.Errorf("Write: missing item HRESULT"))
	}
	if r.itemError < 0 {
		return hresultError("Write", "", r.itemError)
	}
	return nil
}
