package opcda

import (
	"context"
	"encoding/binary"
	"encoding/hex"
	"reflect"
	"testing"

	"github.com/oiweiwei/go-msrpc/msrpc/dcom/oaut"
	"github.com/oiweiwei/go-msrpc/ndr"
)

func TestArrayRoundTrip(t *testing.T) {
	for _, a := range []Array{
		{ElementType: 5, Bounds: []ArrayBound{{Lower: -2, Count: 2}, {Lower: 1, Count: 2}}, Values: []any{float64(1), float64(2), float64(3), float64(4)}},
		{ElementType: 8, Bounds: []ArrayBound{{Count: 3}}, Values: []any{"", "a\x00b", "\u6d41\u91cf"}},
		{ElementType: 21, Bounds: []ArrayBound{{Count: 2}}, Values: []any{uint64(0), uint64(0xffffffffffffffff)}},
		{ElementType: 12, Bounds: []ArrayBound{{Count: 2}}, Values: []any{Variant{Type: 14, Value: Decimal{Lo: 12345, Scale: 2}}, Variant{Type: 8, Value: "\u6d41\u91cf"}}},
	} {
		v, err := writeVariant(a)
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
		var result oaut.Variant
		if err := ndr.Unmarshal(
			wire,
			ndr.UnmarshalNDRFunc(
				func(ctx context.Context, r ndr.Reader) error { return unmarshalReadVariant(ctx, r, &result) },
			),
		); err != nil {
			t.Fatal(err)
		}
		got, err := scalarValue(&result)
		if err != nil || !reflect.DeepEqual(got, a) {
			t.Fatalf("got=%#v want=%#v err=%v", got, a, err)
		}
	}
}

func TestArrayWireFixture(t *testing.T) {
	// Independent NDR32 VT_ARRAY|VT_I4: two values, lower bound -2.
	wire, err := hex.DecodeString(
		"0a0000000000000003200000000000000020000001000000020000000100000001008000040000000000030003000000020000000300000002000000feffffff020000000100000002000000",
	)
	if err != nil {
		t.Fatal(err)
	}
	var v oaut.Variant
	if err := ndr.Unmarshal(
		wire,
		ndr.UnmarshalNDRFunc(
			func(ctx context.Context, r ndr.Reader) error { return unmarshalReadVariant(ctx, r, &v) },
		),
	); err != nil {
		t.Fatal(err)
	}
	got, err := scalarValue(&v)
	want := Array{
		ElementType: 3,
		Bounds:      []ArrayBound{{Lower: -2, Count: 2}},
		Values:      []any{int32(1), int32(2)},
	}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("got=%#v err=%v", got, err)
	}
	// The released upstream codec must read our wire contract and produce
	// a typed-array response that the local validation layer can consume.
	var upstream oaut.Variant
	if err := ndr.Unmarshal(wire, &upstream); err != nil {
		t.Fatal(err)
	}
	encoded, err := ndr.Marshal(&upstream)
	if err != nil {
		t.Fatal(err)
	}
	if err := ndr.Unmarshal(
		encoded,
		ndr.UnmarshalNDRFunc(func(ctx context.Context, r ndr.Reader) error {
			return unmarshalReadVariant(ctx, r, &v)
		}),
	); err != nil {
		t.Fatal(err)
	}
	got, err = scalarValue(&v)
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("upstream array lost values or bounds: got=%#v err=%v", got, err)
	}
	for _, mutation := range []struct {
		offset int
		value  uint32
	}{{16, 5}, {28, 0}, {40, 5 << 16}, {48, 3}, {64, 0}, {64, 0xffffffff}} {
		invalid := append([]byte(nil), wire...)
		binary.LittleEndian.PutUint32(invalid[mutation.offset:], mutation.value)
		var v oaut.Variant
		err := ndr.Unmarshal(
			invalid,
			ndr.UnmarshalNDRFunc(
				func(ctx context.Context, r ndr.Reader) error { return unmarshalReadVariant(ctx, r, &v) },
			),
		)
		if err == nil {
			_, err = scalarValue(&v)
		}
		if err == nil {
			t.Fatalf("accepted malformed array at offset %d", mutation.offset)
		}
	}
	for end := range wire {
		var v oaut.Variant
		if err := ndr.Unmarshal(
			wire[:end],
			ndr.UnmarshalNDRFunc(
				func(ctx context.Context, r ndr.Reader) error { return unmarshalReadVariant(ctx, r, &v) },
			),
		); err == nil {
			t.Fatalf("accepted truncation at %d", end)
		}
	}
}

func TestInvalidArraysRejected(t *testing.T) {
	for _, a := range []Array{
		{ElementType: 14, Bounds: []ArrayBound{{Count: 1}}, Values: []any{Decimal{}}},
		{ElementType: 5, Bounds: []ArrayBound{{Count: 2}}, Values: []any{1.0}},
		{ElementType: 3, Bounds: []ArrayBound{{Count: 1}}, Values: []any{int64(1)}},
		{ElementType: 3, Bounds: []ArrayBound{{Count: 0xffffffff}}},
		{ElementType: 3, Bounds: []ArrayBound{{Count: 2, Lower: 0x7fffffff}}},
	} {
		if _, err := writeVariant(a); err == nil {
			t.Fatalf("accepted %#v", a)
		}
	}
}
