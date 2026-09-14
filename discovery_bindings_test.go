package opcda

import (
	"encoding/binary"
	"testing"
	"unicode/utf16"

	"github.com/oiweiwei/go-msrpc/ndr"
)

func TestProgIDRequestUnicodeWire(t *testing.T) {
	const progID = "Vendor.\u4f9b\u5e94.\U00010400.1"
	wantUnits := append(utf16.Encode([]rune(progID)), 0)

	wire, err := ndr.Marshal(&progIDRequest{progID: progID})
	if err != nil {
		t.Fatal(err)
	}
	body := wire[32:]
	if got := binary.LittleEndian.Uint32(body); got != uint32(len(wantUnits)) {
		t.Fatalf("maximum count = %d, want %d", got, len(wantUnits))
	}
	if got := binary.LittleEndian.Uint32(body[4:]); got != 0 {
		t.Fatalf("offset = %d, want 0", got)
	}
	if got := binary.LittleEndian.Uint32(body[8:]); got != uint32(len(wantUnits)) {
		t.Fatalf("actual count = %d, want %d", got, len(wantUnits))
	}
	for i, want := range wantUnits {
		if got := binary.LittleEndian.Uint16(body[12+i*2:]); got != want {
			t.Fatalf("UTF-16 unit %d = %#x, want %#x", i, got, want)
		}
	}
}
