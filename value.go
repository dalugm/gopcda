package opcda

import (
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"time"

	"github.com/oiweiwei/go-msrpc/msrpc/dcom/oaut"
)

// Currency is an exact VT_CY value in units of 1/10000. JSON uses a decimal
// string so JavaScript consumers do not lose integer precision.
type Currency int64

func (v Currency) String() string { return fixedDecimal(big.NewInt(int64(v)), 4) }

// MarshalJSON emits an exact decimal string.
func (v Currency) MarshalJSON() ([]byte, error) { return json.Marshal(v.String()) }

// Decimal preserves the sign, 96-bit unsigned coefficient and decimal scale of
// VT_DECIMAL. Scale must be at most 28. JSON uses an exact decimal string.
type Decimal struct {
	Hi       uint32
	Lo       uint64
	Scale    uint8
	Negative bool
}

func (v Decimal) String() string {
	n := new(big.Int).SetUint64(uint64(v.Hi))
	n.Lsh(n, 64)
	n.Add(n, new(big.Int).SetUint64(v.Lo))
	if v.Negative && n.Sign() == 0 {
		return "-" + fixedDecimal(n, int(v.Scale))
	}
	if v.Negative {
		n.Neg(n)
	}
	return fixedDecimal(n, int(v.Scale))
}

// MarshalJSON emits an exact decimal string, rejecting invalid scales.
func (v Decimal) MarshalJSON() ([]byte, error) {
	if v.Scale > 28 {
		return nil, errors.New("DECIMAL scale exceeds 28")
	}
	return json.Marshal(v.String())
}

func fixedDecimal(n *big.Int, scale int) string {
	negative := n.Sign() < 0
	digits := new(big.Int).Abs(n).String()
	if scale > 0 {
		if len(digits) <= scale {
			digits = strings.Repeat("0", scale+1-len(digits)) + digits
		}
		digits = digits[:len(digits)-scale] + "." + digits[len(digits)-scale:]
	}
	if negative {
		return "-" + digits
	}
	return digits
}

// ErrorCode is a VT_ERROR data value, not an RPC or per-item operation failure.
type ErrorCode uint32

// Variant requests an explicit Automation VARTYPE when writing. Primitive Go
// inputs remain supported. EMPTY/NULL require nil; INT/UINT use int32/uint32.
type Variant struct {
	Type  uint16 `json:"type"`
	Value any    `json:"value"`
}

func writeExtendedValue(value any, depth int) (*oaut.Variant, error) {
	v := &oaut.Variant{Size: 3, VarUnion: &oaut.Variant_VarUnion{}}
	switch x := value.(type) {
	case Array:
		a, err := writeArray(x, depth+1)
		if err != nil {
			return nil, err
		}
		v.VT = VTArray | x.ElementType
		v.VarUnion.Value = &oaut.Variant_VarUnion_SafeArray{SafeArray: a}
	case Currency:
		v.VT = VTCY
		v.Size = 4
		v.VarUnion.Value = &oaut.Variant_VarUnion_Currency{
			Currency: &oaut.Currency{Int64: int64(x)},
		}
	case Decimal:
		if x.Scale > 28 {
			return nil, errors.New("DECIMAL scale exceeds 28")
		}
		var sign uint8
		if x.Negative {
			sign = 0x80
		}
		v.VT = VTDecimal
		v.Size = 5
		v.VarUnion.Value = &oaut.Variant_VarUnion_Decimal{
			Decimal: &oaut.Decimal{Hi32: x.Hi, Lo64: x.Lo, Scale: x.Scale, Sign: sign},
		}
	case ErrorCode:
		v.VT = VTError
		v.VarUnion.Value = &oaut.Variant_VarUnion_HResult{HResult: int32(x)}
	case time.Time:
		date, err := dateAutomation(x)
		if err != nil {
			return nil, err
		}
		v.VT = VTDate
		v.Size = 4
		v.VarUnion.Value = &oaut.Variant_VarUnion_Date{Date: date}
	case Variant:
		if x.Type&VTByRef != 0 {
			base := x.Type &^ VTByRef
			if base == VTVariant {
				nested, ok := x.Value.(Variant)
				if !ok {
					return nil, errors.New("VT_VARIANT|VT_BYREF requires Variant")
				}
				inner, err := writeVariantDepth(nested, depth+1)
				if err != nil {
					return nil, err
				}
				v.VT = x.Type
				v.VarUnion.Value = &oaut.Variant_VarUnion_VariantPtr{VariantPtr: inner}
				return v, nil
			}
			if base == VTEmpty || base == VTNull {
				return nil, errors.New("EMPTY/NULL cannot be BYREF")
			}
			inner, err := writeVariantDepth(Variant{Type: base, Value: x.Value}, depth+1)
			if err != nil {
				return nil, err
			}
			inner.VT = x.Type
			return inner, nil
		}
		switch x.Type {
		case VTEmpty, VTNull:
			if x.Value != nil {
				return nil, errors.New("EMPTY/NULL require nil")
			}
			v.VT = x.Type
		case VTInt:
			n, ok := x.Value.(int32)
			if !ok {
				return nil, errors.New("VT_INT requires int32")
			}
			v.VT = VTInt
			v.VarUnion.Value = &oaut.Variant_VarUnion_Int{Int: n}
		case VTUint:
			n, ok := x.Value.(uint32)
			if !ok {
				return nil, errors.New("VT_UINT requires uint32")
			}
			v.VT = VTUint
			v.VarUnion.Value = &oaut.Variant_VarUnion_Uint{Uint: n}
		default:
			if _, nested := x.Value.(Variant); nested {
				return nil, errors.New("nested scalar VARIANT is invalid")
			}
			var err error
			v, err = writeVariantDepth(x.Value, depth+1)
			if err != nil {
				return nil, err
			}
			if v.VT != x.Type {
				return nil, fmt.Errorf("value type 0x%04x does not match 0x%04x", v.VT, x.Type)
			}
		}
	default:
		return nil, fmt.Errorf("unsupported write type %T", value)
	}
	return v, nil
}

func dateAutomation(value time.Time) (float64, error) {
	value = value.UTC().Round(time.Millisecond)
	if value.Year() < 100 || value.Year() > 9999 {
		return 0, errors.New("DATE outside years 100..9999")
	}
	midnight := time.Date(value.Year(), value.Month(), value.Day(), 0, 0, 0, 0, time.UTC)
	days := (midnight.Unix() - time.Date(1899, 12, 30, 0, 0, 0, 0, time.UTC).Unix()) / 86400
	fraction := float64(value.Sub(midnight)) / float64(24*time.Hour)
	if days < 0 {
		return float64(days) - fraction, nil
	}
	return float64(days) + fraction, nil
}
