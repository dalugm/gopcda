package opcda

import (
	"context"
	"encoding/binary"
	"math"
	"reflect"
	"testing"

	"github.com/oiweiwei/go-msrpc/msrpc/dcom/oaut"
	"github.com/oiweiwei/go-msrpc/ndr"
)

func TestWriteScalarTypes(t *testing.T) {
	for _, tc := range []struct {
		value any
		vt    uint16
		bits  uint64
		width int
	}{
		{true, 11, 0xffff, 2},
		{false, 11, 0, 2},
		{int8(-128), 16, 128, 1},
		{uint8(255), 17, 255, 1},
		{int16(-32768), 2, 32768, 2},
		{uint16(65535), 18, 65535, 2},
		{int32(math.MinInt32), 3, 1 << 31, 4},
		{uint32(math.MaxUint32), 19, math.MaxUint32, 4},
		{int64(math.MinInt64), 20, 1 << 63, 8},
		{uint64(math.MaxUint64), 21, math.MaxUint64, 8},
		{float32(12.5), 4, uint64(math.Float32bits(12.5)), 4},
		{float64(-12.5), 5, math.Float64bits(-12.5), 8},
	} {
		v, err := writeVariant(tc.value)
		if err != nil {
			t.Fatal(err)
		}
		b, err := ndr.Marshal(
			ndr.MarshalNDRFunc(
				func(ctx context.Context, w ndr.Writer) error { return marshalWriteVariant(ctx, w, v) },
			),
		)
		if err != nil {
			t.Fatal(err)
		}
		off := 20
		if tc.width == 8 {
			off = 24
		}
		var got uint64
		for i := 0; i < tc.width; i++ {
			got |= uint64(b[off+i]) << (8 * i)
		}
		if binary.LittleEndian.Uint16(b[8:]) != tc.vt ||
			binary.LittleEndian.Uint32(b[16:]) != uint32(tc.vt) ||
			got != tc.bits {
			t.Fatalf("%T: incorrect VARIANT: %x", tc.value, b)
		}
		var decoded oaut.Variant
		if err := ndr.Unmarshal(
			b,
			ndr.UnmarshalNDRFunc(
				func(ctx context.Context, r ndr.Reader) error { return unmarshalReadVariant(ctx, r, &decoded) },
			),
		); err != nil {
			t.Fatal(err)
		}
		value, err := scalarValue(&decoded)
		if err != nil || !reflect.DeepEqual(value, tc.value) {
			t.Fatalf("%T round trip: %v, %v", tc.value, value, err)
		}
	}
}

func TestStringVariantWire(t *testing.T) {
	for _, tc := range []struct {
		text  string
		units []uint16
	}{
		{"", nil}, {"a\x00", []uint16{97, 0}}, {"\x00", []uint16{0}}, {"hello", []uint16{'h', 'e', 'l', 'l', 'o'}}, {"a\x00b", []uint16{'a', 0, 'b'}}, {"\U0001f680", []uint16{0xd83d, 0xde80}},
	} {
		v, err := writeVariant(tc.text)
		if err != nil {
			t.Fatal(err)
		}
		b, err := ndr.Marshal(
			ndr.MarshalNDRFunc(
				func(ctx context.Context, w ndr.Writer) error { return marshalWriteVariant(ctx, w, v) },
			),
		)
		if err != nil {
			t.Fatal(err)
		}
		if binary.LittleEndian.Uint16(b[8:]) != 8 || binary.LittleEndian.Uint32(b[16:]) != 8 ||
			binary.LittleEndian.Uint32(b[20:]) == 0 {
			t.Fatalf("BSTR header: %x", b)
		}
		if binary.LittleEndian.Uint32(b[24:]) != uint32(len(tc.units)) ||
			binary.LittleEndian.Uint32(b[32:]) != uint32(len(tc.units)) {
			t.Fatalf("BSTR length: %x", b)
		}
		for i, unit := range tc.units {
			if binary.LittleEndian.Uint16(b[36+i*2:]) != unit {
				t.Fatalf("BSTR UTF-16: %x", b)
			}
		}
		if binary.LittleEndian.Uint32(b[28:]) != uint32(len(tc.units)*2) {
			t.Fatalf("BSTR byte count: %x", b)
		}
		if v.Size != uint32((len(b)+7)/8) {
			t.Fatalf("BSTR size: %x", b)
		}
		var decoded oaut.Variant
		if err := ndr.Unmarshal(
			b,
			ndr.UnmarshalNDRFunc(
				func(ctx context.Context, r ndr.Reader) error { return unmarshalReadVariant(ctx, r, &decoded) },
			),
		); err != nil {
			t.Fatal(err)
		}
		value, err := scalarValue(&decoded)
		if err != nil || value != tc.text {
			t.Fatalf("BSTR round trip: %q, %v", value, err)
		}
	}
	if _, err := writeVariant("\xff"); err == nil {
		t.Fatal("accepted invalid UTF-8")
	}
}

func TestBSTRMalformedLengths(t *testing.T) {
	v, err := writeVariant("a\x00")
	if err != nil {
		t.Fatal(err)
	}
	b, err := ndr.Marshal(
		ndr.MarshalNDRFunc(
			func(ctx context.Context, w ndr.Writer) error { return marshalWriteVariant(ctx, w, v) },
		),
	)
	if err != nil {
		t.Fatal(err)
	}
	decode := func(b []byte) error {
		var v oaut.Variant
		return ndr.Unmarshal(
			b,
			ndr.UnmarshalNDRFunc(
				func(ctx context.Context, r ndr.Reader) error { return unmarshalReadVariant(ctx, r, &v) },
			),
		)
	}
	for i := 0; i < len(b); i++ {
		if err := decode(b[:i]); err == nil {
			t.Fatalf("accepted truncation %d", i)
		}
	}
	for _, offset := range []int{24, 28, 32} {
		broken := append([]byte(nil), b...)
		binary.LittleEndian.PutUint32(broken[offset:], math.MaxUint32)
		if err := decode(broken); err == nil {
			t.Fatalf("accepted invalid BSTR field at %d", offset)
		}
	}
}

func TestMixedBatchWriteWire(t *testing.T) {
	values := make([]*oaut.Variant, 0, 3)
	for _, x := range []any{"A", uint64(math.MaxUint64), ""} {
		v, err := writeVariant(x)
		if err != nil {
			t.Fatal(err)
		}
		values = append(values, v)
	}
	b, err := ndr.Marshal(&batchWriteRequest{handles: []uint32{1, 2, 3}, values: values})
	if err != nil {
		t.Fatal(err)
	}
	// All three VARIANT pointers precede their referents. Each BSTR payload
	// precedes the next VARIANT, which is aligned to eight bytes.
	if len(b) != 180 || binary.LittleEndian.Uint16(b[80:]) != 8 ||
		binary.LittleEndian.Uint16(b[108:]) != 'A' ||
		binary.LittleEndian.Uint16(b[120:]) != 21 ||
		binary.LittleEndian.Uint64(b[136:]) != math.MaxUint64 ||
		binary.LittleEndian.Uint16(b[152:]) != 8 ||
		binary.LittleEndian.Uint32(b[172:]) != 0 {
		t.Fatalf("mixed batch: %x", b)
	}
}
