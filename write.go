package opcda

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/oiweiwei/go-msrpc/dcerpc"
	"github.com/oiweiwei/go-msrpc/msrpc/dcom"
	"github.com/oiweiwei/go-msrpc/msrpc/dcom/oaut"
)

// WriteItem synchronously writes a value to one full ItemID using a
// temporary DA2 group. See the supported type mapping in README.
// A nil error means the server acknowledged the write and cleanup succeeded;
// it does not verify the physical process value. Writes are never retried.
// An error after sending the request does not imply the value was unchanged.
func (s *Server) WriteItem(ctx context.Context, id string, value any) error {
	if id == "" || strings.ContainsRune(id, 0) {
		return errors.New("opcda: ItemID must be nonempty and contain no NUL")
	}
	if _, err := writeVariant(value); err != nil {
		return err
	}
	ctx, done := s.operationContext(ctx)
	defer done()
	if err := s.operationError(ctx); err != nil {
		return err
	}
	if err := s.conn.writeItem(ctx, id, value); err != nil {
		return fmt.Errorf("opcda: write %q: %w", id, err)
	}
	return nil
}

func (c *dcomConn) writeItem(ctx context.Context, id string, value any) error {
	v, err := writeVariant(value)
	if err != nil {
		return err
	}
	acknowledged := false
	err = c.withSingleItem(
		ctx,
		id,
		func(conn dcerpc.Conn, ipid *dcom.IPID, item *addOneResponse) error {
			if err := writeSingleValue(ctx, conn, ipid, item, v); err != nil {
				return err
			}
			acknowledged = true
			return nil
		},
	)
	if err != nil && acknowledged {
		return fmt.Errorf("%w: %w", ErrWriteAcknowledged, err)
	}
	return err
}

func writeSingleValue(
	ctx context.Context,
	conn dcerpc.Conn,
	ipid *dcom.IPID,
	item *addOneResponse,
	v *oaut.Variant,
) error {
	// AddItems access rights are hints; the Write response decides permission.
	response := &writeOneResponse{}
	if err := conn.Invoke(
		ctx,
		&opcOp{
			opNum:       4,
			interfaceID: iopcSyncIOIID.GUID().UUID(),
			req:         &writeOneRequest{handle: item.handle, value: v},
			resp:        response,
		},
		dcom.WithIPID(ipid),
	); err != nil {
		return unknownWrite(err)
	}
	return response.check()
}
