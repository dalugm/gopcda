package opcda

import (
	"context"

	"github.com/oiweiwei/go-msrpc/ndr"
	itemmgt "github.com/oiweiwei/go-opcda/opc/opcda/iopcitemmgt/v0"
)

type batchRemoveRequest struct{ handles []uint32 }

func (r *batchRemoveRequest) MarshalNDR(ctx context.Context, w ndr.Writer) error {
	return (&itemmgt.RemoveItemsRequest{This: orpcThis(), Server: r.handles}).MarshalNDR(ctx, w)
}

type batchRemoveResponse struct {
	count   int
	errors  []int32
	hresult int32
}

func (r *batchRemoveResponse) UnmarshalNDR(ctx context.Context, w ndr.Reader) error {
	*r = batchRemoveResponse{count: r.count}
	response := &itemmgt.RemoveItemsResponse{}
	if err := response.UnmarshalNDR(ctx, bindingsReader{Reader: w, count: r.count}); err != nil {
		return err
	}
	r.errors, r.hresult = response.Errors, response.Return
	return nil
}
