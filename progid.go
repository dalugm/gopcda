package opcda

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/oiweiwei/go-msrpc/midl/uuid"
	"github.com/oiweiwei/go-msrpc/msrpc/dcom"
	"github.com/oiweiwei/go-msrpc/msrpc/dtyp"
)

const opcEnumCLSID = "13486d51-4821-11d2-a494-3cb306c10000"

var opcServerListIID = (*dcom.IID)(
	dtyp.GUIDFromUUID(uuid.MustParse("9dd0b56c-ad9e-43ee-8305-487f3188bf7a")),
)

// ResolveProgID resolves a registered ProgID on cfg.Host through OPCEnum's
// IOPCServerList2 interface. It uses cfg's credentials and ignores its CLSID and
// ProgID fields. The server must have an accessible OPCEnum installation.
func ResolveProgID(ctx context.Context, cfg ServerConfig, progID string) (string, error) {
	return resolveProgID(
		ctx,
		cfg,
		progID,
		func(ctx context.Context, cfg ServerConfig) (progIDConnection, error) {
			return activateDCOM(ctx, cfg, opcServerListIID)
		},
	)
}

type progIDConnection interface {
	lookupProgID(context.Context, string) (string, error)
	closeResolver(context.Context) error
}

func resolveProgID(
	ctx context.Context,
	cfg ServerConfig,
	progID string,
	open func(context.Context, ServerConfig) (progIDConnection, error),
) (clsid string, retErr error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if strings.TrimSpace(progID) == "" || strings.ContainsRune(progID, 0) ||
		!utf8.ValidString(progID) {
		return "", errors.New("invalid ProgID")
	}
	cfg.CLSID, cfg.ProgID = opcEnumCLSID, ""
	conn, err := open(ctx, cfg)
	if err != nil {
		return "", fmt.Errorf("activate OPCEnum to resolve ProgID %q: %w", progID, err)
	}
	defer func() {
		cleanup, cancel := cleanupContext()
		defer cancel()
		retErr = errors.Join(retErr, conn.closeResolver(cleanup))
		if retErr != nil {
			clsid = ""
		}
	}()
	return conn.lookupProgID(ctx, progID)
}

func (c *dcomConn) lookupProgID(ctx context.Context, progID string) (string, error) {
	resp := &progIDResponse{}
	op := &opcOp{
		opNum:       5,
		interfaceID: opcServerListIID.GUID().UUID(),
		req:         &progIDRequest{progID: progID},
		resp:        resp,
	}
	if err := c.serverConn.Invoke(ctx, op, dcom.WithIPID(c.serverIPID)); err != nil {
		return "", fmt.Errorf("OPCEnum CLSIDFromProgID %q: %w", progID, err)
	}
	return resp.result(progID)
}

func (c *dcomConn) closeResolver(ctx context.Context) error {
	return c.closeContext(ctx)
}

func resolveServerConfig(
	ctx context.Context,
	cfg ServerConfig,
	resolve func(context.Context, ServerConfig, string) (string, error),
) (ServerConfig, error) {
	if cfg.CLSID != "" {
		return cfg, nil
	}
	if cfg.ProgID == "" {
		return cfg, errors.New("dcom: CLSID or ProgID is required")
	}
	id, err := resolve(ctx, cfg, cfg.ProgID)
	if err != nil {
		return cfg, err
	}
	cfg.CLSID = id
	return cfg, nil
}
