package opcda

import (
	"context"
	"encoding/binary"
	"math"
	"testing"
	"time"

	"github.com/oiweiwei/go-msrpc/msrpc/dcom/oaut"
	"github.com/oiweiwei/go-msrpc/ndr"
)

func TestReadAutomationDate(t *testing.T) {
	for _, tt := range []struct {
		value float64
		want  string
	}{
		{0, "1899-12-30T00:00:00Z"},
		{0.25, "1899-12-30T06:00:00Z"},
		{-0.25, "1899-12-30T06:00:00Z"},
		{-1.25, "1899-12-29T06:00:00Z"},
		{25569, "1970-01-01T00:00:00Z"},
		{25569 + 0.123/86400, "1970-01-01T00:00:00.123Z"},
		{-657434, "0100-01-01T00:00:00Z"},
		{2958465.5, "9999-12-31T12:00:00Z"},
	} {
		// Hand-built scalar VARIANT header and DATE payload, not the encoder under test.
		wire := make([]byte, 32)
		binary.LittleEndian.PutUint32(wire, 4)
		binary.LittleEndian.PutUint16(wire[8:], 7)
		binary.LittleEndian.PutUint32(wire[16:], 7)
		binary.LittleEndian.PutUint64(wire[24:], math.Float64bits(tt.value))
		var variant oaut.Variant
		if err := ndr.Unmarshal(
			wire,
			ndr.UnmarshalNDRFunc(
				func(ctx context.Context, r ndr.Reader) error { return unmarshalReadVariant(ctx, r, &variant) },
			),
		); err != nil {
			t.Fatal(err)
		}
		got, err := scalarValue(&variant)
		if err != nil {
			t.Fatalf("DATE %v: %v", tt.value, err)
		}
		date, ok := got.(time.Time)
		if !ok || date.Format(time.RFC3339Nano) != tt.want {
			t.Fatalf("DATE %v = %v, want %s", tt.value, got, tt.want)
		}
	}
}

func TestReadAutomationDateRejectsInvalidValues(t *testing.T) {
	for _, value := range []float64{math.NaN(), math.Inf(1), math.Inf(-1), -657435, 2958466} {
		v := &oaut.Variant{
			VT:       7,
			VarUnion: &oaut.Variant_VarUnion{Value: &oaut.Variant_VarUnion_Date{Date: value}},
		}
		if _, err := scalarValue(v); err == nil {
			t.Fatalf("accepted DATE %v", value)
		}
	}
}

func TestDateWriteBoundaries(t *testing.T) {
	for _, value := range []time.Time{
		time.Date(100, 1, 1, 0, 0, 0, 0, time.UTC),
		time.Date(9999, 12, 31, 23, 59, 59, 999000000, time.UTC),
		time.Date(1899, 12, 29, 23, 59, 59, 123000000, time.UTC),
	} {
		if got := valueRoundTrip(t, value); got != value {
			t.Fatalf("got=%v want=%v", got, value)
		}
	}
	if _, err := writeVariant(
		time.Date(9999, 12, 31, 23, 59, 59, 999999999, time.UTC),
	); err == nil {
		t.Fatal("accepted DATE rounding outside supported range")
	}
}
