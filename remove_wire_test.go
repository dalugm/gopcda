package opcda

import (
	"encoding/binary"
	"testing"

	"github.com/oiweiwei/go-msrpc/ndr"
)

func TestRemoveItemsWire(t *testing.T) {
	wire, err := ndr.Marshal(&batchRemoveRequest{handles: []uint32{7, 9}})
	if err != nil {
		t.Fatal(err)
	}
	if len(wire) != 48 || binary.LittleEndian.Uint32(wire[32:]) != 2 ||
		binary.LittleEndian.Uint32(wire[36:]) != 2 ||
		binary.LittleEndian.Uint32(wire[40:]) != 7 ||
		binary.LittleEndian.Uint32(wire[44:]) != 9 {
		t.Fatalf("request = %x", wire)
	}
	response := make([]byte, 28)
	binary.LittleEndian.PutUint32(response[8:], 0x20000)
	binary.LittleEndian.PutUint32(response[12:], 2)
	binary.LittleEndian.PutUint32(response[20:], 0xc0040001)
	binary.LittleEndian.PutUint32(response[24:], 1)
	r := batchRemoveResponse{count: 2}
	if err := ndr.Unmarshal(response, &r); err != nil {
		t.Fatal(err)
	}
	if r.hresult != 1 || len(r.errors) != 2 || r.errors[0] != 0 || r.errors[1] >= 0 {
		t.Fatalf("response = %+v", r)
	}
	for n := range len(response) {
		if err := ndr.Unmarshal(response[:n], &batchRemoveResponse{count: 2}); err == nil {
			t.Fatalf("accepted truncation %d", n)
		}
	}
	binary.LittleEndian.PutUint32(response[12:], 3)
	if err := ndr.Unmarshal(response, &batchRemoveResponse{count: 2}); err == nil {
		t.Fatal("accepted wrong array count")
	}
}
