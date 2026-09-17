package opcda

import (
	"context"
	"fmt"
	"math"
	"unicode/utf16"

	"github.com/oiweiwei/go-msrpc/msrpc/dcom/oaut"
	"github.com/oiweiwei/go-msrpc/ndr"
)

// marshalWriteVariant preserves empty BSTRs and counts UTF-16 code units.
// The generated String marshaler treats an empty string as a null BSTR.
func marshalWriteVariant(ctx context.Context, w ndr.Writer, v *oaut.Variant) error {
	if v.VT != 8 {
		return v.MarshalNDR(ctx, w)
	}
	units := utf16.Encode([]rune(v.VarUnion.GetValue().(*oaut.String).Data))
	if err := w.WriteAlign(8); err != nil {
		return err
	}
	for _, value := range []any{v.Size, uint32(0), uint16(8), uint16(0), uint16(0), uint16(0), uint32(8)} {
		if err := w.WriteData(value); err != nil {
			return err
		}
	}
	body := ndr.MarshalNDRFunc(func(ctx context.Context, w ndr.Writer) error {
		if err := w.WriteSize(uint64(len(units))); err != nil {
			return err
		}
		if err := w.WriteData(uint32(len(units) * 2)); err != nil {
			return err
		}
		if err := w.WriteData(uint32(len(units))); err != nil {
			return err
		}
		for _, unit := range units {
			if err := w.WriteData(unit); err != nil {
				return err
			}
		}
		return nil
	})
	if err := w.WritePointer(v, body); err != nil {
		return err
	}
	return w.WriteDeferred()
}

// unmarshalReadVariant keeps BSTRs length-counted, including trailing NULs.
func unmarshalReadVariant(ctx context.Context, r ndr.Reader, v *oaut.Variant) error {
	*v = oaut.Variant{VarUnion: &oaut.Variant_VarUnion{}}
	if err := r.ReadAlign(8); err != nil {
		return err
	}
	var reserved uint32
	var reserved16 uint16
	for _, field := range []any{&v.Size, &reserved, &v.VT, &reserved16, &reserved16, &reserved16} {
		if err := r.ReadData(field); err != nil {
			return err
		}
	}
	if v.VT != 8 {
		return v.VarUnion.UnmarshalUnionNDR(ctx, r, uint32(v.VT))
	}
	var tag uint32
	if err := r.ReadData(&tag); err != nil {
		return err
	}
	if tag != 8 {
		return fmt.Errorf("invalid BSTR union tag %d", tag)
	}
	value := &oaut.Variant_VarUnion_BSTR{}
	v.VarUnion.Value = value
	body := ndr.UnmarshalNDRFunc(func(ctx context.Context, r ndr.Reader) error {
		var count uint64
		var bytes, size uint32
		if err := r.ReadSize(&count); err != nil {
			return err
		}
		if err := r.ReadData(&bytes); err != nil {
			return err
		}
		if err := r.ReadData(&size); err != nil {
			return err
		}
		if count != uint64(size) || count > uint64(r.Len()/2) ||
			(bytes != size*2 && (bytes != math.MaxUint32 || size != 0)) ||
			uint64(size)*2 > math.MaxUint32 {
			return fmt.Errorf("invalid BSTR length")
		}
		units := make([]uint16, int(count))
		for i := range units {
			if err := r.ReadData(&units[i]); err != nil {
				return err
			}
		}
		value.BSTR = &oaut.String{Data: string(utf16.Decode(units)), Size: size, BytesCount: bytes}
		return nil
	})
	if err := r.ReadPointer(
		&value.BSTR,
		func(v any) { value.BSTR = *v.(**oaut.String) },
		body,
	); err != nil {
		return err
	}
	return r.ReadDeferred()
}
