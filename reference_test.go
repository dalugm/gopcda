package opcda

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/hex"
	"reflect"
	"testing"

	"github.com/oiweiwei/go-msrpc/msrpc/dcom/oaut"
	"github.com/oiweiwei/go-msrpc/ndr"
)

func TestBYREFRoundTrip(t *testing.T) {
	for _, value := range []Variant{
		{Type: 0x4010, Value: int8(-7)},
		{Type: 0x4003, Value: int32(-123)},
		{Type: 0x4006, Value: Currency(-12345)},
		{Type: 0x400e, Value: Decimal{Lo: 123, Scale: 2}},
		{Type: 0x4008, Value: "a\x00\U0001f600"},
		{Type: 0x6005, Value: Array{ElementType: 5, Bounds: []ArrayBound{{Count: 2}}, Values: []any{1.5, 2.5}}},
		{Type: 0x400c, Value: Variant{Type: 8, Value: ""}},
		{Type: 0x400c, Value: Variant{Type: 0x4003, Value: int32(-123)}},
		{Type: 0x400c, Value: Variant{Type: 0x400c, Value: Variant{Type: 3, Value: int32(7)}}},
	} {
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
		var got oaut.Variant
		if err := ndr.Unmarshal(
			wire,
			ndr.UnmarshalNDRFunc(
				func(ctx context.Context, r ndr.Reader) error { return unmarshalReadVariant(ctx, r, &got) },
			),
		); err != nil {
			t.Fatalf("type %x: %v", value.Type, err)
		}
		result, err := scalarValue(&got)
		if err != nil || !reflect.DeepEqual(result, value) {
			t.Fatalf("got=%#v want=%#v error=%v", result, value, err)
		}
	}
}

// Independent NDR32 fixture: a unique-pointer marker followed by the deferred
// signed byte. CHAR is not an NDR string.
func TestBYREFCharFixture(t *testing.T) {
	data, err := hex.DecodeString("040000000000000010400000000000001040000001000000f9")
	if err != nil {
		t.Fatal(err)
	}
	var v oaut.Variant
	if err := ndr.Unmarshal(
		data,
		ndr.UnmarshalNDRFunc(
			func(ctx context.Context, r ndr.Reader) error { return unmarshalReadVariant(ctx, r, &v) },
		),
	); err != nil {
		t.Fatal(err)
	}
	got, err := scalarValue(&v)
	if err != nil || got != (Variant{Type: 0x4010, Value: int8(-7)}) {
		t.Fatalf("got=%v err=%v", got, err)
	}
	encoded, err := ndr.Marshal(ndr.MarshalNDRFunc(func(ctx context.Context, w ndr.Writer) error {
		return marshalWriteVariant(ctx, w, &v)
	}))
	if err != nil {
		t.Fatal(err)
	}
	if len(encoded) != len(data) || binary.LittleEndian.Uint32(encoded[20:24]) == 0 {
		t.Fatalf("invalid BYREF CHAR wire: %x", encoded)
	}
	binary.LittleEndian.PutUint32(encoded[20:24], 1)
	if !bytes.Equal(encoded, data) {
		t.Fatalf("wire=%x want=%x", encoded, data)
	}
	for end := range len(data) {
		err := ndr.Unmarshal(
			data[:end],
			ndr.UnmarshalNDRFunc(func(ctx context.Context, r ndr.Reader) error {
				return unmarshalReadVariant(ctx, r, &oaut.Variant{})
			}),
		)
		if err == nil {
			t.Fatalf("accepted truncation at byte %d", end)
		}
	}
	binary.LittleEndian.PutUint32(data[20:24], 0)
	err = ndr.Unmarshal(
		data[:24],
		ndr.UnmarshalNDRFunc(func(ctx context.Context, r ndr.Reader) error {
			return unmarshalReadVariant(ctx, r, &oaut.Variant{})
		}),
	)
	if err == nil {
		t.Fatal("accepted null BYREF CHAR")
	}
}

func TestBYREFNumericUpstreamInteroperability(t *testing.T) {
	for _, tt := range []struct {
		name  string
		value Variant
		union *oaut.Variant_VarUnion
		raw   any
	}{
		{"char_negative", Variant{Type: VTI1 | VTByRef, Value: int8(-7)}, &oaut.Variant_VarUnion{Value: &oaut.Variant_VarUnion_CharPtr{CharPtr: 249}}, rune(249)},
		{"char_min", Variant{Type: VTI1 | VTByRef, Value: int8(-128)}, &oaut.Variant_VarUnion{Value: &oaut.Variant_VarUnion_CharPtr{CharPtr: 128}}, rune(128)},
		{"char_zero", Variant{Type: VTI1 | VTByRef, Value: int8(0)}, &oaut.Variant_VarUnion{Value: &oaut.Variant_VarUnion_CharPtr{}}, rune(0)},
		{"byte", Variant{Type: VTUI1 | VTByRef, Value: uint8(200)}, &oaut.Variant_VarUnion{Value: &oaut.Variant_VarUnion_BytePtr{BytePtr: 200}}, uint8(200)},
		{"short", Variant{Type: VTI2 | VTByRef, Value: int16(-123)}, &oaut.Variant_VarUnion{Value: &oaut.Variant_VarUnion_ShortPtr{ShortPtr: -123}}, int16(-123)},
		{"long", Variant{Type: VTI4 | VTByRef, Value: int32(-123456)}, &oaut.Variant_VarUnion{Value: &oaut.Variant_VarUnion_LongPtr{LongPtr: -123456}}, int32(-123456)},
		{"double", Variant{Type: VTR8 | VTByRef, Value: -12.5}, &oaut.Variant_VarUnion{Value: &oaut.Variant_VarUnion_DoublePtr{DoublePtr: -12.5}}, -12.5},
		{"bool", Variant{Type: VTBool | VTByRef, Value: true}, &oaut.Variant_VarUnion{Value: &oaut.Variant_VarUnion_BoolPtr{BoolPtr: -1}}, int16(-1)},
	} {
		t.Run(tt.name, func(t *testing.T) {
			upstream := &oaut.Variant{Size: 4, VT: tt.value.Type, VarUnion: tt.union}
			wire, err := ndr.Marshal(upstream)
			if err != nil {
				t.Fatal(err)
			}
			var decoded oaut.Variant
			if err := ndr.Unmarshal(
				wire,
				ndr.UnmarshalNDRFunc(func(ctx context.Context, r ndr.Reader) error {
					return unmarshalReadVariant(ctx, r, &decoded)
				}),
			); err != nil {
				t.Fatal(err)
			}
			got, err := scalarValue(&decoded)
			if err != nil || !reflect.DeepEqual(got, tt.value) {
				t.Fatalf("decoded=%#v want=%#v error=%v", got, tt.value, err)
			}
			local, err := writeVariant(tt.value)
			if err != nil {
				t.Fatal(err)
			}
			wire, err = ndr.Marshal(
				ndr.MarshalNDRFunc(func(ctx context.Context, w ndr.Writer) error {
					return marshalWriteVariant(ctx, w, local)
				}),
			)
			if err != nil {
				t.Fatal(err)
			}
			if err := ndr.Unmarshal(wire, &decoded); err != nil {
				t.Fatal(err)
			}
			if got := decoded.VarUnion.GetValue(); !reflect.DeepEqual(got, tt.raw) {
				t.Fatalf("upstream decoded=%#v want=%#v", got, tt.raw)
			}
		})
	}
}

func TestVariantNestingRejected(t *testing.T) {
	value := Variant{Type: 3, Value: int32(1)}
	for range 40 {
		value = Variant{Type: 0x400c, Value: value}
	}
	if _, err := writeVariant(value); err == nil {
		t.Fatal("accepted excessive nesting")
	}
}

func TestWireNestingRejected(t *testing.T) {
	v := &oaut.Variant{
		Size:     3,
		VT:       3,
		VarUnion: &oaut.Variant_VarUnion{Value: &oaut.Variant_VarUnion_Long{Long: 1}},
	}
	for range 40 {
		v = &oaut.Variant{
			Size: 4,
			VT:   0x400c,
			VarUnion: &oaut.Variant_VarUnion{
				Value: &oaut.Variant_VarUnion_VariantPtr{VariantPtr: v},
			},
		}
	}
	// The unbounded generated encoder supplies an independently nested input.
	wire, err := ndr.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	var decoded oaut.Variant
	if err := ndr.Unmarshal(
		wire,
		ndr.UnmarshalNDRFunc(
			func(ctx context.Context, r ndr.Reader) error { return unmarshalReadVariant(ctx, r, &decoded) },
		),
	); err == nil {
		t.Fatal("accepted excessive wire nesting")
	}
	if _, err := ndr.Marshal(
		ndr.MarshalNDRFunc(
			func(ctx context.Context, w ndr.Writer) error { return marshalWriteVariant(ctx, w, v) },
		),
	); err == nil {
		t.Fatal("encoded excessive wire nesting")
	}
}
