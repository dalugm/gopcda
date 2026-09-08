package opcda

import (
	"context"
	"encoding/binary"
	"testing"

	"github.com/oiweiwei/go-msrpc/msrpc/dcom/oaut"
	"github.com/oiweiwei/go-msrpc/ndr"
)

func TestBatchReadDeferredVariants(t *testing.T) {
	// ORPC, state pointer/count, TWO inline states, then deferred R4 variants,
	// then separate HRESULT array. Interleaving each variant with its state is wrong.
	b := make([]byte, 124)
	put := func(i int, v uint32) { binary.LittleEndian.PutUint32(b[i:], v) }
	put(8, 0x20000)
	put(12, 2)
	for i := range 2 {
		off := 16 + i*20
		put(off, uint32(i+1))
		binary.LittleEndian.PutUint64(b[off+4:], 116444736000000000)
		binary.LittleEndian.PutUint16(b[off+12:], 0xc0)
		put(off+16, uint32(0x20004+i*4))
	}
	for i := range 2 {
		off := 56 + i*24
		put(off, 3)
		binary.LittleEndian.PutUint16(b[off+8:], 4)
		put(off+16, 4)
		put(off+20, 0x41480000)
	}
	put(104, 0x2000c)
	put(108, 2)
	put(116, 0xc0040007)
	put(120, 1)
	r := batchReadResponse{count: 2}
	if err := ndr.Unmarshal(b, &r); err != nil {
		t.Fatal(err)
	}
	out, err := r.results([]string{"A", "B"})
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 2 || out[0].Value != float32(12.5) || out[0].Error != nil ||
		out[1].Error == nil {
		t.Fatalf("%+v", out)
	}
	for n := 0; n < len(b); n++ {
		r := batchReadResponse{count: 2}
		if err := ndr.Unmarshal(b[:n], &r); err == nil {
			t.Fatalf("accepted truncation %d", n)
		}
	}
}

func TestBatchWriteArrays(t *testing.T) {
	a, _ := writeVariant(float32(1))
	b, _ := writeVariant(float64(2))
	wire, err := ndr.Marshal(
		&batchWriteRequest{handles: []uint32{7, 9}, values: []*oaut.Variant{a, b}},
	)
	if err != nil {
		t.Fatal(err)
	}
	if binary.LittleEndian.Uint32(wire[32:]) != 2 || binary.LittleEndian.Uint32(wire[36:]) != 2 ||
		binary.LittleEndian.Uint32(wire[40:]) != 7 ||
		binary.LittleEndian.Uint32(wire[44:]) != 9 ||
		binary.LittleEndian.Uint32(wire[48:]) != 2 {
		t.Fatalf("%x", wire)
	}
}

func TestGroupWaitCancellation(t *testing.T) {
	g := newPersistentGroup()
	if err := g.acquire(context.Background()); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := g.acquire(ctx); err == nil {
		t.Fatal("acquired busy group after cancel")
	}
	g.release()
}
