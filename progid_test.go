package opcda

import (
	"context"
	"encoding/binary"
	"errors"
	"testing"
	"unicode/utf16"

	"github.com/oiweiwei/go-msrpc/ndr"
)

func TestProgIDWire(t *testing.T) {
	b, err := ndr.Marshal(&progIDRequest{progID: "Vendor.Server.1"})
	if err != nil {
		t.Fatal(err)
	}
	units := utf16.Encode([]rune("Vendor.Server.1\x00"))
	if binary.LittleEndian.Uint32(b[32:]) != uint32(len(units)) ||
		binary.LittleEndian.Uint32(b[36:]) != 0 ||
		binary.LittleEndian.Uint32(b[40:]) != uint32(len(units)) {
		t.Fatalf("string header: %x", b)
	}
	for i, u := range units {
		if binary.LittleEndian.Uint16(b[44+i*2:]) != u {
			t.Fatalf("string data: %x", b)
		}
	}
	// ORPCTHAT, inline CLSID (little-endian GUID fields), HRESULT.
	reply := []byte{
		0,
		0,
		0,
		0,
		0,
		0,
		0,
		0,
		0x51,
		0x6d,
		0x48,
		0x13,
		0x21,
		0x48,
		0xd2,
		0x11,
		0xa4,
		0x94,
		0x3c,
		0xb3,
		6,
		0xc1,
		0,
		0,
		0,
		0,
		0,
		0,
	}
	var r progIDResponse
	if err := ndr.Unmarshal(reply, &r); err != nil {
		t.Fatal(err)
	}
	if id, err := r.result("Vendor.Server.1"); err != nil || id != opcEnumCLSID {
		t.Fatalf("%s %v", id, err)
	}
	for n := range reply {
		if err := ndr.Unmarshal(reply[:n], &r); err == nil {
			t.Fatalf("accepted truncation %d", n)
		}
	}
	binary.LittleEndian.PutUint32(reply[24:], 0x80040154)
	if err := ndr.Unmarshal(reply, &r); err != nil {
		t.Fatal(err)
	}
	_, err = r.result("Vendor.Server.1")
	var hr *HRESULTError
	if !errors.As(err, &hr) || hr.Code != 0x80040154 {
		t.Fatalf("lost HRESULT: %v", err)
	}
	clear(reply)
	if err := ndr.Unmarshal(reply, &r); err != nil {
		t.Fatal(err)
	}
	if _, err := r.result("missing"); err == nil {
		t.Fatal("accepted zero CLSID")
	}
}

type fakeProgIDConnection struct {
	lookupErr, closeErr error
	closed              bool
	cancel              context.CancelFunc
}

func (f *fakeProgIDConnection) lookupProgID(context.Context, string) (string, error) {
	if f.cancel != nil {
		f.cancel()
	}
	return opcEnumCLSID, f.lookupErr
}

func (f *fakeProgIDConnection) closeResolver(ctx context.Context) error {
	f.closed = ctx.Err() == nil
	return f.closeErr
}

func TestResolveProgIDLifecycle(t *testing.T) {
	failure := errors.New("failure")
	for _, tc := range []struct{ lookupErr, closeErr error }{{}, {failure, nil}, {nil, failure}, {failure, failure}} {
		ctx, cancel := context.WithCancel(context.Background())
		f := &fakeProgIDConnection{lookupErr: tc.lookupErr, closeErr: tc.closeErr, cancel: cancel}
		id, err := resolveProgID(
			ctx,
			ServerConfig{Host: "target", CLSID: "ignored", ProgID: "ignored"},
			"Vendor.Server.1",
			func(_ context.Context, cfg ServerConfig) (progIDConnection, error) {
				if cfg.Host != "target" || cfg.CLSID != opcEnumCLSID || cfg.ProgID != "" {
					t.Fatalf("activation config: %v", cfg)
				}
				return f, nil
			},
		)
		cancel()
		if !f.closed {
			t.Fatal("cleanup used canceled context")
		}
		if tc.lookupErr != nil || tc.closeErr != nil {
			if !errors.Is(err, failure) || id != "" {
				t.Fatalf("%q %v", id, err)
			}
		} else if err != nil || id != opcEnumCLSID {
			t.Fatalf("%q %v", id, err)
		}
	}
	for _, id := range []string{"", " ", "a\x00b", "\xff"} {
		_, err := resolveProgID(
			context.Background(),
			ServerConfig{},
			id,
			func(context.Context, ServerConfig) (progIDConnection, error) {
				t.Fatal("invalid ProgID reached network")
				return nil, nil
			},
		)
		if err == nil {
			t.Fatal("accepted invalid ProgID")
		}
	}
}

func TestServerIdentificationPrecedence(t *testing.T) {
	calls := 0
	resolve := func(_ context.Context, _ ServerConfig, name string) (string, error) {
		calls++
		if name != "Vendor.Server.1" {
			t.Fatal(name)
		}
		return opcEnumCLSID, nil
	}
	cfg, err := resolveServerConfig(
		context.Background(),
		ServerConfig{CLSID: "explicit", ProgID: "ignored"},
		resolve,
	)
	if err != nil || cfg.CLSID != "explicit" || calls != 0 {
		t.Fatalf("explicit CLSID resolved: %v %v", cfg, err)
	}
	cfg, err = resolveServerConfig(
		context.Background(),
		ServerConfig{ProgID: "Vendor.Server.1"},
		resolve,
	)
	if err != nil || cfg.CLSID != opcEnumCLSID || calls != 1 {
		t.Fatalf("ProgID not resolved: %v %v", cfg, err)
	}
	if _, err := resolveServerConfig(
		context.Background(),
		ServerConfig{},
		resolve,
	); err == nil ||
		calls != 1 {
		t.Fatal("missing identifier reached resolver")
	}
}
