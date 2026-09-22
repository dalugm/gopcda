package opcda

import (
	"context"
	"encoding/binary"
	"testing"
	"unicode/utf16"

	"github.com/oiweiwei/go-msrpc/dcerpc"
	"github.com/oiweiwei/go-msrpc/msrpc/dcom"
	"github.com/oiweiwei/go-msrpc/ndr"
)

func TestParseGUID(t *testing.T) {
	for _, s := range []string{"41EBD53D-36C4-4027-B2B4-09A6E4A362DD", "{41ebd53d-36c4-4027-b2b4-09a6e4a362dd}"} {
		g, err := parseGUID(s)
		if err != nil || g.Data1 != 0x41ebd53d || g.Data4[7] != 0xdd {
			t.Fatalf("parse %q: %v %v", s, g, err)
		}
	}
	for _, s := range []string{"", "bad", "00000000-0000-0000-0000-000000000000", "{41ebd53d-36c4-4027-b2b4-09a6e4a362dd"} {
		if _, err := parseGUID(s); err == nil {
			t.Errorf("accepted %q", s)
		}
	}
}

func TestTCPBindings(t *testing.T) {
	sa := []uint16{15}
	sa = append(sa, utf16.Encode([]rune("OS130[pipe]"))...)
	sa = append(sa, 0, 7)
	sa = append(sa, utf16.Encode([]rune("OS130[49672]"))...)
	sa = append(sa, 0, 0)
	b := &dcom.DualStringArray{StringArray: sa, SecurityOffset: uint16(len(sa))}
	if p, e := extractTCPPort(b); e != nil || p != "49672" {
		t.Fatalf("%q %v", p, e)
	}
	for _, b := range []*dcom.DualStringArray{nil, {}, {StringArray: []uint16{0, 7, 65, 91, 49, 93, 0}, SecurityOffset: 1}, {StringArray: []uint16{7, 65, 91, 48, 93, 0, 0}, SecurityOffset: 7}} {
		if p, e := extractTCPPort(b); e == nil {
			t.Fatalf("invalid binding yielded %q", p)
		}
	}
}

// NDR32 fixture from OPC DA IDL: ORPCTHAT, status pointer, status,
// deferred UTF-16 vendor string, HRESULT. All scalar offsets are explicit.
func statusFixture() []byte {
	b := make([]byte, 84)
	put := func(off int, v uint32) { binary.LittleEndian.PutUint32(b[off:], v) }
	put(8, 0x20000)
	for _, off := range []int{12, 20, 28} {
		binary.LittleEndian.PutUint64(b[off:], 116444736000000000)
	}
	put(36, 1)
	put(40, 2)
	put(44, 100)
	binary.LittleEndian.PutUint16(b[48:], 3)
	binary.LittleEndian.PutUint16(b[50:], 2)
	binary.LittleEndian.PutUint16(b[52:], 7)
	put(56, 0x20004)
	put(60, 3)
	put(68, 3)
	binary.LittleEndian.PutUint16(b[72:], '\u03a9')
	binary.LittleEndian.PutUint16(b[74:], '\u00e9')
	return b
}

func TestStatusResponse(t *testing.T) {
	b := statusFixture()
	var r getStatusResp
	if err := ndr.Unmarshal(b, &r); err != nil {
		t.Fatal(err)
	}
	s, err := r.serverStatus()
	if err != nil {
		t.Fatal(err)
	}
	if s.VendorInfo != "\u03a9\u00e9" || s.ProductVersion != "3.2.7" || s.State != 1 ||
		s.StartTime.Unix() != 0 {
		t.Fatalf("%+v", s)
	}
	for n := range b {
		var r getStatusResp
		if err := ndr.Unmarshal(b[:n], &r); err == nil {
			t.Fatalf("accepted truncated response %d", n)
		}
	}
	fail := make([]byte, 16)
	binary.LittleEndian.PutUint32(fail[12:], 0x80070005)
	if err := ndr.Unmarshal(fail, &r); err != nil {
		t.Fatal(err)
	}
	if _, err := r.serverStatus(); err == nil {
		t.Fatal("accepted failed HRESULT")
	}
	var empty getStatusResp
	if err := ndr.Unmarshal(make([]byte, 16), &empty); err != nil {
		t.Fatal(err)
	}
	if _, err := empty.serverStatus(); err == nil {
		t.Fatal("accepted nil status")
	}
}

// Embedding Conn leaves AlterContext unavailable: invoking an already-bound
// IOPCServer must not try to rebind with the caller's unauthenticated context.
type statusConn struct {
	dcerpc.Conn
	opnum int
}

func (c *statusConn) Invoke(
	ctx context.Context,
	op dcerpc.Operation,
	opts ...dcerpc.CallOption,
) error {
	c.opnum = op.OpNum()
	return ndr.Unmarshal(statusFixture(), op.(*opcOp).resp)
}

func TestGetStatusUsesBoundInterfaceAndWireOpnum(t *testing.T) {
	conn := &statusConn{}
	c := &dcomConn{serverConn: conn, serverIPID: &dcom.IPID{}}
	s, err := c.getServerStatus(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if conn.opnum != 6 || s.VendorInfo != "\u03a9\u00e9" {
		t.Fatalf("opnum=%d status=%+v", conn.opnum, s)
	}
}
