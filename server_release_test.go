package opcda

import (
	"context"
	"errors"
	"io"
	"slices"
	"testing"
	"time"

	"github.com/oiweiwei/go-msrpc/dcerpc"
	"github.com/oiweiwei/go-msrpc/msrpc/dcom"
	rem "github.com/oiweiwei/go-msrpc/msrpc/dcom/iremunknown/v0"
	"github.com/oiweiwei/go-msrpc/ndr"
)

type serverReleaseConn struct {
	dcerpc.Conn
	events     []string
	request    rem.RemoteReleaseRequest
	callIPID   *dcom.IPID
	deadline   time.Time
	bindErr    error
	releaseErr error
	closeErr   error
}

func (c *serverReleaseConn) Bind(ctx context.Context, opts ...dcerpc.Option) (dcerpc.Conn, error) {
	c.events = append(c.events, "bind")
	parsed, err := dcerpc.ParseOptions(ctx, opts...)
	if err != nil {
		return nil, err
	}
	if len(parsed.AbstractSyntaxes) != 1 ||
		!parsed.AbstractSyntaxes[0].IfUUID.Equals(rem.RemoteUnknownSyntaxV0_0.IfUUID) {
		return nil, errors.New("release must bind the IRemUnknown presentation context")
	}
	if parsed.Security.Level != dcerpc.AuthLevelPktPrivacy {
		return nil, errors.New("release must retain RPC packet privacy")
	}
	if c.bindErr != nil {
		return nil, c.bindErr
	}
	return &serverReleaseBinding{owner: c}, nil
}

func (c *serverReleaseConn) Close(context.Context) error {
	c.events = append(c.events, "close")
	return c.closeErr
}

func (c *serverReleaseConn) Invoke(
	_ context.Context,
	op dcerpc.Operation,
	_ ...dcerpc.CallOption,
) error {
	if op.OpNum() != 7 {
		return errors.New("unexpected IOPCServer operation during cleanup")
	}
	c.events = append(c.events, "remove group")
	return nil
}

// A distinct presentation context catches calls accidentally sent on IOPCServer.
type serverReleaseBinding struct {
	dcerpc.Conn
	owner *serverReleaseConn
}

func (b *serverReleaseBinding) Invoke(
	ctx context.Context,
	op dcerpc.Operation,
	opts ...dcerpc.CallOption,
) error {
	c := b.owner
	c.events = append(c.events, "release")
	if op.OpName() != "/IRemUnknown/v0/RemRelease" {
		return errors.New("unexpected release operation")
	}
	wire, err := ndr.Marshal(ndr.MarshalNDRFunc(op.MarshalNDRRequest))
	if err != nil {
		return err
	}
	if err := ndr.Unmarshal(wire, &c.request); err != nil {
		return err
	}
	c.callIPID, _ = dcom.HasIPID(opts)
	c.deadline, _ = ctx.Deadline()
	return c.releaseErr
}

func testServerReleaseConn() (*dcomConn, *serverReleaseConn) {
	wire := &serverReleaseConn{}
	conn := &dcomConn{
		cfg: ServerConfig{
			Host:     "opc.test",
			Domain:   "TEST",
			Username: "user",
			Password: "test",
		},
		serverConn:    wire,
		serverIPID:    &dcom.IPID{Data1: 11},
		remoteUnknown: &dcom.IPID{Data1: 22},
		serverRefs:    5,
	}
	return conn, wire
}

func TestServerCloseReleasesActivatedReference(t *testing.T) {
	c, wire := testServerReleaseConn()
	c.pendingRemovals = map[int]bool{7: true}
	s, err := connect(
		context.Background(),
		ServerConfig{},
		func(context.Context, ServerConfig) (connection, error) {
			return c, nil
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := s.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(wire.events, []string{"remove group", "bind", "release", "close"}) {
		t.Fatalf("cleanup order/repetitions = %v", wire.events)
	}
	if wire.callIPID == nil || !wire.callIPID.UUID().Equals(c.remoteUnknown.UUID()) {
		t.Fatalf("RemRelease call IPID = %v, want IRemUnknown", wire.callIPID)
	}
	request := wire.request
	if request.InterfaceReferencesCount != 1 || len(request.InterfaceReferences) != 1 {
		t.Fatalf("release request = %+v", request)
	}
	ref := request.InterfaceReferences[0]
	if ref.IPID == nil || !ref.IPID.UUID().Equals(c.serverIPID.UUID()) ||
		ref.PublicReferencesCount != c.serverRefs || ref.PrivateReferencesCount != 0 {
		t.Fatalf("released reference = %+v", ref)
	}
	deadline, _ := ctx.Deadline()
	if !wire.deadline.Equal(deadline) {
		t.Fatal("release did not use the caller's cleanup deadline")
	}
}

func TestServerClosePreservesReferenceReleaseErrors(t *testing.T) {
	for _, failure := range []string{"bind", "release"} {
		t.Run(failure, func(t *testing.T) {
			c, wire := testServerReleaseConn()
			if failure == "bind" {
				wire.bindErr = io.EOF
			} else {
				wire.releaseErr = io.EOF
			}
			wire.closeErr = io.ErrClosedPipe
			s, err := connect(
				context.Background(),
				ServerConfig{},
				func(context.Context, ServerConfig) (connection, error) {
					return c, nil
				},
			)
			if err != nil {
				t.Fatal(err)
			}
			for range 2 {
				err := s.Close(context.Background())
				if !errors.Is(err, io.EOF) || !errors.Is(err, io.ErrClosedPipe) {
					t.Fatalf("cleanup lost an error: %v", err)
				}
			}
			want := []string{"bind", "release", "close"}
			if failure == "bind" {
				want = []string{"bind", "close"}
			}
			if !slices.Equal(wire.events, want) {
				t.Fatalf("cleanup was skipped or retried: %v", wire.events)
			}
		})
	}
}

func TestServerCloseWithoutActivatedReferences(t *testing.T) {
	c, wire := testServerReleaseConn()
	c.serverRefs = 0
	if err := c.closeContext(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(wire.events, []string{"close"}) {
		t.Fatalf("released an unowned reference: %v", wire.events)
	}
}

func TestResolverCloseReleasesActivatedReferenceOnce(t *testing.T) {
	c, wire := testServerReleaseConn()
	if err := c.closeResolver(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := c.closeContext(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(wire.events, []string{"bind", "release", "close"}) {
		t.Fatalf("resolver cleanup order/repetitions = %v", wire.events)
	}
}

func TestServerCloseReportsMissingReleaseIPID(t *testing.T) {
	for _, missing := range []string{"server", "remote unknown"} {
		t.Run(missing, func(t *testing.T) {
			c, wire := testServerReleaseConn()
			if missing == "server" {
				c.serverIPID = nil
			} else {
				c.remoteUnknown = &dcom.IPID{}
			}
			if err := c.closeContext(context.Background()); err == nil {
				t.Fatal("missing release identity reported as successful cleanup")
			}
			if !slices.Equal(wire.events, []string{"close"}) {
				t.Fatalf("cleanup events = %v", wire.events)
			}
		})
	}
}
