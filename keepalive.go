package opcda

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/oiweiwei/go-msrpc/dcerpc"
	"github.com/oiweiwei/go-msrpc/midl/uuid"
	"github.com/oiweiwei/go-msrpc/msrpc/dcom"
	exporter "github.com/oiweiwei/go-msrpc/msrpc/dcom/iobjectexporter/v0"
	rem "github.com/oiweiwei/go-msrpc/msrpc/dcom/iremunknown/v0"
	"github.com/oiweiwei/go-msrpc/ssp"
	"github.com/oiweiwei/go-msrpc/ssp/credential"
	"github.com/oiweiwei/go-msrpc/ssp/gssapi"
)

func cleanupContext() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), 10*time.Second)
}

type objectPinger struct {
	gate      chan struct{}
	errMu     sync.RWMutex
	conn      dcerpc.Conn
	client    exporter.ObjectExporterClient
	set       uint64
	sequence  uint16
	objects   map[uint64]int
	err       error
	cancel    context.CancelFunc
	rpcCancel context.CancelFunc
	done      chan struct{}
}

func newObjectPinger() *objectPinger {
	p := &objectPinger{gate: make(chan struct{}, 1), objects: make(map[uint64]int)}
	p.gate <- struct{}{}
	return p
}

func (p *objectPinger) acquire(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-p.gate:
	}
	if err := ctx.Err(); err != nil {
		p.release()
		return err
	}
	return nil
}

func (p *objectPinger) release() { p.gate <- struct{}{} }

// startKeepalive runs once before publishing the session. Even when the server
// itself has SORF_NOPING, groups may require pinging on this exporter later.
func (c *dcomConn) startKeepalive(
	ctx context.Context,
	bind func(context.Context) (dcerpc.Conn, error),
) (retErr error) {
	conn, cancel, err := bindCleanupTransport(ctx, c.rpcCtx, bind)
	if err != nil {
		return err
	}
	p := newObjectPinger()
	p.rpcCancel, p.conn = cancel, conn
	defer func() {
		if retErr != nil {
			cleanup, done := cleanupContext()
			defer done()
			retErr = errors.Join(retErr, p.close(cleanup))
		}
	}()
	p.client, err = exporter.NewObjectExporterClient(ctx, conn, dcerpc.WithNoBind(conn))
	if err != nil {
		return err
	}
	if c.serverFlags&0x1000 == 0 {
		if err := p.add(ctx, c.serverOID); err != nil {
			return err
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	loop, loopCancel := context.WithCancel(context.Background())
	p.cancel = loopCancel
	p.done = make(chan struct{})
	c.pinger = p
	c.pingHealth.Store(p)
	go p.run(loop)
	return nil
}

func (c *dcomConn) bindObjectExporter(ctx context.Context) (dcerpc.Conn, error) {
	auth := gssapi.NewSecurityContext(
		ctx,
		gssapi.WithCredential(
			credential.NewFromPassword(c.cfg.Domain+"\\"+c.cfg.Username, c.cfg.Password),
		),
		gssapi.WithMechanismFactory(ssp.NTLM),
	)
	conn, err := dcerpc.Dial(auth, c.cfg.Host,
		dcerpc.WithEndpoint("ncacn_ip_tcp:[135]"), dcerpc.WithMechanism(ssp.NTLM),
	)
	if err != nil {
		return nil, err
	}
	client, err := exporter.NewObjectExporterClient(auth, conn,
		dcerpc.WithSeal(), dcerpc.WithTargetName(c.cfg.Host),
	)
	if err != nil {
		return nil, errors.Join(err, closeRPC(conn))
	}
	return client.Conn(), nil
}

func (c *dcomConn) retainObject(ctx context.Context, oid uint64, flags uint32) error {
	if flags&0x1000 != 0 {
		return nil
	}
	c.pingMu.Lock()
	p := c.pinger
	c.pingMu.Unlock()
	if p == nil {
		return errors.New("DCOM keepalive is not initialized")
	}
	return p.add(ctx, oid)
}

func (p *objectPinger) change(ctx context.Context, add, del []uint64) error {
	// Cancellation before dispatch does not make the remote ping set uncertain.
	if err := ctx.Err(); err != nil {
		return err
	}
	p.sequence++
	r, err := p.client.ComplexPing(
		ctx,
		&exporter.ComplexPingRequest{
			SetID:              p.set,
			SequenceNum:        p.sequence,
			AddToSetCount:      uint16(len(add)),
			DeleteFromSetCount: uint16(len(del)),
			AddToSet:           add,
			DeleteFromSet:      del,
		},
	)
	if err != nil {
		err = fmt.Errorf("DCOM ComplexPing: %w", err)
		p.setError(err)
		return err
	}
	p.set = r.SetID
	return nil
}

func (p *objectPinger) add(ctx context.Context, oid uint64) error {
	if err := p.acquire(ctx); err != nil {
		return err
	}
	defer p.release()
	if oid == 0 {
		return errors.New("missing OID for keepalive")
	}
	if err := p.healthError(); err != nil {
		return err
	}
	if p.objects[oid] == 0 {
		if err := p.change(ctx, []uint64{oid}, nil); err != nil {
			return err
		}
	}
	p.objects[oid]++
	return nil
}

func (c *dcomConn) releaseObject(ctx context.Context, oid uint64) error {
	c.pingMu.Lock()
	p := c.pinger
	c.pingMu.Unlock()
	if p == nil {
		return nil
	}
	if err := p.acquire(ctx); err != nil {
		return err
	}
	defer p.release()
	if p.objects[oid] > 1 {
		p.objects[oid]--
		return nil
	}
	if p.objects[oid] == 0 {
		return nil
	}
	if err := p.change(ctx, nil, []uint64{oid}); err != nil {
		return err
	}
	delete(p.objects, oid)
	return nil
}

func (p *objectPinger) run(ctx context.Context) {
	defer close(p.done)
	ticker := time.NewTicker(60 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			call, cancel := context.WithTimeout(ctx, 10*time.Second)
			if err := p.acquire(call); err != nil {
				cancel()
				continue
			}
			if p.set != 0 {
				_, err := p.client.SimplePing(call, &exporter.SimplePingRequest{SetID: p.set})
				if err != nil && ctx.Err() == nil {
					p.setError(fmt.Errorf("DCOM keepalive failed; reconnect: %w", err))
				}
			}
			p.release()
			cancel()
		}
	}
}
func (p *objectPinger) setError(err error) { p.errMu.Lock(); p.err = err; p.errMu.Unlock() }

func (p *objectPinger) healthError() error { p.errMu.RLock(); defer p.errMu.RUnlock(); return p.err }

func (c *dcomConn) keepaliveError() error {
	p := c.pingHealth.Load()
	if p == nil {
		return nil
	}
	return p.healthError()
}

func (c *dcomConn) stopKeepalive(ctx context.Context) error {
	c.pingMu.Lock()
	p := c.pinger
	c.pinger = nil
	c.pingHealth.Store(nil)
	c.pingMu.Unlock()
	if p == nil {
		return nil
	}
	return p.close(ctx)
}

func (p *objectPinger) close(ctx context.Context) error {
	if p.cancel != nil {
		p.cancel()
	}
	if p.rpcCancel != nil {
		defer p.rpcCancel()
	}
	if p.done != nil {
		<-p.done
	}
	if err := p.acquire(ctx); err != nil {
		return errors.Join(err, p.conn.Close(ctx))
	}
	defer p.release()
	ids := make([]uint64, 0, len(p.objects))
	for oid := range p.objects {
		ids = append(ids, oid)
	}
	var err error
	if len(ids) > 0 {
		err = p.change(ctx, nil, ids)
	}
	return errors.Join(err, p.conn.Close(ctx))
}

func (c *dcomConn) closeContext(ctx context.Context) error {
	if c.rpcCancel != nil {
		defer c.rpcCancel()
	}
	c.groupsMu.Lock()
	if c.closed {
		c.groupsMu.Unlock()
		return nil
	}
	c.closed = true
	c.groupsMu.Unlock()
	done := make(chan struct{})
	go func() { c.groupOps.Wait(); close(done) }()
	select {
	case <-done:
	case <-ctx.Done():
		// Creation observes rpcCancel. Finish local teardown once it unwinds.
		go func() {
			<-done
			cleanup, cancel := cleanupContext()
			defer cancel()
			_ = c.stopKeepalive(cleanup)
			if c.serverConn != nil {
				_ = c.serverConn.Close(cleanup)
			}
		}()
		return ctx.Err()
	}
	var errs []error
	c.groupsMu.Lock()
	ids := make([]int, 0, len(c.groups))
	activeHandles := make(map[int]bool, len(c.groups))
	for id, g := range c.groups {
		ids = append(ids, id)
		activeHandles[g.handle] = true
	}
	pending := make([]int, 0, len(c.pendingRemovals))
	for h := range c.pendingRemovals {
		// A completed removal may have lost its response and its handle may
		// already belong to a new group. Its disposal below owns that handle.
		if !activeHandles[h] {
			pending = append(pending, h)
		}
	}
	c.groupsMu.Unlock()
	for _, h := range pending {
		errs = append(errs, c.removeRemoteGroup(ctx, h))
	}
	for _, id := range ids {
		errs = append(errs, c.removeGroupInternal(ctx, id))
	}
	errs = append(errs, c.releaseServerReference(ctx))
	errs = append(errs, c.stopKeepalive(ctx))
	if c.serverConn != nil {
		errs = append(errs, c.serverConn.Close(ctx))
	}
	return errors.Join(errs...)
}

// releaseServerReference runs once during teardown, after group cleanup. A failed
// RemRelease may have reached the server and must not be replayed.
func (c *dcomConn) releaseServerReference(ctx context.Context) error {
	if c.serverRefs == 0 {
		return nil
	}
	if c.remoteUnknown == nil || c.remoteUnknown.UUID().Equals(&uuid.UUID{}) {
		return errors.New("activation omitted IRemUnknown IPID for release")
	}
	if c.serverIPID == nil || c.serverIPID.UUID().Equals(&uuid.UUID{}) || c.serverConn == nil {
		return errors.New("activation omitted server interface for release")
	}
	auth := gssapi.NewSecurityContext(
		ctx,
		gssapi.WithCredential(
			credential.NewFromPassword(c.cfg.Domain+"\\"+c.cfg.Username, c.cfg.Password),
		),
		gssapi.WithMechanismFactory(ssp.NTLM),
	)
	// Bind IRemUnknown on the existing object transport, without sending its
	// methods on IOPCServer's presentation context. closeContext owns the shared
	// transport; closing this bound client separately would close it twice.
	client, err := rem.NewRemoteUnknownClient(auth, c.serverConn,
		dcerpc.WithSeal(), dcerpc.WithTargetName(c.cfg.Host),
	)
	if err != nil {
		return fmt.Errorf("bind IRemUnknown for server reference release: %w", err)
	}
	_, err = client.IPID(ctx, c.remoteUnknown).RemoteRelease(ctx, &rem.RemoteReleaseRequest{
		This:                     orpcThis(),
		InterfaceReferencesCount: 1,
		InterfaceReferences: []*dcom.RemoteInterfaceReference{
			{IPID: c.serverIPID, PublicReferencesCount: c.serverRefs},
		},
	})
	if err != nil {
		return fmt.Errorf("release activated server reference: %w", err)
	}
	return nil
}
