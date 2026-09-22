package opcda

import (
	"context"
	"testing"
	"time"

	"github.com/oiweiwei/go-msrpc/dcerpc"
	exporter "github.com/oiweiwei/go-msrpc/msrpc/dcom/iobjectexporter/v0"
)

type pingClient struct {
	exporter.ObjectExporterClient
	calls []*exporter.ComplexPingRequest
}

func (p *pingClient) ComplexPing(
	ctx context.Context,
	r *exporter.ComplexPingRequest,
	opts ...dcerpc.CallOption,
) (*exporter.ComplexPingResponse, error) {
	p.calls = append(p.calls, r)
	return &exporter.ComplexPingResponse{SetID: 123}, nil
}

func TestPingSetReferenceCounts(t *testing.T) {
	fake := &pingClient{}
	p := newObjectPinger()
	p.client = fake
	c := &dcomConn{pinger: p}
	for range 2 {
		if err := p.add(context.Background(), 7); err != nil {
			t.Fatal(err)
		}
	}
	if len(fake.calls) != 1 || p.objects[7] != 2 || fake.calls[0].SequenceNum != 1 {
		t.Fatal("duplicate OID was registered twice")
	}
	if err := c.releaseObject(context.Background(), 7); err != nil {
		t.Fatal(err)
	}
	if len(fake.calls) != 1 {
		t.Fatal("OID removed while still referenced")
	}
	if err := c.releaseObject(context.Background(), 7); err != nil {
		t.Fatal(err)
	}
	if len(fake.calls) != 2 || fake.calls[1].DeleteFromSet[0] != 7 || fake.calls[1].SetID != 123 ||
		fake.calls[1].SequenceNum != 2 {
		t.Fatal("wrong ping set deletion")
	}
}

func TestHealthCheckDoesNotWaitForPingNetworkLocks(t *testing.T) {
	p := newObjectPinger()
	c := &dcomConn{pinger: p}
	c.pingHealth.Store(p)
	if err := p.acquire(t.Context()); err != nil {
		t.Fatal(err)
	}
	c.pingMu.Lock()
	defer p.release()
	defer c.pingMu.Unlock()
	done := make(chan error, 1)
	go func() { done <- c.keepaliveError() }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("health check blocked behind a network lock")
	}
}
