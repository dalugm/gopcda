package opcda

import (
	"context"
	"encoding/binary"
	"testing"

	"github.com/oiweiwei/go-msrpc/msrpc/dcom/oaut"
	"github.com/oiweiwei/go-msrpc/ndr"
	syncio "github.com/oiweiwei/go-opcda/opc/opcda/iopcsyncio/v0"
)

func TestBindingsPreserveEmptyBSTR(t *testing.T) {
	value, err := writeVariant("")
	if err != nil {
		t.Fatal(err)
	}
	request := &syncio.WriteRequest{
		This:       orpcThis(),
		Server:     []uint32{1},
		ItemValues: []*oaut.Variant{value},
	}
	wire, err := ndr.Marshal(ndr.MarshalNDRFunc(func(ctx context.Context, w ndr.Writer) error {
		return request.MarshalNDR(ctx, bindingsWriter{w})
	}))
	if err != nil {
		t.Fatal(err)
	}
	// ORPC (32), count + handle array (12), value array + pointer (8),
	// aligned VARIANT (offset 56), non-null BSTR pointer (offset 76).
	if len(wire) < 92 || binary.LittleEndian.Uint32(wire[76:]) == 0 ||
		binary.LittleEndian.Uint32(wire[84:]) != 0 {
		t.Fatalf("empty BSTR must retain a non-null, zero-byte payload: %x", wire)
	}
}

func TestBindingsReadLengthCountedBSTR(t *testing.T) {
	for _, text := range []string{"", "a\x00", "\x00", "a\x00b", "\U0001f680"} {
		value, err := writeVariant(text)
		if err != nil {
			t.Fatal(err)
		}
		variant, err := ndr.Marshal(
			ndr.MarshalNDRFunc(func(ctx context.Context, w ndr.Writer) error {
				return marshalWriteVariant(ctx, w, value)
			}),
		)
		if err != nil {
			t.Fatal(err)
		}
		wire := append(readFixture()[:40:40], variant...)
		for len(wire)%4 != 0 {
			wire = append(wire, 0)
		}
		for _, word := range []uint32{0x30000, 1, 0, 0} {
			wire = binary.LittleEndian.AppendUint32(wire, word)
		}
		var response readOneResponse
		if err := ndr.Unmarshal(wire, &response); err != nil {
			t.Fatalf("%q: %v", text, err)
		}
		result, err := response.result("A")
		if err != nil || result.Value != text || result.Quality != 0xc0 ||
			result.SourceTimestampMs != 0 {
			t.Fatalf("%q: result=%+v err=%v", text, result, err)
		}
		for n := range len(wire) {
			if err := ndr.Unmarshal(wire[:n], &readOneResponse{}); err == nil {
				t.Fatalf("%q: accepted truncation %d", text, n)
			}
		}
		broken := append([]byte(nil), wire...)
		binary.LittleEndian.PutUint32(broken[68:], 0xfffffffe)
		if err := ndr.Unmarshal(broken, &readOneResponse{}); err == nil {
			t.Fatal("accepted invalid BSTR byte count")
		}
	}
}

func TestBindingsAddUnicodeItem(t *testing.T) {
	const id = "A\U0001f680\u00e9"
	wire, err := ndr.Marshal(&addOneRequest{id: id})
	if err != nil {
		t.Fatal(err)
	}
	// Non-null empty access path precedes the item ID. The ID contains
	// four UTF-16 units plus its terminator, independent of UTF-8 byte length.
	if len(wire) != 106 || binary.LittleEndian.Uint32(wire[84:]) != 5 ||
		binary.LittleEndian.Uint32(wire[92:]) != 5 {
		t.Fatalf("invalid UTF-16 item counts: %x", wire)
	}
	for i, unit := range []uint16{'A', 0xd83d, 0xde80, 0x00e9, 0} {
		if binary.LittleEndian.Uint16(wire[96+2*i:]) != unit {
			t.Fatalf("invalid UTF-16 item body: %x", wire)
		}
	}
}

func TestGroupStatePreservesUnspecifiedFields(t *testing.T) {
	wire, err := ndr.Marshal(&groupStateRequest{rate: 1000, active: false, deadband: 0})
	if err != nil {
		t.Fatal(err)
	}
	// Set rate, active=false and deadband=0 explicitly. Leave time bias,
	// locale and client handle unchanged by sending null pointers.
	if len(wire) != 68 || binary.LittleEndian.Uint32(wire[32:]) == 0 ||
		binary.LittleEndian.Uint32(wire[36:]) != 1000 ||
		binary.LittleEndian.Uint32(wire[40:]) == 0 || binary.LittleEndian.Uint32(wire[44:]) != 0 ||
		binary.LittleEndian.Uint32(wire[48:]) != 0 || binary.LittleEndian.Uint32(wire[52:]) == 0 ||
		binary.LittleEndian.Uint32(wire[56:]) != 0 || binary.LittleEndian.Uint32(wire[60:]) != 0 ||
		binary.LittleEndian.Uint32(wire[64:]) != 0 {
		t.Fatalf("unexpected optional group state fields: %x", wire)
	}
}

func TestBindingsRejectMismatchedBlobCount(t *testing.T) {
	wire := make([]byte, 60)
	put := func(offset int, value uint32) { binary.LittleEndian.PutUint32(wire[offset:], value) }
	put(8, 0x20000)
	put(12, 1)
	put(16, 123)
	put(24, 1)
	put(28, 4) // OPCITEMRESULT.dwBlobSize
	put(32, 0x20004)
	put(36, 4) // conformant blob count
	put(40, 0x12345678)
	put(44, 0x20008)
	put(48, 1)
	if err := ndr.Unmarshal(wire, &addOneResponse{}); err != nil {
		t.Fatal(err)
	}
	put(36, 0)
	if err := ndr.Unmarshal(wire, &addOneResponse{}); err == nil {
		t.Fatal("accepted zero conformant blob count with nonzero dwBlobSize")
	}
}
