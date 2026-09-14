package opcda

import (
	"context"
	"fmt"

	"github.com/oiweiwei/go-msrpc/msrpc/dcom/oaut"
	"github.com/oiweiwei/go-msrpc/ndr"
	binding "github.com/oiweiwei/go-opcda/opc/opcda"
)

// bindingsWriter preserves gopcda's length-counted BSTR and UTF-16 string
// contracts while generated OPC bindings own operation and array layouts.
// go-msrpc v1.5.4 treats an empty BSTR as null and counts UTF-8 bytes for
// NDR strings. Keep these overrides local until upstream supports them.
type bindingsWriter struct{ ndr.Writer }

func (w bindingsWriter) WritePointer(ptr ndr.Pointer, bodies ...ndr.Marshaler) error {
	switch value := ptr.(type) {
	case **oaut.Variant:
		return w.Writer.WritePointer(
			ptr,
			ndr.MarshalNDRFunc(func(ctx context.Context, target ndr.Writer) error {
				return marshalWriteVariant(ctx, target, *value)
			}),
		)
	case *string:
		return w.Writer.WritePointer(
			ptr,
			ndr.MarshalNDRFunc(func(_ context.Context, target ndr.Writer) error {
				text := *value
				// A single terminator requests an empty, non-null access path from
				// the generated ItemDefinition marshaler.
				if text == "\x00" {
					text = ""
				}
				return writeUTF16String(target, text)
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
					return fmt.Errorf("invalid item blob length")
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
