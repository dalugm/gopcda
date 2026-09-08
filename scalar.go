package opcda

import (
	"fmt"
	"math"
	"unicode/utf16"
	"unicode/utf8"

	"github.com/oiweiwei/go-msrpc/msrpc/dcom/oaut"
)

func writeVariant(value any) (*oaut.Variant, error) {
	v := &oaut.Variant{Size: 3, VarUnion: &oaut.Variant_VarUnion{}}
	switch x := value.(type) {
	case bool:
		var b int16
		if x {
			b = -1
		}
		v.VT = 11
		v.VarUnion.Value = &oaut.Variant_VarUnion_Bool{Bool: b}
	case int8:
		v.VT = 16
		v.VarUnion.Value = &oaut.Variant_VarUnion_Char{Char: uint8(x)}
	case uint8:
		v.VT = 17
		v.VarUnion.Value = &oaut.Variant_VarUnion_Byte{Byte: x}
	case int16:
		v.VT = 2
		v.VarUnion.Value = &oaut.Variant_VarUnion_Short{Short: x}
	case uint16:
		v.VT = 18
		v.VarUnion.Value = &oaut.Variant_VarUnion_Ushort{Ushort: x}
	case int32:
		v.VT = 3
		v.VarUnion.Value = &oaut.Variant_VarUnion_Long{Long: x}
	case uint32:
		v.VT = 19
		v.VarUnion.Value = &oaut.Variant_VarUnion_Ulong{Ulong: x}
	case int64:
		v.Size = 4
		v.VT = 20
		v.VarUnion.Value = &oaut.Variant_VarUnion_LongLongValue{LongLongValue: x}
	case uint64:
		v.Size = 4
		v.VT = 21
		v.VarUnion.Value = &oaut.Variant_VarUnion_UlongLong{UlongLong: x}
	case float32:
		if math.IsNaN(float64(x)) || math.IsInf(float64(x), 0) {
			return nil, fmt.Errorf("write value must be finite")
		}
		v.VT = 4
		v.VarUnion.Value = &oaut.Variant_VarUnion_Float{Float: x}
	case float64:
		if math.IsNaN(x) || math.IsInf(x, 0) {
			return nil, fmt.Errorf("write value must be finite")
		}
		v.Size = 4
		v.VT = 5
		v.VarUnion.Value = &oaut.Variant_VarUnion_Double{Double: x}
	case string:
		if !utf8.ValidString(x) {
			return nil, fmt.Errorf("write string must be valid UTF-8")
		}
		if uint64(len(x)) > math.MaxUint32/2 {
			return nil, fmt.Errorf("write string is too large")
		}
		v.VT = 8
		v.VarUnion.Value = &oaut.Variant_VarUnion_BSTR{
			BSTR: &oaut.String{Data: x, Size: uint32(len(utf16.Encode([]rune(x))))},
		}
		v.Size = uint32((36 + 2*len(utf16.Encode([]rune(x))) + 7) / 8)
	default:
		return nil, fmt.Errorf(
			"unsupported write type %T; use bool, fixed-width integers, floats, or string",
			value,
		)
	}
	return v, nil
}
