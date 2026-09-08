package opcda

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"reflect"
	"testing"

	"github.com/oiweiwei/go-msrpc/ndr"
)

func TestBrowseRequestWire(t *testing.T) {
	b, err := ndr.Marshal(&browseIDsRequest{})
	if err != nil {
		t.Fatal(err)
	}
	want, _ := hex.DecodeString("030000000100000000000000010000000000000000000000")
	if len(b) != 32+len(want) || !bytes.Equal(b[32:], want) {
		t.Fatalf("unexpected request: %x", b)
	}
}

// IEnumString::RemoteNext uses a conformant-varying pointer array:
// max_count, offset, actual_count, referents, deferred strings, fetched, HRESULT.
func nextFixture() []byte {
	b := make([]byte, 68)
	put := func(i int, v uint32) { binary.LittleEndian.PutUint32(b[i:], v) }
	put(8, 5)
	put(16, 2)
	put(20, 0x20000)
	put(24, 0x20004)
	put(28, 2)
	put(36, 2)
	put(40, 'A')
	put(44, 2)
	put(52, 2)
	put(56, 'B')
	put(60, 2)
	put(64, 1)
	return b
}

func TestEnumFinalPartialBatch(t *testing.T) {
	b := nextFixture()
	r := enumNextResponse{requested: 5}
	if err := ndr.Unmarshal(b, &r); err != nil {
		t.Fatal(err)
	}
	if r.hresult != 1 || r.fetched != 2 || !reflect.DeepEqual(r.values, []string{"A", "B"}) {
		t.Fatalf("%+v", r)
	}
	for n := 0; n < len(b); n++ {
		r := enumNextResponse{requested: 5}
		if err := ndr.Unmarshal(b[:n], &r); err == nil {
			t.Fatalf("accepted truncation %d", n)
		}
	}
	for _, off := range []int{8, 12, 16, 60} {
		b := nextFixture()
		binary.LittleEndian.PutUint32(b[off:], 6)
		r := enumNextResponse{requested: 5}
		if err := ndr.Unmarshal(b, &r); err == nil {
			t.Fatalf("accepted invalid count at %d", off)
		}
	}
}

func TestCollectItemIDs(t *testing.T) {
	n := 0
	ids, err := collectItemIDs(
		context.Background(),
		func(context.Context) (*enumNextResponse, error) {
			n++
			if n == 1 {
				return &enumNextResponse{values: []string{"B", "A"}, fetched: 2}, nil
			}
			return &enumNextResponse{values: []string{"A", "C"}, fetched: 2, hresult: 1}, nil
		},
	)
	if err != nil || !reflect.DeepEqual(ids, []string{"A", "B", "C"}) || n != 2 {
		t.Fatalf("%v %v %d", ids, err, n)
	}
	for _, r := range []*enumNextResponse{{}, {hresult: -1}, {values: []string{""}, fetched: 1, hresult: 1}} {
		if _, err := collectItemIDs(
			context.Background(),
			func(context.Context) (*enumNextResponse, error) { return r, nil },
		); err == nil {
			t.Fatalf("accepted %+v", r)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := collectItemIDs(
		ctx,
		func(context.Context) (*enumNextResponse, error) { t.Fatal("called after cancel"); return nil, nil },
	); !errors.Is(
		err,
		context.Canceled,
	) {
		t.Fatal(err)
	}
}

func TestBrowseEnumeratorReference(t *testing.T) {
	// OBJREF_STANDARD for IEnumString, with an empty resolver DUALSTRINGARRAY.
	obj := make([]byte, 72)
	copy(obj, "MEOW")
	binary.LittleEndian.PutUint32(obj[4:], 1)
	copy(obj[8:], enumStringIID.EncodeBinary())
	binary.LittleEndian.PutUint32(obj[28:], 1)
	binary.LittleEndian.PutUint64(obj[32:], 7)
	binary.LittleEndian.PutUint64(obj[40:], 9)
	obj[48] = 1
	binary.LittleEndian.PutUint16(obj[64:], 2)
	binary.LittleEndian.PutUint16(obj[66:], 1)
	b := make([]byte, 96)
	binary.LittleEndian.PutUint32(b[8:], 0x20000)
	binary.LittleEndian.PutUint32(b[12:], 72)
	binary.LittleEndian.PutUint32(b[16:], 72)
	copy(b[20:], obj)
	var r browseIDsResponse
	if err := ndr.Unmarshal(b, &r); err != nil {
		t.Fatal(err)
	}
	std, err := decodeStandardReference(r.pointer, enumStringIID)
	if err != nil || std.OXID != 7 || std.IPID.Data1 != 1 {
		t.Fatalf("%+v %v", std, err)
	}
	if _, err := decodeStandardReference(r.pointer, browseIID); err == nil {
		t.Fatal("accepted incorrect IID")
	}
	for n := 0; n < len(b); n++ {
		var r browseIDsResponse
		if err := ndr.Unmarshal(b[:n], &r); err == nil {
			t.Fatalf("accepted truncated pointer response %d", n)
		}
	}
	r.pointer.Data[0] = 0
	if _, err := decodeStandardReference(r.pointer, enumStringIID); err == nil {
		t.Fatal("accepted invalid OBJREF")
	}
}

type browseConnection struct {
	connection
	browse func(context.Context) ([]string, error)
}

func (c *browseConnection) browseItemIDs(
	ctx context.Context,
) ([]string, error) {
	return c.browse(ctx)
}

func TestBrowseServerDisconnectCancels(t *testing.T) {
	session, disconnect := context.WithCancel(context.Background())
	s := &Server{
		ctx: session,
		conn: &browseConnection{
			browse: func(ctx context.Context) ([]string, error) { disconnect(); <-ctx.Done(); return nil, ctx.Err() },
		},
	}
	if _, err := s.BrowseItemIDs(context.Background()); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	s.conn = &browseConnection{
		browse: func(context.Context) ([]string, error) { t.Fatal("called after disconnect"); return nil, nil },
	}
	if _, err := s.BrowseItemIDs(context.Background()); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}
