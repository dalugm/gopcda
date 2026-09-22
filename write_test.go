package opcda

import (
	"context"
	"encoding/binary"
	"errors"
	"io"
	"math"
	"testing"

	"github.com/oiweiwei/go-msrpc/dcerpc"
	"github.com/oiweiwei/go-msrpc/msrpc/dcom"
	"github.com/oiweiwei/go-msrpc/ndr"
)

func TestFloatWriteWire(t *testing.T) {
	for _, v := range []any{float32(12.5), float64(-12.5)} {
		variant, err := writeVariant(v)
		if err != nil {
			t.Fatal(err)
		}
		b, err := ndr.Marshal(&writeOneRequest{handle: 123, value: variant})
		if err != nil {
			t.Fatal(err)
		}
		for _, off := range []int{32, 36, 44} {
			if binary.LittleEndian.Uint32(b[off:]) != 1 {
				t.Fatalf("count at %d: %x", off, b)
			}
		}
		if binary.LittleEndian.Uint32(b[40:]) != 123 || binary.LittleEndian.Uint32(b[48:]) == 0 {
			t.Fatalf("handle or variant ref: %x", b)
		}
		switch v.(type) {
		case float32:
			if len(b) != 80 || binary.LittleEndian.Uint16(b[64:]) != 4 ||
				binary.LittleEndian.Uint32(b[72:]) != 4 ||
				math.Float32frombits(binary.LittleEndian.Uint32(b[76:])) != 12.5 {
				t.Fatalf("%x", b)
			}
		case float64:
			if len(b) != 88 || binary.LittleEndian.Uint16(b[64:]) != 5 ||
				binary.LittleEndian.Uint32(b[72:]) != 5 ||
				math.Float64frombits(binary.LittleEndian.Uint64(b[80:])) != -12.5 {
				t.Fatalf("%x", b)
			}
		}
	}
}

func TestWriteResponse(t *testing.T) {
	b := make([]byte, 24)
	binary.LittleEndian.PutUint32(b[8:], 0x20000)
	binary.LittleEndian.PutUint32(b[12:], 1)
	var r writeOneResponse
	if err := ndr.Unmarshal(b, &r); err != nil {
		t.Fatal(err)
	}
	if err := r.check(); err != nil {
		t.Fatal(err)
	}
	for n := range b {
		var r writeOneResponse
		if err := ndr.Unmarshal(b[:n], &r); err == nil {
			t.Fatalf("truncation %d", n)
		}
	}
	binary.LittleEndian.PutUint32(b[16:], 0xc0040006)
	binary.LittleEndian.PutUint32(b[20:], 1)
	if err := ndr.Unmarshal(b, &r); err != nil {
		t.Fatal(err)
	}
	if err := r.check(); err == nil {
		t.Fatal("accepted per-item failure")
	}
	if err := (&writeOneResponse{}).check(); err == nil {
		t.Fatal("accepted missing result")
	}
}

func TestWriteValidation(t *testing.T) {
	for _, v := range []any{1, []byte("12.5"), nil, math.NaN(), math.Inf(1), float32(math.Inf(-1))} {
		if _, err := writeVariant(v); err == nil {
			t.Fatalf("accepted %v", v)
		}
	}
	s := &Server{ctx: context.Background()}
	for _, id := range []string{"", "a\x00b", "Pump.\xff", "Pump.\xc0\xaf"} {
		if err := s.WriteItem(context.Background(), id, float32(1)); err == nil {
			t.Fatal("accepted invalid ID")
		}
	}
}

type writeConn struct {
	dcerpc.Conn
	calls     int
	opnum     int
	handle    uint32
	itemError int32
	transport error
}

func (c *writeConn) Invoke(
	ctx context.Context,
	op dcerpc.Operation,
	opts ...dcerpc.CallOption,
) error {
	c.calls++
	c.opnum = op.OpNum()
	c.handle = op.(*opcOp).req.(*writeOneRequest).handle
	if c.transport != nil {
		return c.transport
	}
	b := make([]byte, 24)
	binary.LittleEndian.PutUint32(b[8:], 0x20000)
	binary.LittleEndian.PutUint32(b[12:], 1)
	binary.LittleEndian.PutUint32(b[16:], uint32(c.itemError))
	if c.itemError < 0 {
		binary.LittleEndian.PutUint32(b[20:], 1)
	}
	return ndr.Unmarshal(b, op.(*opcOp).resp)
}

func TestSingleWriteUsesServerOutcomeDespiteAccessRightsHint(t *testing.T) {
	for _, test := range []struct {
		name      string
		rights    uint32
		itemError int32
		transport error
	}{
		{name: "accepted despite hint", rights: 1},
		{name: "accepted with writable hint", rights: 3},
		{name: "server rejects bad rights", rights: 1, itemError: -1073479674},
		{name: "transport failure", rights: 1, transport: io.ErrUnexpectedEOF},
	} {
		t.Run(test.name, func(t *testing.T) {
			conn := &writeConn{itemError: test.itemError, transport: test.transport}
			v, err := writeVariant(float32(1))
			if err != nil {
				t.Fatal(err)
			}
			err = writeSingleValue(
				t.Context(),
				conn,
				&dcom.IPID{},
				&addOneResponse{rights: test.rights, handle: 123},
				v,
			)
			switch {
			case test.transport != nil:
				if !errors.Is(err, test.transport) || !errors.Is(err, ErrWriteOutcomeUnknown) {
					t.Errorf("lost uncertain write outcome: %v", err)
				}
			case test.itemError < 0:
				var hr *HRESULTError
				if !errors.As(err, &hr) || hr.Code != 0xc0040006 || hr.Operation != "Write" {
					t.Errorf("lost server bad-rights HRESULT: %v", err)
				}
			case err != nil:
				t.Errorf("server-accepted write failed: %v", err)
			}
			if errors.Is(err, ErrWriteNotAttempted) {
				t.Errorf("explicit write was classified as not attempted: %v", err)
			}
			if conn.calls != 1 || conn.opnum != 4 || conn.handle != 123 {
				t.Errorf("explicit write must send handle 123 once at opnum 4: %+v", conn)
			}
		})
	}
}
