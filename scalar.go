package opcda

import (
	"context"
	"errors"
	"math"
	"unicode/utf16"
	"unicode/utf8"

	"github.com/oiweiwei/go-msrpc/msrpc/dcom/oaut"
	"github.com/oiweiwei/go-msrpc/ndr"
)

func writeVariant(value any) (*oaut.Variant, error) {
	return writeVariantDepth(value, 0)
}

func writeVariantDepth(value any, depth int) (*oaut.Variant, error) {
	if depth > 32 {
		return nil, errors.New("VARIANT nesting exceeds 32")
	}
	v := &oaut.Variant{Size: 3, VarUnion: &oaut.Variant_VarUnion{}}
	switch x := value.(type) {
	case bool:
		var b int16
		if x {
			b = -1
		}
		v.VT = VTBool
		v.VarUnion.Value = &oaut.Variant_VarUnion_Bool{Bool: b}
	case int8:
		v.VT = VTI1
		v.VarUnion.Value = &oaut.Variant_VarUnion_Char{Char: uint8(x)}
	case uint8:
		v.VT = VTUI1
		v.VarUnion.Value = &oaut.Variant_VarUnion_Byte{Byte: x}
	case int16:
		v.VT = VTI2
		v.VarUnion.Value = &oaut.Variant_VarUnion_Short{Short: x}
	case uint16:
		v.VT = VTUI2
		v.VarUnion.Value = &oaut.Variant_VarUnion_Ushort{Ushort: x}
	case int32:
		v.VT = VTI4
		v.VarUnion.Value = &oaut.Variant_VarUnion_Long{Long: x}
	case uint32:
		v.VT = VTUI4
		v.VarUnion.Value = &oaut.Variant_VarUnion_Ulong{Ulong: x}
	case int64:
		v.Size = 4
		v.VT = VTI8
		v.VarUnion.Value = &oaut.Variant_VarUnion_LongLongValue{LongLongValue: x}
	case uint64:
		v.Size = 4
		v.VT = VTUI8
		v.VarUnion.Value = &oaut.Variant_VarUnion_UlongLong{UlongLong: x}
	case float32:
		if math.IsNaN(float64(x)) || math.IsInf(float64(x), 0) {
			return nil, errors.New("write value must be finite")
		}
		v.VT = VTR4
		v.VarUnion.Value = &oaut.Variant_VarUnion_Float{Float: x}
	case float64:
		if math.IsNaN(x) || math.IsInf(x, 0) {
			return nil, errors.New("write value must be finite")
		}
		v.Size = 4
		v.VT = VTR8
		v.VarUnion.Value = &oaut.Variant_VarUnion_Double{Double: x}
	case string:
		if !utf8.ValidString(x) {
			return nil, errors.New("write string must be valid UTF-8")
		}
		if uint64(len(x)) > math.MaxUint32/2 {
			return nil, errors.New("write string is too large")
		}
		v.VT = VTBSTR
		v.VarUnion.Value = &oaut.Variant_VarUnion_BSTR{
			BSTR: &oaut.String{
				Data:    x,
				Size:    uint32(len(utf16.Encode([]rune(x)))),
				IsEmpty: x == "",
			},
		}
		v.Size = uint32((36 + 2*len(utf16.Encode([]rune(x))) + 7) / 8)
	default:
		var err error
		v, err = writeExtendedValue(value, depth)
		if err != nil {
			return nil, err
		}
		if v.VT&(VTArray|VTByRef) != 0 {
			wire, err := ndr.Marshal(
				ndr.MarshalNDRFunc(
					func(ctx context.Context, w ndr.Writer) error { return marshalWriteVariant(ctx, w, v) },
				),
			)
			if err != nil {
				return nil, err
			}
			v.Size = uint32((len(wire) + 7) / 8)
		}
	}
	return v, nil
}
