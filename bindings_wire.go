package opcda

import (
	"context"
	"errors"
	"fmt"

	"github.com/oiweiwei/go-msrpc/msrpc/dcom/oaut"
	"github.com/oiweiwei/go-msrpc/ndr"
	binding "github.com/oiweiwei/go-opcda/opc/opcda"
)

// bindingsWriter preserves gopcda's value contracts in deferred VARIANTs
// while generated OPC bindings own operation and array layouts.
// NDR strings use the upstream codec.
type bindingsWriter struct{ ndr.Writer }

func (w bindingsWriter) WritePointer(ptr ndr.Pointer, bodies ...ndr.Marshaler) error {
	if value, ok := ptr.(**oaut.Variant); ok {
		return w.Writer.WritePointer(
			ptr,
			ndr.MarshalNDRFunc(func(ctx context.Context, target ndr.Writer) error {
				return marshalWriteVariant(ctx, target, *value)
			}),
		)
	}
	wrapped := make([]ndr.Marshaler, len(bodies))
	for i, body := range bodies {
		wrapped[i] = ndr.MarshalNDRFunc(func(ctx context.Context, target ndr.Writer) error {
			return body.MarshalNDR(ctx, bindingsWriter{target})
		})
	}
	return w.Writer.WritePointer(ptr, wrapped...)
}

// bindingsReader preserves BSTR contents and bounds generated result arrays
// by the requested batch size before allocation. It also wraps deferred
// callbacks, since the NDR runtime supplies its underlying reader to them.
type bindingsReader struct {
	ndr.Reader
	count int
}

func (r bindingsReader) ReadPointer(
	ptr ndr.Pointer,
	setter func(any),
	bodies ...ndr.Unmarshaler,
) error {
	if value, ok := ptr.(**oaut.Variant); ok {
		return r.Reader.ReadPointer(
			ptr,
			setter,
			ndr.UnmarshalNDRFunc(func(ctx context.Context, target ndr.Reader) error {
				*value = &oaut.Variant{}
				return unmarshalReadVariant(ctx, target, *value)
			}),
		)
	}
	array := false
	switch ptr.(type) {
	case *[]*binding.ItemState, *[]*binding.ItemResult, *[]int32:
		array = true
	}
	wrapped := make([]ndr.Unmarshaler, len(bodies))
	for i, body := range bodies {
		wrapped[i] = ndr.UnmarshalNDRFunc(func(ctx context.Context, target ndr.Reader) error {
			reader := bindingsReader{Reader: target, count: r.count}
			if blob, ok := ptr.(*[]byte); ok {
				wire := &bindingBlobReader{Reader: target}
				if err := body.UnmarshalNDR(ctx, wire); err != nil {
					return err
				}
				// The generated decoder substitutes BlobSize for a zero wire
				// count. Reject that fallback rather than accepting malformed data.
				if uint64(len(*blob)) != wire.count {
					return errors.New("invalid item blob length")
				}
				return nil
			}
			if array {
				return body.UnmarshalNDR(
					ctx,
					&bindingArrayReader{bindingsReader: reader, first: true},
				)
			}
			return body.UnmarshalNDR(ctx, reader)
		})
	}
	return r.Reader.ReadPointer(ptr, setter, wrapped...)
}

type bindingArrayReader struct {
	bindingsReader
	first bool
}

func (r *bindingArrayReader) ReadSize(size *uint64) error {
	if err := r.Reader.ReadSize(size); err != nil {
		return err
	}
	if r.first {
		r.first = false
		if r.count < 1 || r.count > maxBatchItems || *size != uint64(r.count) {
			return fmt.Errorf("batch count %d, expected %d", *size, r.count)
		}
	}
	return nil
}

// Blob referents have a single conformant count and no nested arrays.
type bindingBlobReader struct {
	ndr.Reader
	count uint64
}

func (r *bindingBlobReader) ReadSize(size *uint64) error {
	if err := r.Reader.ReadSize(size); err != nil {
		return err
	}
	r.count = *size
	return nil
}
