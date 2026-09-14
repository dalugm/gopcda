package opcda

import (
	"context"
	"fmt"

	"github.com/oiweiwei/go-msrpc/msrpc/dcom/oaut"
	"github.com/oiweiwei/go-msrpc/ndr"
	binding "github.com/oiweiwei/go-opcda/opc/opcda"
	statemgt "github.com/oiweiwei/go-opcda/opc/opcda/iopcgroupstatemgt/v0"
	itemmgt "github.com/oiweiwei/go-opcda/opc/opcda/iopcitemmgt/v0"
	syncio "github.com/oiweiwei/go-opcda/opc/opcda/iopcsyncio/v0"
)

const maxBatchItems = 1000

type batchAddRequest struct {
	ids     []string
	clients []uint32
}

func (r *batchAddRequest) MarshalNDR(ctx context.Context, w ndr.Writer) error {
	if len(r.ids) != len(r.clients) {
		return fmt.Errorf("AddItems: mismatched item and client counts")
	}
	items := make([]*binding.ItemDefinition, len(r.ids))
	for i, id := range r.ids {
		// A terminator requests a non-null empty access path from the generator.
		items[i] = &binding.ItemDefinition{
			AccessPath: "\x00",
			ItemID:     id,
			Active:     true,
			Client:     r.clients[i],
		}
	}
	return (&itemmgt.AddItemsRequest{This: orpcThis(), ItemArray: items}).MarshalNDR(
		ctx,
		bindingsWriter{w},
	)
}

type batchAddResponse struct {
	count   int
	items   []addOneResponse
	errors  []int32
	hresult int32
}

func (r *batchAddResponse) UnmarshalNDR(ctx context.Context, w ndr.Reader) error {
	*r = batchAddResponse{count: r.count}
	response := &itemmgt.AddItemsResponse{}
	if err := response.UnmarshalNDR(ctx, bindingsReader{Reader: w, count: r.count}); err != nil {
		return err
	}
	if response.AddResults != nil {
		r.items = make([]addOneResponse, len(response.AddResults))
		for i, item := range response.AddResults {
			if item == nil {
				return fmt.Errorf("AddItems: null result")
			}
			if item.Blob != nil && uint64(len(item.Blob)) != uint64(item.BlobSize) {
				return fmt.Errorf("invalid item blob length")
			}
			r.items[i] = addOneResponse{
				handle:    item.Server,
				canonical: item.CanonicalDataType,
				rights:    item.AccessRights,
			}
		}
	}
	r.errors, r.hresult = response.Errors, response.Return
	return nil
}

type batchReadRequest struct {
	handles []uint32
	cache   bool
}

func (r *batchReadRequest) MarshalNDR(ctx context.Context, w ndr.Writer) error {
	source := binding.DataSource(2)
	if r.cache {
		source = 1
	}
	return (&syncio.ReadRequest{This: orpcThis(), Source: source, Server: r.handles}).MarshalNDR(
		ctx,
		w,
	)
}

type batchReadResponse struct {
	count   int
	states  []readOneResponse
	errors  []int32
	hresult int32
}

func (r *batchReadResponse) UnmarshalNDR(ctx context.Context, w ndr.Reader) error {
	*r = batchReadResponse{count: r.count}
	response := &syncio.ReadResponse{}
	if err := response.UnmarshalNDR(ctx, bindingsReader{Reader: w, count: r.count}); err != nil {
		return err
	}
	if response.ItemValues != nil {
		r.states = make([]readOneResponse, len(response.ItemValues))
		for i, state := range response.ItemValues {
			if state == nil || state.Timestamp == nil {
				return fmt.Errorf("Read: missing item state or timestamp")
			}
			r.states[i] = readOneResponse{
				client:   state.Client,
				timeLow:  state.Timestamp.LowDateTime,
				timeHigh: state.Timestamp.HighDateTime,
				quality:  state.Quality,
				variant:  state.DataValue,
			}
		}
	}
	r.errors, r.hresult = response.Errors, response.Return
	return nil
}

func (r *batchReadResponse) results(ids []string) ([]ReadResult, error) {
	if r.hresult < 0 {
		return nil, hresultError("Read", "", r.hresult)
	}
	if len(r.errors) != len(ids) || len(r.states) != len(ids) {
		return nil, fmt.Errorf("Read: missing result arrays")
	}
	out := make([]ReadResult, len(ids))
	for i, id := range ids {
		s := &r.states[i]
		s.hasState = true
		s.hasError = true
		s.itemError = r.errors[i]
		value, err := s.result(id)
		if err != nil {
			out[i] = ReadResult{ItemID: id, Error: err}
		} else {
			out[i] = *value
		}
	}
	return out, nil
}

type batchWriteRequest struct {
	handles []uint32
	values  []*oaut.Variant
}

func (r *batchWriteRequest) MarshalNDR(ctx context.Context, w ndr.Writer) error {
	if len(r.handles) != len(r.values) {
		return fmt.Errorf("Write: mismatched handle and value counts")
	}
	return (&syncio.WriteRequest{This: orpcThis(), Server: r.handles, ItemValues: r.values}).MarshalNDR(
		ctx,
		bindingsWriter{w},
	)
}

type batchWriteResponse struct {
	count   int
	errors  []int32
	hresult int32
}

func (r *batchWriteResponse) UnmarshalNDR(ctx context.Context, w ndr.Reader) error {
	*r = batchWriteResponse{count: r.count}
	response := &syncio.WriteResponse{}
	if err := response.UnmarshalNDR(ctx, bindingsReader{Reader: w, count: r.count}); err != nil {
		return err
	}
	r.errors, r.hresult = response.Errors, response.Return
	return nil
}

type groupStateRequest struct {
	rate     uint32
	active   bool
	deadband float32
}

// The generated SetStateRequest represents optional pointers as values and
// always sends them. Preserve null (no change) for time bias, locale and client
// handle until the binding exposes pointer presence.
func (r *groupStateRequest) MarshalNDR(ctx context.Context, w ndr.Writer) error {
	if err := writeORPC(ctx, w); err != nil {
		return err
	}
	active := uint32(0)
	if r.active {
		active = 1
	}
	for _, value := range []any{r.rate, active, nil, r.deadband, nil, nil} {
		if value == nil {
			if err := w.WritePointer(nil); err != nil {
				return err
			}
		} else {
			body := ndr.MarshalNDRFunc(
				func(ctx context.Context, w ndr.Writer) error { return w.WriteData(value) },
			)
			if err := w.WritePointer(&value, body); err != nil {
				return err
			}
		}
		if err := w.WriteDeferred(); err != nil {
			return err
		}
	}
	return nil
}

type groupStateResponse struct {
	rate    uint32
	hresult int32
}

func (r *groupStateResponse) UnmarshalNDR(ctx context.Context, w ndr.Reader) error {
	response := &statemgt.SetStateResponse{}
	if err := response.UnmarshalNDR(ctx, w); err != nil {
		return err
	}
	r.rate, r.hresult = response.RevisedUpdateRate, response.Return
	return nil
}
