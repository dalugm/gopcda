package opcda

import (
	"errors"
	"fmt"
	"math"

	"github.com/oiweiwei/go-msrpc/msrpc/dcom/oaut"
)

// ArrayBound describes one SAFEARRAY dimension. Count is the number of elements.
type ArrayBound struct {
	Lower int32  `json:"lower"`
	Count uint32 `json:"count"`
}

// Array preserves SAFEARRAY element types and bounds. Bounds are in wire order:
// rightmost (fastest-changing) dimension first. Values are flat in that order.
// A nil Bounds and nil Values pair represents a null SAFEARRAY.
type Array struct {
	ElementType uint16       `json:"elementType"`
	Bounds      []ArrayBound `json:"bounds"`
	Values      []any        `json:"values"`
}

func arrayLayout(vt uint16) (tag, width uint32, features uint16, err error) {
	switch vt {
	case VTI1, VTUI1:
		return uint32(oaut.SafeArrayTypeI1), 1, uint16(oaut.AdvFeatureFlagHaveVarType), nil
	case VTI2, VTUI2, VTBool:
		return uint32(oaut.SafeArrayTypeI2), 2, uint16(oaut.AdvFeatureFlagHaveVarType), nil
	case VTI4, VTUI4, VTR4, VTError, VTInt, VTUint:
		return uint32(oaut.SafeArrayTypeI4), 4, uint16(oaut.AdvFeatureFlagHaveVarType), nil
	case VTI8, VTUI8, VTR8, VTCY, VTDate:
		return uint32(oaut.SafeArrayTypeI8), 8, uint16(oaut.AdvFeatureFlagHaveVarType), nil
	case VTBSTR:
		return uint32(
				oaut.SafeArrayTypeString,
			), 4, uint16(
				oaut.AdvFeatureFlagString | oaut.AdvFeatureFlagHaveVarType,
			), nil
	case VTVariant:
		return uint32(
				oaut.SafeArrayTypeVariant,
			), 16, uint16(
				oaut.AdvFeatureFlagVariant | oaut.AdvFeatureFlagHaveVarType,
			), nil
	default:
		return 0, 0, 0, fmt.Errorf(
			"unsupported SAFEARRAY element type 0x%04x (use VARIANT elements for DECIMAL)",
			vt,
		)
	}
}

func arrayCount(bounds []ArrayBound) (int, error) {
	if len(bounds) == 0 || len(bounds) > 32 {
		return 0, errors.New("SAFEARRAY requires 1..32 dimensions")
	}
	count := uint64(1)
	for _, b := range bounds {
		if b.Count > 0 && int64(b.Lower)+int64(b.Count)-1 > math.MaxInt32 {
			return 0, errors.New("SAFEARRAY upper bound overflows int32")
		}
		count *= uint64(b.Count)
		if count > 1<<20 {
			return 0, errors.New("SAFEARRAY exceeds 1048576 elements")
		}
	}
	return int(count), nil
}

func writeArray(a Array, depth int) (*oaut.SafeArray, error) {
	tag, width, features, err := arrayLayout(a.ElementType)
	if err != nil {
		return nil, err
	}
	if a.Bounds == nil && a.Values == nil {
		return nil, nil
	}
	count, err := arrayCount(a.Bounds)
	if err != nil {
		return nil, err
	}
	if count != len(a.Values) {
		return nil, fmt.Errorf("SAFEARRAY has %d values, bounds require %d", len(a.Values), count)
	}
	result := &oaut.SafeArray{
		DimsCount:      uint16(len(a.Bounds)),
		Features:       features,
		ElementsLength: width,
		LocksCount:     uint32(a.ElementType) << 16,
		ArrayStructs:   &oaut.SafeArrayUnion{SafeArrayType: tag},
	}
	for _, b := range a.Bounds {
		result.Bound = append(
			result.Bound,
			&oaut.SafeArrayBound{ElementsCount: b.Count, LowerBound: b.Lower},
		)
	}
	var bytes []byte
	var words []uint16
	var longs []uint32
	var hypers []int64
	var strings []*oaut.String
	var variants []*oaut.Variant
	for i, value := range a.Values {
		if a.ElementType == VTVariant {
			v, e := writeVariantDepth(value, depth)
			if e != nil {
				return nil, fmt.Errorf("SAFEARRAY element %d: %w", i, e)
			}
			variants = append(variants, v)
			continue
		}
		v, e := writeVariantDepth(Variant{Type: a.ElementType, Value: value}, depth)
		if e != nil {
			return nil, fmt.Errorf("SAFEARRAY element %d: %w", i, e)
		}
		switch x := v.VarUnion.GetValue().(type) {
		case uint8:
			bytes = append(bytes, x)
		case int16:
			words = append(words, uint16(x))
		case uint16:
			words = append(words, x)
		case int32:
			longs = append(longs, uint32(x))
		case uint32:
			longs = append(longs, x)
		case float32:
			longs = append(longs, math.Float32bits(x))
		case int64:
			hypers = append(hypers, x)
		case uint64:
			hypers = append(hypers, int64(x))
		case float64:
			hypers = append(hypers, int64(math.Float64bits(x)))
		case *oaut.Currency:
			hypers = append(hypers, x.Int64)
		case *oaut.String:
			strings = append(strings, x)
		default:
			return nil, fmt.Errorf("invalid SAFEARRAY element %d", i)
		}
	}
	size := uint32(count)
	switch tag {
	case uint32(oaut.SafeArrayTypeI1):
		result.ArrayStructs.Value = &oaut.SafeArrayUnion_Byte{
			Byte: &oaut.ByteSizedArray{Size: size, Data: bytes},
		}
	case uint32(oaut.SafeArrayTypeI2):
		result.ArrayStructs.Value = &oaut.SafeArrayUnion_Word{
			Word: &oaut.WordSizedArray{Size: size, Data: words},
		}
	case uint32(oaut.SafeArrayTypeI4):
		result.ArrayStructs.Value = &oaut.SafeArrayUnion_Long{
			Long: &oaut.DwordSizedArray{Size: size, Data: longs},
		}
	case uint32(oaut.SafeArrayTypeI8):
		result.ArrayStructs.Value = &oaut.SafeArrayUnion_Hyper{
			Hyper: &oaut.HyperSizedArray{Size: size, Data: hypers},
		}
	case uint32(oaut.SafeArrayTypeString):
		result.ArrayStructs.Value = &oaut.SafeArrayUnion_String{
			String: &oaut.SafeArrayString{Size: size, String: strings},
		}
	case uint32(oaut.SafeArrayTypeVariant):
		result.ArrayStructs.Value = &oaut.SafeArrayUnion_Variant{
			Variant: &oaut.SafeArrayVariant{Size: size, Variant: variants},
		}
	}
	return result, nil
}

func readArray(a *oaut.SafeArray, vt uint16, depth int) (Array, error) {
	result := Array{ElementType: vt}
	tag, width, features, err := arrayLayout(vt)
	if err != nil {
		return result, err
	}
	if a == nil {
		return result, nil
	}
	if a.ArrayStructs == nil || a.ArrayStructs.SafeArrayType != tag || a.ElementsLength != width ||
		int(a.DimsCount) != len(a.Bound) {
		return result, errors.New("inconsistent SAFEARRAY header")
	}
	if a.Features&uint16(oaut.AdvFeatureFlagHaveVarType) != 0 && uint16(a.LocksCount>>16) != vt {
		return result, errors.New("SAFEARRAY element type disagrees with VARIANT")
	}
	typeFeatures := uint16(
		oaut.AdvFeatureFlagString | oaut.AdvFeatureFlagUnknown | oaut.AdvFeatureFlagDispatch | oaut.AdvFeatureFlagVariant,
	)
	if a.Features&typeFeatures != features&typeFeatures {
		return result, errors.New("inconsistent SAFEARRAY features")
	}
	for _, b := range a.Bound {
		if b == nil {
			return result, errors.New("missing SAFEARRAY bound")
		}
		result.Bounds = append(
			result.Bounds,
			ArrayBound{Lower: b.LowerBound, Count: b.ElementsCount},
		)
	}
	count, err := arrayCount(result.Bounds)
	if err != nil {
		return result, err
	}
	result.Values = make([]any, 0, count)
	var size uint32
	switch p := a.ArrayStructs.GetValue().(type) {
	case *oaut.ByteSizedArray:
		if p == nil {
			return result, errors.New("missing SAFEARRAY data")
		}
		size = p.Size
		for _, n := range p.Data {
			if vt == VTI1 {
				result.Values = append(result.Values, int8(n))
			} else {
				result.Values = append(result.Values, n)
			}
		}
	case *oaut.WordSizedArray:
		if p == nil {
			return result, errors.New("missing SAFEARRAY data")
		}
		size = p.Size
		for _, n := range p.Data {
			var v any = n
			switch vt {
			case VTI2:
				v = int16(n)
			case VTBool:
				v = n != 0
			}
			result.Values = append(result.Values, v)
		}
	case *oaut.DwordSizedArray:
		if p == nil {
			return result, errors.New("missing SAFEARRAY data")
		}
		size = p.Size
		for _, n := range p.Data {
			var v any = n
			switch vt {
			case VTI4, VTInt:
				v = int32(n)
			case VTR4:
				v = math.Float32frombits(n)
			case VTError:
				v = ErrorCode(n)
			}
			result.Values = append(result.Values, v)
		}
	case *oaut.HyperSizedArray:
		if p == nil {
			return result, errors.New("missing SAFEARRAY data")
		}
		size = p.Size
		for _, n := range p.Data {
			var v any = n
			switch vt {
			case VTUI8:
				v = uint64(n)
			case VTR8:
				v = math.Float64frombits(uint64(n))
			case VTCY:
				v = Currency(n)
			case VTDate:
				v, err = automationDate(math.Float64frombits(uint64(n)))
				if err != nil {
					return result, err
				}
			}
			result.Values = append(result.Values, v)
		}
	case *oaut.SafeArrayString:
		if p == nil {
			return result, errors.New("missing SAFEARRAY data")
		}
		size = p.Size
		for _, s := range p.String {
			value := ""
			if s != nil {
				value = s.Data
			}
			result.Values = append(result.Values, value)
		}
	case *oaut.SafeArrayVariant:
		if p == nil {
			return result, errors.New("missing SAFEARRAY data")
		}
		size = p.Size
		for i, v := range p.Variant {
			value, e := scalarValueDepth(v, depth)
			if e != nil {
				return result, fmt.Errorf("SAFEARRAY element %d: %w", i, e)
			}
			if v.VT&VTByRef == 0 {
				value = Variant{Type: v.VT, Value: value}
			}
			result.Values = append(result.Values, value)
		}
	default:
		return result, errors.New("invalid SAFEARRAY payload")
	}
	if int(size) != count || len(result.Values) != count {
		return result, errors.New("SAFEARRAY payload does not match bounds")
	}
	return result, nil
}
