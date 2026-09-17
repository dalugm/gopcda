package opcda

import (
	"context"
	"errors"

	"github.com/oiweiwei/go-msrpc/msrpc/dcom/oaut"
	"github.com/oiweiwei/go-msrpc/ndr"
)

// These wrappers keep corrections active in generated SAFEARRAY deferred bodies.
type valueWriter struct {
	ndr.Writer
	ctx context.Context
}

func (w valueWriter) WritePointer(ptr ndr.Pointer, bodies ...ndr.Marshaler) error {
	switch p := ptr.(type) {
	case **oaut.String:
		if *p == nil {
			return w.Writer.WritePointer(nil)
		}
		return w.Writer.WritePointer(
			ptr,
			ndr.MarshalNDRFunc(
				func(ctx context.Context, w ndr.Writer) error { return writeBSTR(ctx, w, (*p).Data) },
			),
		)
	case **oaut.Variant:
		return w.Writer.WritePointer(
			ptr,
			ndr.MarshalNDRFunc(
				func(_ context.Context, target ndr.Writer) error { return marshalWriteVariant(w.ctx, target, *p) },
			),
		)
	}
	wrapped := make([]ndr.Marshaler, len(bodies))
	for i, body := range bodies {
		wrapped[i] = ndr.MarshalNDRFunc(func(_ context.Context, target ndr.Writer) error {
			return body.MarshalNDR(w.ctx, valueWriter{Writer: target, ctx: w.ctx})
		})
	}
	return w.Writer.WritePointer(ptr, wrapped...)
}

type valueReader struct {
	ndr.Reader
	ctx context.Context
}

func (r valueReader) ReadSize(n *uint64) error {
	if err := r.Reader.ReadSize(n); err != nil {
		return err
	}
	if *n > 1<<20 {
		return errors.New("automation array count exceeds 1048576")
	}
	return nil
}

func (r valueReader) ReadPointer(
	ptr ndr.Pointer,
	setter func(any),
	bodies ...ndr.Unmarshaler,
) error {
	switch p := ptr.(type) {
	case **oaut.String:
		return readBSTRPointer(r.Reader, p, setter)
	case **oaut.Variant:
		return r.Reader.ReadPointer(
			ptr,
			setter,
			ndr.UnmarshalNDRFunc(func(_ context.Context, target ndr.Reader) error {
				*p = &oaut.Variant{}
				return unmarshalReadVariant(r.ctx, target, *p)
			}),
		)
	}
	wrapped := make([]ndr.Unmarshaler, len(bodies))
	for i, body := range bodies {
		wrapped[i] = ndr.UnmarshalNDRFunc(func(_ context.Context, target ndr.Reader) error {
			reader := valueReader{Reader: target, ctx: r.ctx}
			if _, ok := valueArrayLength(ptr); ok {
				counted := &valueCountReader{valueReader: reader}
				if err := body.UnmarshalNDR(r.ctx, counted); err != nil {
					return err
				}
				// SAFEARRAY has an extra pointer layer without a conformant count.
				// The inner deferred body validates the bounds count.
				if _, ok := ptr.(**oaut.SafeArray); ok && !counted.seen {
					return nil
				}
				size, _ := valueArrayLength(ptr)
				if !counted.seen || uint64(size) != counted.count {
					return errors.New("SAFEARRAY conformant count mismatch")
				}
				return nil
			}
			return body.UnmarshalNDR(r.ctx, reader)
		})
	}
	return r.Reader.ReadPointer(ptr, setter, wrapped...)
}

func valueArrayLength(ptr any) (int, bool) {
	switch p := ptr.(type) {
	case **oaut.SafeArray:
		if *p == nil {
			return 0, true
		}
		return len((*p).Bound), true
	case *[]byte:
		return len(*p), true
	case *[]uint16:
		return len(*p), true
	case *[]uint32:
		return len(*p), true
	case *[]int64:
		return len(*p), true
	case *[]*oaut.String:
		return len(*p), true
	case *[]*oaut.Variant:
		return len(*p), true
	default:
		return 0, false
	}
}

type valueCountReader struct {
	valueReader
	count uint64
	seen  bool
}

func (r *valueCountReader) ReadSize(n *uint64) error {
	if err := r.valueReader.ReadSize(n); err != nil {
		return err
	}
	if !r.seen {
		r.count = *n
		r.seen = true
	}
	return nil
}
