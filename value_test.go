package opcda

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/oiweiwei/go-msrpc/msrpc/dcom/oaut"
	"github.com/oiweiwei/go-msrpc/ndr"
)

func TestExtendedScalarRoundTrip(t *testing.T) {
	for _, tt := range []struct {
		in, want any
		vt       uint16
	}{
		{Currency(-9223372036854775807), Currency(-9223372036854775807), 6},
		{Decimal{Hi: 0xffffffff, Lo: 0xffffffffffffffff, Scale: 28, Negative: true}, Decimal{Hi: 0xffffffff, Lo: 0xffffffffffffffff, Scale: 28, Negative: true}, 14},
		{ErrorCode(0x80004005), ErrorCode(0x80004005), 10},
		{time.Date(2026, 9, 14, 21, 37, 41, 830000000, time.UTC), time.Date(2026, 9, 14, 21, 37, 41, 830000000, time.UTC), 7},
		{Variant{Type: 0}, nil, 0},
		{Variant{Type: 1}, nil, 1},
		{Variant{Type: 22, Value: int32(-7)}, int32(-7), 22},
		{Variant{Type: 23, Value: uint32(0xffffffff)}, uint32(0xffffffff), 23},
	} {
		v, err := writeVariant(tt.in)
		if err != nil {
			t.Fatalf("%T: %v", tt.in, err)
		}
		if v.VT != tt.vt {
			t.Fatalf("VT=%d want %d", v.VT, tt.vt)
		}
		wire, err := ndr.Marshal(
			ndr.MarshalNDRFunc(
				func(ctx context.Context, w ndr.Writer) error { return marshalWriteVariant(ctx, w, v) },
			),
		)
		if err != nil {
			t.Fatal(err)
		}
		var decoded oaut.Variant
		if err := ndr.Unmarshal(
			wire,
			ndr.UnmarshalNDRFunc(
				func(ctx context.Context, r ndr.Reader) error { return unmarshalReadVariant(ctx, r, &decoded) },
			),
		); err != nil {
			t.Fatal(err)
		}
		got, err := scalarValue(&decoded)
		if err != nil || !reflect.DeepEqual(got, tt.want) {
			t.Fatalf("%T: got %#v want %#v err=%v", tt.in, got, tt.want, err)
		}
	}
}

func TestTypedValueRejectsInvalidInputs(t *testing.T) {
	for _, v := range []any{Decimal{Scale: 29}, Variant{Type: 9}, Variant{Type: 13}, Variant{Type: 36}, Variant{Type: 22, Value: int64(1)}, Variant{Type: 0, Value: 1}, time.Date(99, 1, 1, 0, 0, 0, 0, time.UTC)} {
		if _, err := writeVariant(v); err == nil {
			t.Fatalf("accepted %#v", v)
		}
	}
}
