package opcda

import (
	"context"
	"fmt"

	"github.com/oiweiwei/go-msrpc/msrpc/dcom/oaut"
	"github.com/oiweiwei/go-msrpc/ndr"
)

const maxBatchItems = 1000

func readArray[T any](
	ctx context.Context,
	w ndr.Reader,
	count int,
	target *[]T,
	element func(context.Context, ndr.Reader, *T) error,
) error {
	if count < 1 || count > maxBatchItems {
		return fmt.Errorf("invalid expected batch count %d", count)
	}
	body := ndr.UnmarshalNDRFunc(func(ctx context.Context, w ndr.Reader) error {
		var n uint64
		if err := w.ReadSize(&n); err != nil {
			return err
		}
		if n != uint64(count) {
			return fmt.Errorf("batch count %d, expected %d", n, count)
		}
		*target = make([]T, count)
		for i := range *target {
			if err := element(ctx, w, &(*target)[i]); err != nil {
				return err
			}
		}
		return nil
	})
	if err := w.ReadPointer(target, func(v any) { *target = *v.(*[]T) }, body); err != nil {
		return err
	}
	return w.ReadDeferred()
}

func readErrors(ctx context.Context, w ndr.Reader, count int, target *[]int32) error {
	return readArray(
		ctx,
		w,
		count,
		target,
		func(ctx context.Context, w ndr.Reader, v *int32) error { return w.ReadData(v) },
	)
}

type batchAddRequest struct {
	ids     []string
	clients []uint32
}

func (r *batchAddRequest) MarshalNDR(ctx context.Context, w ndr.Writer) error {
	if err := writeORPC(ctx, w); err != nil {
		return err
	}
	if err := w.WriteData(uint32(len(r.ids))); err != nil {
		return err
	}
	if err := w.WriteSize(uint64(len(r.ids))); err != nil {
		return err
	}
	for i, id := range r.ids {
		for _, value := range []string{"", id} {
			text := value
			body := ndr.MarshalNDRFunc(
				func(ctx context.Context, w ndr.Writer) error { return ndr.WriteUTF16NString(ctx, w, text) },
			)
			if err := w.WritePointer(&text, body); err != nil {
				return err
			}
		}
		for _, v := range []any{uint32(1), r.clients[i], uint32(0), uint32(0), uint16(0), uint16(0)} {
			if err := w.WriteData(v); err != nil {
				return err
			}
		}
	}
	return w.WriteDeferred()
}

type batchAddResponse struct {
	count   int
	items   []addOneResponse
	errors  []int32
	hresult int32
}

func (r *batchAddResponse) UnmarshalNDR(ctx context.Context, w ndr.Reader) error {
	r.items = nil
	r.errors = nil
	if err := readORPC(ctx, w); err != nil {
		return err
	}
	if err := readArray(
		ctx,
		w,
		r.count,
		&r.items,
		func(ctx context.Context, w ndr.Reader, v *addOneResponse) error { return v.readInline(ctx, w) },
	); err != nil {
		return err
	}
	if err := readErrors(ctx, w, r.count, &r.errors); err != nil {
		return err
	}
	return w.ReadData(&r.hresult)
}

type batchReadRequest struct {
	handles []uint32
	cache   bool
}

func (r *batchReadRequest) MarshalNDR(ctx context.Context, w ndr.Writer) error {
	if err := writeORPC(ctx, w); err != nil {
		return err
	}
	source := uint16(2)
	if r.cache {
		source = 1
	}
	if err := w.WriteData(source); err != nil {
		return err
	}
	if err := w.WriteData(uint32(len(r.handles))); err != nil {
		return err
	}
	if err := w.WriteSize(uint64(len(r.handles))); err != nil {
		return err
	}
	for _, h := range r.handles {
		if err := w.WriteData(h); err != nil {
			return err
		}
	}
	return nil
}

type batchReadResponse struct {
	count   int
	states  []readOneResponse
	errors  []int32
	hresult int32
}

func (r *batchReadResponse) UnmarshalNDR(ctx context.Context, w ndr.Reader) error {
	r.states = nil
	r.errors = nil
	if err := readORPC(ctx, w); err != nil {
		return err
	}
	if err := readArray(
		ctx,
		w,
		r.count,
		&r.states,
		func(ctx context.Context, w ndr.Reader, v *readOneResponse) error { return v.readInline(ctx, w) },
	); err != nil {
		return err
	}
	if err := readErrors(ctx, w, r.count, &r.errors); err != nil {
		return err
	}
	return w.ReadData(&r.hresult)
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
	if err := writeORPC(ctx, w); err != nil {
		return err
	}
	if err := w.WriteData(uint32(len(r.handles))); err != nil {
		return err
	}
	if err := w.WriteSize(uint64(len(r.handles))); err != nil {
		return err
	}
	for _, h := range r.handles {
		if err := w.WriteData(h); err != nil {
			return err
		}
	}
	if err := w.WriteSize(uint64(len(r.values))); err != nil {
		return err
	}
	for _, value := range r.values {
		body := ndr.MarshalNDRFunc(
			func(ctx context.Context, w ndr.Writer) error { return marshalWriteVariant(ctx, w, value) },
		)
		if err := w.WritePointer(value, body); err != nil {
			return err
		}
	}
	return w.WriteDeferred()
}

type batchWriteResponse struct {
	count   int
	errors  []int32
	hresult int32
}

func (r *batchWriteResponse) UnmarshalNDR(ctx context.Context, w ndr.Reader) error {
	r.errors = nil
	if err := readORPC(ctx, w); err != nil {
		return err
	}
	if err := readErrors(ctx, w, r.count, &r.errors); err != nil {
		return err
	}
	return w.ReadData(&r.hresult)
}

type groupStateRequest struct {
	rate     uint32
	active   bool
	deadband float32
}

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
	if err := readORPC(ctx, w); err != nil {
		return err
	}
	if err := w.ReadData(&r.rate); err != nil {
		return err
	}
	return w.ReadData(&r.hresult)
}

func (r *addOneResponse) readInline(ctx context.Context, w ndr.Reader) error {
	var reserved uint16
	var blobSize uint32
	for _, v := range []any{&r.handle, &r.canonical, &reserved, &r.rights, &blobSize} {
		if err := w.ReadData(v); err != nil {
			return err
		}
	}
	var blob []byte
	f := ndr.UnmarshalNDRFunc(func(ctx context.Context, w ndr.Reader) error {
		var count uint64
		if err := w.ReadSize(&count); err != nil {
			return err
		}
		if count != uint64(blobSize) || count > uint64(w.Len()) {
			return fmt.Errorf("invalid item blob length")
		}
		blob = make([]byte, int(count))
		for i := range blob {
			if err := w.ReadData(&blob[i]); err != nil {
				return err
			}
		}
		return nil
	})
	return w.ReadPointer(&blob, func(v any) { blob = *v.(*[]byte) }, f)
}

func (r *readOneResponse) readInline(ctx context.Context, w ndr.Reader) error {
	var reserved uint16
	for _, v := range []any{&r.client, &r.timeLow, &r.timeHigh, &r.quality, &reserved} {
		if err := w.ReadData(v); err != nil {
			return err
		}
	}
	variant := ndr.UnmarshalNDRFunc(func(ctx context.Context, w ndr.Reader) error {
		r.variant = &oaut.Variant{}
		return unmarshalReadVariant(ctx, w, r.variant)
	})
	return w.ReadPointer(&r.variant, func(v any) { r.variant = *v.(**oaut.Variant) }, variant)
}
