package opcda

import (
	"context"
	"encoding/binary"
	"math"
	"testing"

	"github.com/oiweiwei/go-msrpc/ndr"
)

func readFixture() []byte {
	b := make([]byte, 80)
	put := func(i int, v uint32) { binary.LittleEndian.PutUint32(b[i:], v) }
	put(8, 0x20000)
	put(12, 1)
	put(16, 1)
	binary.LittleEndian.PutUint64(b[20:], 116444736000000000)
	binary.LittleEndian.PutUint16(b[28:], 0xc0)
	put(32, 0x20004)
	put(40, 3)
	binary.LittleEndian.PutUint16(b[48:], 4)
	put(56, 4)
	put(60, math.Float32bits(12.5))
	put(64, 0x20008)
	put(68, 1)
	return b
}

func TestReadFloatQualityAndTime(t *testing.T) {
	b := readFixture()
	var r readOneResponse
	if err := ndr.Unmarshal(b, &r); err != nil {
		t.Fatal(err)
	}
	result, err := r.result("test.VALUE")
	if err != nil {
		t.Fatal(err)
	}
	if result.Value != float32(12.5) || result.Quality != 0xc0 || result.SourceTimestampMs != 0 {
		t.Fatalf("%+v", result)
	}
	binary.LittleEndian.PutUint16(b[28:], 0x18)
	if err := ndr.Unmarshal(b, &r); err != nil {
		t.Fatal(err)
	}
	result, err = r.result("test.VALUE")
	if err != nil || QualityIsGood(result.Quality) {
		t.Fatalf("%+v %v", result, err)
	}
	for n := 0; n < len(b); n++ {
		var r readOneResponse
		if err := ndr.Unmarshal(b[:n], &r); err == nil {
			t.Fatalf("accepted truncation %d", n)
		}
	}
	binary.LittleEndian.PutUint32(b[72:], 0xc0040007)
	binary.LittleEndian.PutUint32(b[76:], 1)
	if err := ndr.Unmarshal(b, &r); err != nil {
		t.Fatal(err)
	}
	if _, err := r.result("missing"); err == nil {
		t.Fatal("accepted per-item failure")
	}
}

func TestDeviceReadRequest(t *testing.T) {
	b, err := ndr.Marshal(&readOneRequest{handle: 0x12345678})
	if err != nil {
		t.Fatal(err)
	}
	if len(b) != 48 || binary.LittleEndian.Uint16(b[32:]) != 2 ||
		binary.LittleEndian.Uint32(b[36:]) != 1 ||
		binary.LittleEndian.Uint32(b[40:]) != 1 ||
		binary.LittleEndian.Uint32(b[44:]) != 0x12345678 {
		t.Fatalf("%x", b)
	}
}

func TestReadRejectsInvalidInput(t *testing.T) {
	s := &Server{ctx: context.Background()}
	for _, id := range []string{"", "a\x00b"} {
		if _, err := s.ReadItem(context.Background(), id); err == nil {
			t.Fatal("accepted invalid ID")
		}
	}
}

func TestAddGroupReferenceStringAndResponse(t *testing.T) {
	b, err := ndr.Marshal(&addGroupReq{Name: "", RIID: iopcItemMgtIID})
	if err != nil {
		t.Fatal(err)
	}
	// Empty top-level ref string starts directly with max_count, offset, count.
	if binary.LittleEndian.Uint32(b[32:]) != 1 || binary.LittleEndian.Uint32(b[36:]) != 0 ||
		binary.LittleEndian.Uint32(b[40:]) != 1 {
		t.Fatalf("%x", b)
	}
	response := make([]byte, 24)
	binary.LittleEndian.PutUint32(response[8:], 123)
	binary.LittleEndian.PutUint32(response[12:], 1000)
	binary.LittleEndian.PutUint32(response[20:], 0x80070005)
	var r addGroupResp
	if err := ndr.Unmarshal(response, &r); err != nil {
		t.Fatal(err)
	}
	if r.ServerHandle != 123 || r.GroupIface != nil || uint32(r.Return) != 0x80070005 {
		t.Fatalf("%+v", r)
	}
}

func TestAddOneItemRequest(t *testing.T) {
	b, err := ndr.Marshal(&addOneRequest{id: "A"})
	if err != nil {
		t.Fatal(err)
	}
	// ORPC(32), count(4), conformant count(4), OPCITEMDEF(28), deferred strings.
	if binary.LittleEndian.Uint32(b[32:]) != 1 || binary.LittleEndian.Uint32(b[36:]) != 1 ||
		binary.LittleEndian.Uint32(b[48:]) != 1 ||
		binary.LittleEndian.Uint32(b[52:]) != 1 {
		t.Fatalf("%x", b)
	}
	if binary.LittleEndian.Uint32(b[56:]) != 0 || binary.LittleEndian.Uint32(b[60:]) != 0 ||
		binary.LittleEndian.Uint16(b[64:]) != 0 {
		t.Fatalf("unexpected blob or requested type: %x", b)
	}
	if binary.LittleEndian.Uint32(b[68:]) != 1 || binary.LittleEndian.Uint32(b[84:]) != 2 ||
		binary.LittleEndian.Uint16(b[96:]) != 'A' {
		t.Fatalf("incorrect deferred strings: %x", b)
	}
}

func TestAddOneItemResultAndFailure(t *testing.T) {
	b := make([]byte, 52)
	put := func(i int, v uint32) { binary.LittleEndian.PutUint32(b[i:], v) }
	put(8, 0x20000)
	put(12, 1)
	put(16, 123)
	binary.LittleEndian.PutUint16(b[20:], 4)
	put(24, 1)
	put(36, 0x20004)
	put(40, 1)
	var r addOneResponse
	if err := ndr.Unmarshal(b, &r); err != nil {
		t.Fatal(err)
	}
	if err := r.check(); err != nil {
		t.Fatal(err)
	}
	if r.handle != 123 || r.canonical != 4 || r.rights != 1 {
		t.Fatalf("%+v", r)
	}
	for n := 0; n < len(b); n++ {
		var r addOneResponse
		if err := ndr.Unmarshal(b[:n], &r); err == nil {
			t.Fatalf("accepted truncation %d", n)
		}
	}
	put(44, 0xc0040007)
	put(48, 1)
	if err := ndr.Unmarshal(b, &r); err != nil {
		t.Fatal(err)
	}
	if err := r.check(); err == nil {
		t.Fatal("accepted invalid item ID")
	}
	put(40, 2)
	if err := ndr.Unmarshal(b, &r); err == nil {
		t.Fatal("accepted wrong result count")
	}
}

func TestReadScalarTypes(t *testing.T) {
	for _, tt := range []struct {
		vt   uint16
		bits uint32
		want any
	}{{3, 0xfffffffe, int32(-2)}, {11, 0xffff, true}, {11, 0, false}, {19, 0xffffffff, uint32(0xffffffff)}} {
		b := readFixture()
		binary.LittleEndian.PutUint16(b[48:], tt.vt)
		binary.LittleEndian.PutUint32(b[56:], uint32(tt.vt))
		binary.LittleEndian.PutUint32(b[60:], tt.bits)
		var r readOneResponse
		if err := ndr.Unmarshal(b, &r); err != nil {
			t.Fatal(err)
		}
		v, err := r.result("A")
		if err != nil || v.Value != tt.want {
			t.Fatalf("vt=%d value=%+v error=%v", tt.vt, v, err)
		}
	}
	r := readOneResponse{hasError: true}
	if _, err := r.result("A"); err == nil {
		t.Fatal("accepted missing state")
	}
}
