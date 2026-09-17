package main

import (
	"errors"
	"fmt"
	"math/big"
	"strconv"
	"strings"
	"time"

	opcda "github.com/dalugm/gopcda"
)

func parseAutomationValue(text, kind string) (any, error) {
	switch kind {
	case "date":
		value, err := time.Parse(time.RFC3339Nano, text)
		if err != nil {
			return nil, err
		}
		if value.UTC().Year() < 100 || value.UTC().Year() > 9999 {
			return nil, errors.New("DATE outside years 100..9999")
		}
		return value.UTC(), nil
	case "empty", "null":
		if kind == "empty" && text == "" {
			return opcda.Variant{Type: opcda.VTEmpty}, nil
		}
		if kind == "null" && text == "null" {
			return opcda.Variant{Type: opcda.VTNull}, nil
		}
		return nil, fmt.Errorf(
			"%s requires %q",
			kind,
			map[string]string{"empty": "", "null": "null"}[kind],
		)
	case "int":
		value, err := strconv.ParseInt(text, 10, 32)
		if err != nil {
			return nil, err
		}
		return opcda.Variant{Type: opcda.VTInt, Value: int32(value)}, nil
	case "uint":
		value, err := strconv.ParseUint(text, 10, 32)
		if err != nil {
			return nil, err
		}
		return opcda.Variant{Type: opcda.VTUint, Value: uint32(value)}, nil
	case "error":
		base := 10
		if strings.HasPrefix(text, "0x") || strings.HasPrefix(text, "0X") {
			base = 0
		}
		value, err := strconv.ParseUint(text, base, 32)
		if err != nil {
			return nil, err
		}
		return opcda.ErrorCode(value), nil
	case "currency", "decimal":
		n, scale, negative, err := parseCoefficient(text)
		if err != nil {
			return nil, err
		}
		if kind == "currency" {
			if scale > 4 {
				return nil, errors.New("currency requires at most 4 fractional digits")
			}
			n.Mul(n, new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(4-scale)), nil))
			if negative {
				n.Neg(n)
			}
			if !n.IsInt64() {
				return nil, errors.New("currency outside int64 range")
			}
			return opcda.Currency(n.Int64()), nil
		}
		if scale > 28 || n.BitLen() > 96 {
			return nil, errors.New("decimal requires a 96-bit coefficient and scale at most 28")
		}
		lo := n.Uint64()
		hi := new(big.Int).Rsh(n, 64).Uint64()
		return opcda.Decimal{Hi: uint32(hi), Lo: lo, Scale: uint8(scale), Negative: negative}, nil
	default:
		return nil, fmt.Errorf("unsupported Automation type %q", kind)
	}
}

func parseCoefficient(text string) (*big.Int, int, bool, error) {
	negative := strings.HasPrefix(text, "-")
	digits := text
	if strings.HasPrefix(digits, "-") || strings.HasPrefix(digits, "+") {
		digits = digits[1:]
	}
	if len(digits) == 0 || len(digits) > 128 {
		return nil, 0, false, errors.New("invalid decimal value")
	}
	whole, fraction, hasDot := strings.Cut(digits, ".")
	if whole == "" || (hasDot && fraction == "") {
		return nil, 0, false, errors.New("decimal requires digits around the decimal point")
	}
	digits = whole + fraction
	for _, r := range digits {
		if r < '0' || r > '9' {
			return nil, 0, false, errors.New("decimal requires base-10 digits without an exponent")
		}
	}
	n, ok := new(big.Int).SetString(digits, 10)
	if !ok {
		return nil, 0, false, errors.New("invalid decimal coefficient")
	}
	return n, len(fraction), negative, nil
}
