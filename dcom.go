package opcda

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf16"

	"github.com/oiweiwei/go-msrpc/dcerpc"
	"github.com/oiweiwei/go-msrpc/midl/uuid"
	dcom "github.com/oiweiwei/go-msrpc/msrpc/dcom"
	iact "github.com/oiweiwei/go-msrpc/msrpc/dcom/iactivation/v0"
	oaut "github.com/oiweiwei/go-msrpc/msrpc/dcom/oaut"
	dtyp "github.com/oiweiwei/go-msrpc/msrpc/dtyp"
	"github.com/oiweiwei/go-msrpc/ssp"
	"github.com/oiweiwei/go-msrpc/ssp/credential"
	"github.com/oiweiwei/go-msrpc/ssp/gssapi"
)

type dcomConn struct {
	rpcCtx          context.Context
	rpcCancel       context.CancelFunc
	groupsMu        sync.Mutex
	groupOps        sync.WaitGroup
	pendingRemovals map[int]bool
	nextGroupID     int
	groups          map[int]*persistentGroup
	closed          bool
	pingMu          sync.Mutex
	pinger          *objectPinger
	pingHealth      atomic.Pointer[objectPinger]
	serverOID       uint64
	serverFlags     uint32
	serverRefs      uint32
	objectPort      string
	serverOXID      uint64
	remoteUnknown   *dcom.IPID
	cfg             ServerConfig
	serverConn      dcerpc.Conn
	serverIPID      *dcom.IPID
}

const ncacnIPTCP = 7

func dialDCOM(ctx context.Context, cfg ServerConfig) (_ connection, retErr error) {
	var err error
	cfg, err = resolveServerConfig(ctx, cfg, ResolveProgID)
	if err != nil {
		return nil, err
	}
	conn, err := activateDCOM(ctx, cfg, iopcServerIID)
	if err != nil {
		return nil, err
	}
	if err := conn.startKeepalive(ctx, conn.bindObjectExporter); err != nil {
		cleanup, cancel := cleanupContext()
		defer cancel()
		return nil, errors.Join(fmt.Errorf("server keepalive: %w", err), conn.closeContext(cleanup))
	}
	return conn, nil
}

func activateDCOM(
	ctx context.Context,
	cfg ServerConfig,
	iid *dcom.IID,
) (_ *dcomConn, retErr error) {
	clsID, err := parseGUID(cfg.CLSID)
	if err != nil {
		return nil, fmt.Errorf("dcom: invalid CLSID: %w", err)
	}
	rpcCtx, rpcCancel := context.WithCancel(context.WithoutCancel(ctx))
	stopInit := context.AfterFunc(ctx, rpcCancel)
	initialized := false
	defer func() {
		stopInit()
		if !initialized {
			rpcCancel()
		}
	}()
	ctx = rpcCtx

	creds := credential.NewFromPassword(cfg.Domain+"\\"+cfg.Username, cfg.Password)
	ctx = gssapi.NewSecurityContext(ctx,
		gssapi.WithCredential(creds),

		gssapi.WithMechanismFactory(ssp.NTLM),
	)

	epmConn, err := dcerpc.Dial(ctx, cfg.Host,
		dcerpc.WithEndpoint("ncacn_ip_tcp:[135]"),
		dcerpc.WithSeal(),
		dcerpc.WithTargetName(cfg.Host),
	)
	if err != nil {
		return nil, fmt.Errorf("dcom: dial epm %s: %w", cfg.Host, err)
	}

	epmClosed := false
	defer func() {
		if !epmClosed {
			retErr = errors.Join(retErr, closeRPC(epmConn))
		}
	}()

	actCli, err := iact.NewActivationClient(ctx, epmConn,
		dcerpc.WithSeal(),
		dcerpc.WithTargetName(cfg.Host),
	)
	if err != nil {
		return nil, fmt.Errorf("dcom: bind activation: %w", err)
	}

	req := &iact.RemoteActivationRequest{
		ORPCThis: &dcom.ORPCThis{
			Version: &dcom.COMVersion{MajorVersion: 5, MinorVersion: 7},
			CID:     &dcom.CID{},
		},
		ClassID:                         clsID,
		Interfaces:                      1,
		IIDs:                            []*dcom.IID{iid},
		Mode:                            0,
		RequestedProtocolSequencesCount: 1,
		RequestedProtocolSequences:      []uint16{ncacnIPTCP},
	}

	resp, err := actCli.RemoteActivation(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("dcom: remote activation: %w", err)
	}
	if resp.HResult != 0 {
		return nil, hresultError("RemoteActivation", "", resp.HResult)
	}
	if len(resp.InterfaceData) == 0 || resp.InterfaceData[0] == nil {
		return nil, errors.New("dcom: activation returned no interface data")
	}
	if len(resp.Results) > 0 && resp.Results[0] != 0 {
		return nil, hresultError("RemoteActivation interface", "", resp.Results[0])
	}

	serverRef, err := decodeStandardReference(resp.InterfaceData[0], iid.GUID().UUID())
	if err != nil {
		return nil, fmt.Errorf("dcom: activation reference: %w", err)
	}
	ipid := serverRef.IPID

	// Extract the OPC server's DCOM endpoint from OXID bindings.
	// The DualStringArray contains string bindings (e.g., ncacn_ip_tcp:host[port])
	// followed by security bindings, all as null-terminated UTF-16 entries.
	dcomPort, err := extractTCPPort(resp.OXIDBindings)
	if err != nil {
		return nil, fmt.Errorf("dcom: activation endpoint: %w", err)
	}
	serverConn, err := dcerpc.Dial(ctx, cfg.Host,
		dcerpc.WithEndpoint("ncacn_ip_tcp:["+dcomPort+"]"),
		dcerpc.WithTargetName(cfg.Host),
		dcerpc.WithMechanism(ssp.NTLM),
	)
	if err != nil {
		return nil, fmt.Errorf("dcom: dial server conn %s port %s: %w", cfg.Host, dcomPort, err)
	}

	opcSrvUUID := &dcerpc.SyntaxID{
		IfUUID:         guidToUUID(iid.GUID()),
		IfVersionMajor: 0,
		IfVersionMinor: 0,
	}
	bound, err := serverConn.Bind(ctx,
		dcerpc.WithAbstractSyntax(opcSrvUUID),
		dcerpc.WithSeal(),
		dcerpc.WithTargetName(cfg.Host),
	)
	if err != nil {
		return nil, errors.Join(
			fmt.Errorf("dcom: bind activated interface: %w", err),
			closeRPC(serverConn),
		)
	}

	epmClosed = true
	if err := closeRPC(epmConn); err != nil {
		return nil, errors.Join(err, closeRPC(bound))
	}

	initialized = true
	return &dcomConn{
		rpcCtx:        rpcCtx,
		rpcCancel:     rpcCancel,
		objectPort:    dcomPort,
		serverOXID:    serverRef.OXID,
		serverOID:     serverRef.OID,
		serverFlags:   serverRef.Flags,
		serverRefs:    serverRef.PublicReferencesCount,
		remoteUnknown: resp.RemoteUnknown,
		cfg:           cfg,
		serverConn:    bound,
		serverIPID:    ipid,
	}, nil
}

func parseGUID(s string) (*dtyp.GUID, error) {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "{") {
		if len(s) != 38 || !strings.HasSuffix(s, "}") {
			return nil, fmt.Errorf("malformed GUID %q", s)
		}
		s = s[1 : len(s)-1]
	}
	u, err := uuid.Parse(s)
	if err != nil {
		return nil, err
	}
	if u.Equals(&uuid.UUID{}) {
		return nil, errors.New("CLSID must not be zero")
	}
	return dtyp.GUIDFromUUID(u), nil
}

func extractTCPPort(bindings *dcom.DualStringArray) (string, error) {
	if bindings == nil || bindings.SecurityOffset == 0 ||
		int(bindings.SecurityOffset) > len(bindings.StringArray) {
		return "", errors.New("missing or invalid string bindings")
	}
	sa := bindings.StringArray[:bindings.SecurityOffset]
	for i := 0; i < len(sa) && sa[i] != 0; {
		tower := sa[i]
		i++
		start := i
		for i < len(sa) && sa[i] != 0 {
			i++
		}
		if i == len(sa) {
			return "", errors.New("unterminated string binding")
		}
		address := string(utf16.Decode(sa[start:i]))
		i++
		if tower != ncacnIPTCP {
			continue
		}
		left := strings.LastIndexByte(address, '[')
		if left < 0 || !strings.HasSuffix(address, "]") {
			continue
		}
		port := address[left+1 : len(address)-1]
		n, err := strconv.ParseUint(port, 10, 16)
		if err == nil && n > 0 {
			return strconv.FormatUint(n, 10), nil
		}
	}
	return "", errors.New("no usable TCP object endpoint in activation bindings")
}

var (
	iopcServerIID = &dcom.IID{
		Data1: 0x39C13A4D, Data2: 0x011E, Data3: 0x11D0,
		Data4: []byte{0x96, 0x75, 0x00, 0x20, 0xAF, 0xD8, 0xAD, 0xB3},
	}
	iopcItemMgtIID = &dcom.IID{
		Data1: 0x39C13A54, Data2: 0x011E, Data3: 0x11D0,
		Data4: []byte{0x96, 0x75, 0x00, 0x20, 0xAF, 0xD8, 0xAD, 0xB3},
	}
	iopcSyncIOIID = &dcom.IID{
		Data1: 0x39C13A52, Data2: 0x011E, Data3: 0x11D0,
		Data4: []byte{0x96, 0x75, 0x00, 0x20, 0xAF, 0xD8, 0xAD, 0xB3},
	}
	iopcGroupStateMgtIID = &dcom.IID{
		Data1: 0x39C13A50, Data2: 0x011E, Data3: 0x11D0,
		Data4: []byte{0x96, 0x75, 0x00, 0x20, 0xAF, 0xD8, 0xAD, 0xB3},
	}
)

var (
	_ = iact.GoPackage
	_ = dcom.GoPackage
	_ = oaut.GoPackage
	_ = dtyp.GoPackage
	_ = time.Second
)
