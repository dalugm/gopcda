package opcda

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"github.com/oiweiwei/go-msrpc/msrpc/dcom/oaut"
	"github.com/oiweiwei/go-msrpc/ndr"
	syncio "github.com/oiweiwei/go-opcda/opc/opcda/iopcsyncio/v0"
)

func valueRoundTrip(t *testing.T, value any) any {
	t.Helper()
	v, err := writeVariant(value)
	if err != nil {
		t.Fatal(err)
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
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func TestArrayAndReferenceTypeMatrix(t *testing.T) {
	for _, tt := range []struct {
		vt    uint16
		value any
	}{
		{16, int8(-128)},
		{17, uint8(255)},
		{2, int16(-32768)},
		{18, uint16(65535)},
		{3, int32(-2147483648)},
		{19, uint32(0xffffffff)},
		{20, int64(-9223372036854775808)},
		{21, uint64(0xffffffffffffffff)},
		{4, float32(-1.25)},
		{5, float64(1.25)},
		{6, Currency(-12345)},
		{7, time.Date(1899, 12, 29, 6, 0, 0, 0, time.UTC)},
		{8, "\U0001f600\x00"},
		{10, ErrorCode(0x80004005)},
		{11, true},
		{22, int32(-7)},
		{23, uint32(7)},
	} {
		a := Array{ElementType: tt.vt, Bounds: []ArrayBound{{Count: 1}}, Values: []any{tt.value}}
		if got := valueRoundTrip(t, a); !reflect.DeepEqual(got, a) {
			t.Fatalf("array %x: got=%#v want=%#v", tt.vt, got, a)
		}
		v := Variant{Type: tt.vt | 0x4000, Value: tt.value}
		if got := valueRoundTrip(t, v); !reflect.DeepEqual(got, v) {
			t.Fatalf("reference %x: got=%#v want=%#v", tt.vt, got, v)
		}
	}
	inner := Array{ElementType: 8, Bounds: []ArrayBound{{Count: 2}}, Values: []any{"", "nested"}}
	for _, a := range []Array{
		{ElementType: 3},
		{ElementType: 3, Bounds: []ArrayBound{{Count: 0}}, Values: []any{}},
		{ElementType: 12, Bounds: []ArrayBound{{Count: 3}}, Values: []any{Variant{Type: 0x2008, Value: inner}, Variant{Type: 0x4003, Value: int32(2)}, Variant{Type: 1}}},
	} {
		if got := valueRoundTrip(t, a); !reflect.DeepEqual(got, a) {
			t.Fatalf("got=%#v want=%#v", got, a)
		}
	}
}

func TestExactValueJSON(t *testing.T) {
	for _, tt := range []struct {
		value any
		want  string
	}{
		{Currency(-9223372036854775808), `"-922337203685477.5808"`},
		{Decimal{Hi: 0xffffffff, Lo: 0xffffffffffffffff, Scale: 28}, `"7.9228162514264337593543950335"`},
		{Decimal{Negative: true, Scale: 2}, `"-0.00"`},
	} {
		got, err := json.Marshal(tt.value)
		if err != nil || string(got) != tt.want {
			t.Fatalf("got=%s want=%s error=%v", got, tt.want, err)
		}
	}
	if _, err := json.Marshal(Decimal{Scale: 29}); err == nil {
		t.Fatal("accepted invalid scale")
	}
}

func TestBatchWriteExtendedValues(t *testing.T) {
	values := []any{
		Array{ElementType: 8, Bounds: []ArrayBound{{Count: 2}}, Values: []any{"a\x00b", ""}},
		Decimal{Lo: 12345, Scale: 2},
		Variant{Type: 0x4003, Value: int32(-7)},
		"tail",
	}
	request := &batchWriteRequest{}
	for i, value := range values {
		v, err := writeVariant(value)
		if err != nil {
			t.Fatal(err)
		}
		request.handles = append(request.handles, uint32(i+1))
		request.values = append(request.values, v)
	}
	wire, err := ndr.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	var decoded syncio.WriteRequest
	if err := ndr.Unmarshal(
		wire,
		ndr.UnmarshalNDRFunc(func(ctx context.Context, r ndr.Reader) error {
			return decoded.UnmarshalNDR(ctx, bindingsReader{Reader: r, count: len(values)})
		}),
	); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(decoded.Server, request.handles) ||
		len(decoded.ItemValues) != len(values) {
		t.Fatal("lost write handles or values")
	}
	for i, v := range decoded.ItemValues {
		got, err := scalarValue(v)
		if err != nil || !reflect.DeepEqual(got, values[i]) {
			t.Fatalf("item %d got=%#v want=%#v error=%v", i, got, values[i], err)
		}
	}
}
