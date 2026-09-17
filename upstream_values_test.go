package opcda

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/hex"
	"testing"

	"github.com/oiweiwei/go-msrpc/msrpc/dcom/oaut"
	"github.com/oiweiwei/go-msrpc/ndr"
)

// Exercise the published codecs directly so local adapters cannot hide regressions.
func TestUpstreamBSTREmptyAndNull(t *testing.T) {
	for _, tc := range []struct {
		name  string
		value *oaut.String
		wire  string
		empty bool
	}{
		{"empty_helper", oaut.EmptyString(), "000000000000000000000000", true},
		{"empty_flag", &oaut.String{IsEmpty: true}, "000000000000000000000000", true},
		{"null_helper", oaut.NullString(), "00000000ffffffff00000000", false},
		{"zero_value", &oaut.String{}, "00000000ffffffff00000000", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			want, err := hex.DecodeString(tc.wire)
			if err != nil {
				t.Fatal(err)
			}
			got, err := ndr.Marshal(tc.value)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(got, want) {
				t.Fatalf("wire=%x want=%x", got, want)
			}
			var decoded oaut.String
			if err := ndr.Unmarshal(want, &decoded); err != nil {
				t.Fatal(err)
			}
			if decoded.IsEmpty != tc.empty || decoded.Size != 0 || decoded.Data != "" {
				t.Fatalf("decoded=%+v, want empty=%t", decoded, tc.empty)
			}
			if decoded.BytesCount != tc.value.BytesCount {
				t.Fatalf("byte count=%x want=%x", decoded.BytesCount, tc.value.BytesCount)
			}
		})
	}
}

func TestBSTRUpstreamWriterMatchesLocal(t *testing.T) {
	for _, text := range []string{"", "a\x00", "\x00", "\u4e2d\x00", "\U0001f680\x00"} {
		v, err := writeVariant(text)
		if err != nil {
			t.Fatal(err)
		}
		local, err := ndr.Marshal(ndr.MarshalNDRFunc(func(ctx context.Context, w ndr.Writer) error {
			return marshalWriteVariant(ctx, w, v)
		}))
		if err != nil {
			t.Fatal(err)
		}
		upstream, err := ndr.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		if len(local) < 24 || len(upstream) < 24 ||
			binary.LittleEndian.Uint32(local[20:24]) == 0 ||
			binary.LittleEndian.Uint32(upstream[20:24]) == 0 {
			t.Fatalf("missing BSTR pointer: local=%x upstream=%x", local, upstream)
		}
		// Non-null pointer identifiers need not be identical across encoders.
		binary.LittleEndian.PutUint32(local[20:24], 1)
		binary.LittleEndian.PutUint32(upstream[20:24], 1)
		if !bytes.Equal(local, upstream) {
			t.Fatalf("%q: local=%x upstream=%x", text, local, upstream)
		}
		var decoded oaut.Variant
		if err := ndr.Unmarshal(
			local,
			ndr.UnmarshalNDRFunc(func(ctx context.Context, r ndr.Reader) error {
				return unmarshalReadVariant(ctx, r, &decoded)
			}),
		); err != nil {
			t.Fatal(err)
		}
		reencoded, err := ndr.Marshal(&decoded)
		if err != nil {
			t.Fatal(err)
		}
		binary.LittleEndian.PutUint32(reencoded[20:24], 1)
		if !bytes.Equal(local, reencoded) {
			t.Fatalf("%q: decoded BSTR re-encoded as %x, want %x", text, reencoded, local)
		}
	}
}

func TestBSTRWriterIgnoresEnclosingArraySize(t *testing.T) {
	want, err := hex.DecodeString("0200000004000000020000002d4e0000")
	if err != nil {
		t.Fatal(err)
	}
	got, err := ndr.Marshal(ndr.MarshalNDRFunc(func(ctx context.Context, w ndr.Writer) error {
		ctx = context.WithValue(ctx, ndr.SizeInfo, []uint64{9})
		return writeBSTR(ctx, w, "\u4e2d\x00")
	}))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("BSTR inherited enclosing array count: got=%x want=%x", got, want)
	}
}

func TestUpstreamBYREFNullClearsReusedUnion(t *testing.T) {
	for _, tc := range []struct {
		name  string
		kind  uint16
		union *oaut.Variant_VarUnion
	}{
		{"char", VTI1 | VTByRef, &oaut.Variant_VarUnion{Value: &oaut.Variant_VarUnion_CharPtr{CharPtr: 249}}},
		{"byte", VTUI1 | VTByRef, &oaut.Variant_VarUnion{Value: &oaut.Variant_VarUnion_BytePtr{BytePtr: 200}}},
		{"long", VTI4 | VTByRef, &oaut.Variant_VarUnion{Value: &oaut.Variant_VarUnion_LongPtr{LongPtr: -123}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// The union discriminant precedes the null unique-pointer marker.
			wire := make([]byte, 8)
			binary.LittleEndian.PutUint32(wire, uint32(tc.kind))
			err := ndr.Unmarshal(
				wire,
				ndr.UnmarshalNDRFunc(func(ctx context.Context, r ndr.Reader) error {
					if err := tc.union.UnmarshalUnionNDR(ctx, r, uint32(tc.kind)); err != nil {
						return err
					}
					return r.ReadDeferred()
				}),
			)
			if err != nil {
				t.Fatal(err)
			}
			if tc.union.Value != nil {
				t.Fatalf("null pointer retained stale value: %#v", tc.union.Value)
			}
		})
	}
}
