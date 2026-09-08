package opcda

import (
	"context"

	"github.com/oiweiwei/go-msrpc/ndr"
)

type batchRemoveRequest struct{ handles []uint32 }

func (r *batchRemoveRequest) MarshalNDR(ctx context.Context, w ndr.Writer) error {
	if err := writeORPC(ctx, w); err != nil {
		return err
	}
	if err := w.WriteData(uint32(len(r.handles))); err != nil {
		return err
	}
	if err := w.WriteSize(uint64(len(r.handles))); err != nil {
		return err
	}
	for _, handle := range r.handles {
		if err := w.WriteData(handle); err != nil {
			return err
		}
	}
	return nil
}

type batchRemoveResponse struct {
	count   int
	errors  []int32
	hresult int32
}

func (r *batchRemoveResponse) UnmarshalNDR(ctx context.Context, w ndr.Reader) error {
	r.errors = nil
	if err := readORPC(ctx, w); err != nil {
		return err
	}
	if err := readErrors(ctx, w, r.count, &r.errors); err != nil {
		return err
	}
	return w.ReadData(&r.hresult)
}
