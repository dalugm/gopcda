package opcda

import (
	"context"
	"errors"

	"github.com/oiweiwei/go-msrpc/msrpc/dcom/oaut"
	"github.com/oiweiwei/go-msrpc/ndr"
)

type writeOneRequest struct {
	handle uint32
	value  *oaut.Variant
}

func (r *writeOneRequest) MarshalNDR(ctx context.Context, w ndr.Writer) error {
	return (&batchWriteRequest{handles: []uint32{r.handle}, values: []*oaut.Variant{r.value}}).MarshalNDR(
		ctx,
		w,
	)
}

type writeOneResponse struct {
	hasError           bool
	itemError, hresult int32
}

func (r *writeOneResponse) UnmarshalNDR(ctx context.Context, w ndr.Reader) error {
	*r = writeOneResponse{}
	response := &batchWriteResponse{count: 1}
	if err := response.UnmarshalNDR(ctx, w); err != nil {
		return err
	}
	if len(response.errors) == 1 {
		r.hasError = true
		r.itemError = response.errors[0]
	}
	r.hresult = response.hresult
	return nil
}

func (r *writeOneResponse) check() error {
	if r.hresult < 0 {
		return hresultError("Write", "", r.hresult)
	}
	if !r.hasError {
		return unknownWrite(errors.New("Write: missing item HRESULT"))
	}
	if r.itemError < 0 {
		return hresultError("Write", "", r.itemError)
	}
	return nil
}
