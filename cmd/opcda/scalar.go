package main

import (
	"encoding/json"
	"fmt"
	"io"
	"math"
	"strconv"
	"time"

	opcda "github.com/dalugm/gopcda"
)

func printResult(out io.Writer, r *opcda.ReadResult) error {
	encoder := json.NewEncoder(out)
	encoder.SetIndent("", "  ")
	return encoder.Encode(struct {
		ItemID      string `json:"itemId"`
		Value       any    `json:"value"`
		Type        string `json:"type"`
		Quality     string `json:"quality"`
		QualityGood bool   `json:"qualityGood"`
		Timestamp   string `json:"timestamp"`
		TimestampMs int64  `json:"timestampMs"`
	}{r.ItemID, r.Value, fmt.Sprintf("%T", r.Value), fmt.Sprintf("0x%04X", uint16(r.Quality)), opcda.QualityIsGood(r.Quality), time.UnixMilli(r.SourceTimestampMs).UTC().Format(time.RFC3339Nano), r.SourceTimestampMs})
}

func parseWriteValue(text, kind string) (any, error) {
	switch kind {
	case "string":
		return text, nil
	case "bool":
		return strconv.ParseBool(text)
	case "date", "currency", "decimal", "error", "empty", "null", "int", "uint":
		return parseAutomationValue(text, kind)
	case "int8", "int16", "int32", "int64":
		bits, _ := strconv.Atoi(kind[3:])
		value, err := strconv.ParseInt(text, 10, bits)
		if err != nil {
			return nil, fmt.Errorf("invalid %s value %q: %w", kind, text, err)
		}
		switch bits {
		case 8:
			return int8(value), nil
		case 16:
			return int16(value), nil
		case 32:
			return int32(value), nil
		default:
			return value, nil
		}
	case "uint8", "uint16", "uint32", "uint64":
		bits, _ := strconv.Atoi(kind[4:])
		value, err := strconv.ParseUint(text, 10, bits)
		if err != nil {
			return nil, fmt.Errorf("invalid %s value %q: %w", kind, text, err)
		}
		switch bits {
		case 8:
			return uint8(value), nil
		case 16:
			return uint16(value), nil
		case 32:
			return uint32(value), nil
		default:
			return value, nil
		}
	}

	bits := 32
	switch kind {
	case "float32":
	case "float64":
		bits = 64
	default:
		return nil, fmt.Errorf(
			"unsupported write type %q; see the write types in --help",
			kind,
		)
	}
	value, err := strconv.ParseFloat(text, bits)
	if err != nil || math.IsNaN(value) || math.IsInf(value, 0) {
		return nil, fmt.Errorf("invalid finite %s value %q", kind, text)
	}
	if bits == 32 {
		return float32(value), nil
	}
	return value, nil
}
