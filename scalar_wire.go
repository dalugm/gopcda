package opcda

import (
	"context"
	"errors"
	"fmt"
	"math"
	"unicode/utf16"

	"github.com/oiweiwei/go-msrpc/msrpc/dcom/oaut"
	"github.com/oiweiwei/go-msrpc/ndr"
)

type variantDepthKey struct{}

func variantContext(ctx context.Context) (context.Context, error) {
	depth, _ := ctx.Value(variantDepthKey{}).(int)
	if depth >= 32 {
		return nil, errors.New("VARIANT nesting exceeds 32")
	}
	ctx = context.WithValue(ctx, variantDepthKey{}, depth+1)
	// A containing SAFEARRAY's conformant dimensions do not apply to its elements.
	return context.WithValue(ctx, ndr.SizeInfo, struct{}{}), nil
}

func variantTag(vt uint16) uint32 {
	if vt&VTArray != 0 {
		return uint32(vt & (VTArray | VTByRef))
	}
	return uint32(vt)
}

// marshalWriteVariant preserves bounded nesting and BYREF scalar layout.
// BSTR payloads use the upstream encoder with explicit empty-string semantics.
// The in-memory union uses base arms for BYREF scalar values.
func marshalWriteVariant(ctx context.Context, w ndr.Writer, v *oaut.Variant) error {
	var err error
	ctx, err = variantContext(ctx)
	if err != nil {
		return err
	}
	if v == nil {
		return errors.New("missing VARIANT")
	}
	if v.VarUnion == nil {
		if v.VT != VTEmpty && v.VT != VTNull {
			return errors.New("missing VARIANT payload")
		}
		normalized := *v
		normalized.VarUnion = &oaut.Variant_VarUnion{}
		v = &normalized
	}
	if err := w.WriteAlign(8); err != nil {
		return err
	}
	for _, value := range []any{v.Size, uint32(0), v.VT, uint16(0), uint16(0), uint16(0)} {
		if err := w.WriteData(value); err != nil {
			return err
		}
	}
	if err := w.WriteUnionAlign(8); err != nil {
		return err
	}
	if err := w.WriteSwitch(variantTag(v.VT)); err != nil {
		return err
	}
	if err := w.WriteUnionAlign(8); err != nil {
		return err
	}
	base := v.VT &^ VTByRef
	body := ndr.MarshalNDRFunc(func(ctx context.Context, w ndr.Writer) error {
		switch base {
		case VTEmpty, VTNull:
			return nil
		case VTBSTR:
			value, _ := v.VarUnion.GetValue().(*oaut.String)
			if value == nil {
				return w.WritePointer(nil)
			}
			return w.WritePointer(
				value,
				ndr.MarshalNDRFunc(
					func(ctx context.Context, w ndr.Writer) error { return writeBSTR(ctx, w, value.Data) },
				),
			)
		case VTVariant:
			value, ok := v.VarUnion.GetValue().(*oaut.Variant)
			if !ok || value == nil {
				return errors.New("missing BYREF VARIANT")
			}
			return w.WritePointer(
				value,
				ndr.MarshalNDRFunc(
					func(_ context.Context, w ndr.Writer) error { return marshalWriteVariant(ctx, w, value) },
				),
			)
		default:
			if v.VarUnion.Value == nil {
				return errors.New("missing VARIANT payload")
			}
			return v.VarUnion.Value.MarshalNDR(ctx, valueWriter{Writer: w, ctx: ctx})
		}
	})
	// Every BYREF arm has an outer unique pointer, including numeric scalars.
	if v.VT&VTByRef != 0 {
		err = w.WritePointer(
			v,
			ndr.MarshalNDRFunc(
				func(_ context.Context, w ndr.Writer) error { return body.MarshalNDR(ctx, w) },
			),
		)
	} else {
		err = body.MarshalNDR(ctx, w)
	}
	if err != nil {
		return err
	}
	return w.WriteDeferred()
}

func unmarshalReadVariant(ctx context.Context, r ndr.Reader, v *oaut.Variant) error {
	var err error
	ctx, err = variantContext(ctx)
	if err != nil {
		return err
	}
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
	if err := r.ReadUnionAlign(8); err != nil {
		return err
	}
	var tag uint32
	if err := r.ReadSwitch(&tag); err != nil {
		return err
	}
	if tag != variantTag(v.VT) {
		return fmt.Errorf("VARIANT type 0x%04x disagrees with union tag 0x%x", v.VT, tag)
	}
	if err := r.ReadUnionAlign(8); err != nil {
		return err
	}
	// Consume known COM payloads so unsupported values fail per item, after the
	// response's HRESULT array is available. This does not expose or invoke them.
	switch v.VT {
	case VTDispatch:
		v.VarUnion.Value = &oaut.Variant_VarUnion_IDispatch{}
	case VTUnknown:
		v.VarUnion.Value = &oaut.Variant_VarUnion_IUnknown{}
	case VTRecord, VTRecord | VTByRef:
		v.VarUnion.Value = &oaut.Variant_VarUnion_Brecord{}
	case VTDispatch | VTByRef:
		v.VarUnion.Value = &oaut.Variant_VarUnion_IDispatchPtr{}
	case VTUnknown | VTByRef:
		v.VarUnion.Value = &oaut.Variant_VarUnion_IUnknownPtr{}
	}
	if v.VarUnion.Value != nil {
		if err := v.VarUnion.Value.UnmarshalNDR(ctx, valueReader{Reader: r, ctx: ctx}); err != nil {
			return err
		}
		return r.ReadDeferred()
	}
	base := v.VT &^ VTByRef
	body := ndr.UnmarshalNDRFunc(func(ctx context.Context, r ndr.Reader) error {
		switch base {
		case VTEmpty, VTNull:
			if v.VT&VTByRef != 0 {
				return errors.New("EMPTY/NULL cannot be BYREF")
			}
			return nil
		case VTI2:
			v.VarUnion.Value = &oaut.Variant_VarUnion_Short{}
		case VTI4:
			v.VarUnion.Value = &oaut.Variant_VarUnion_Long{}
		case VTR4:
			v.VarUnion.Value = &oaut.Variant_VarUnion_Float{}
		case VTR8:
			v.VarUnion.Value = &oaut.Variant_VarUnion_Double{}
		case VTCY:
			v.VarUnion.Value = &oaut.Variant_VarUnion_Currency{}
		case VTDate:
			v.VarUnion.Value = &oaut.Variant_VarUnion_Date{}
		case VTBSTR:
			value := &oaut.Variant_VarUnion_BSTR{}
			v.VarUnion.Value = value
			return readBSTRPointer(r, &value.BSTR, func(p any) { value.BSTR = *p.(**oaut.String) })
		case VTError:
			v.VarUnion.Value = &oaut.Variant_VarUnion_HResult{}
		case VTBool:
			v.VarUnion.Value = &oaut.Variant_VarUnion_Bool{}
		case VTVariant:
			if v.VT&VTByRef == 0 {
				return errors.New("VT_VARIANT requires BYREF")
			}
			value := &oaut.Variant_VarUnion_VariantPtr{}
			v.VarUnion.Value = value
			return r.ReadPointer(
				&value.VariantPtr,
				func(p any) { value.VariantPtr = *p.(**oaut.Variant) },
				ndr.UnmarshalNDRFunc(func(_ context.Context, r ndr.Reader) error {
					value.VariantPtr = &oaut.Variant{}
					return unmarshalReadVariant(ctx, r, value.VariantPtr)
				}),
			)
		case VTDecimal:
			v.VarUnion.Value = &oaut.Variant_VarUnion_Decimal{}
		case VTI1:
			v.VarUnion.Value = &oaut.Variant_VarUnion_Char{}
		case VTUI1:
			v.VarUnion.Value = &oaut.Variant_VarUnion_Byte{}
		case VTUI2:
			v.VarUnion.Value = &oaut.Variant_VarUnion_Ushort{}
		case VTUI4:
			v.VarUnion.Value = &oaut.Variant_VarUnion_Ulong{}
		case VTI8:
			v.VarUnion.Value = &oaut.Variant_VarUnion_LongLongValue{}
		case VTUI8:
			v.VarUnion.Value = &oaut.Variant_VarUnion_UlongLong{}
		case VTInt:
			v.VarUnion.Value = &oaut.Variant_VarUnion_Int{}
		case VTUint:
			v.VarUnion.Value = &oaut.Variant_VarUnion_Uint{}
		default:
			if base&VTArray == 0 {
				return fmt.Errorf("unsupported VARIANT type 0x%04x", v.VT)
			}
			elementType := base &^ VTArray
			if elementType != VTEmpty && elementType != VTDispatch && elementType != VTUnknown &&
				elementType != VTRecord {
				if _, _, _, err := arrayLayout(elementType); err != nil {
					return err
				}
			}
			v.VarUnion.Value = &oaut.Variant_VarUnion_SafeArray{}
		}
		return v.VarUnion.Value.UnmarshalNDR(ctx, valueReader{Reader: r, ctx: ctx})
	})
	if v.VT&VTByRef != 0 {
		var present *byte
		err = r.ReadPointer(
			&present,
			func(p any) { present = *p.(**byte) },
			ndr.UnmarshalNDRFunc(
				func(_ context.Context, r ndr.Reader) error { present = new(byte); return body.UnmarshalNDR(ctx, r) },
			),
		)
		if err == nil {
			err = r.ReadDeferred()
		}
		if err == nil && present == nil {
			return errors.New("null BYREF pointer")
		}
	} else {
		err = body.UnmarshalNDR(ctx, r)
	}
	if err != nil {
		return err
	}
	return r.ReadDeferred()
}

func writeBSTR(ctx context.Context, w ndr.Writer, text string) error {
	value := &oaut.String{Data: text, IsEmpty: text == ""}
	// An enclosing SAFEARRAY's conformant count is not this BSTR's length.
	ctx = context.WithValue(ctx, ndr.SizeInfo, struct{}{})
	return value.MarshalNDR(ctx, w)
}

func readBSTRPointer(r ndr.Reader, value **oaut.String, setter func(any)) error {
	return r.ReadPointer(
		value,
		setter,
		ndr.UnmarshalNDRFunc(func(_ context.Context, r ndr.Reader) error {
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
				return errors.New("invalid BSTR length")
			}
			units := make([]uint16, int(count))
			for i := range units {
				if err := r.ReadData(&units[i]); err != nil {
					return err
				}
			}
			*value = &oaut.String{
				Data:       string(utf16.Decode(units)),
				Size:       size,
				BytesCount: bytes,
				IsEmpty:    size == 0 && bytes == 0,
			}
			return nil
		}),
	)
}
