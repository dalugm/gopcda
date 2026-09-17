package opcda

import (
	"context"
	"errors"
	"fmt"

	"github.com/oiweiwei/go-msrpc/msrpc/dcom"
)

func removalErrors(ids []string, cause error) map[string]error {
	out := make(map[string]error, len(ids))
	for _, id := range ids {
		out[id] = cause
	}
	return out
}

func (c *dcomConn) removeItems(
	ctx context.Context,
	handle int,
	ids []string,
) (map[string]error, error) {
	g, err := c.getGroup(handle)
	if err != nil {
		return removalErrors(ids, err), err
	}
	if err := g.acquire(ctx); err != nil {
		return removalErrors(ids, err), err
	}
	defer g.release()
	if err := c.keepaliveError(); err != nil {
		return removalErrors(ids, err), err
	}
	out := make(map[string]error, len(ids))
	pending := make([]string, 0, len(ids))
	for _, id := range ids {
		if _, seen := out[id]; seen {
			continue
		}
		out[id] = nil
		if _, registered := g.items[id]; registered {
			pending = append(pending, id)
		}
	}
	for start := 0; start < len(pending); start += maxBatchItems {
		chunk := pending[start:min(start+maxBatchItems, len(pending))]
		handles := make([]uint32, len(chunk))
		for i, id := range chunk {
			handles[i] = g.items[id].handle
		}
		r := &batchRemoveResponse{count: len(chunk)}
		// IOPCItemMgt inherits IUnknown; AddItems and ValidateItems precede RemoveItems.
		err := g.itemConn.Invoke(ctx, &opcOp{
			opNum:       5,
			interfaceID: iopcItemMgtIID.GUID().UUID(),
			req:         &batchRemoveRequest{handles: handles}, resp: r,
		}, dcom.WithIPID(g.itemIPID))
		if err == nil && r.hresult < 0 {
			err = hresultError("RemoveItems", "", r.hresult)
		}
		if err == nil && len(r.errors) != len(chunk) {
			err = errors.New("RemoveItems: missing result array")
		}
		if err != nil {
			for _, id := range pending[start:] {
				out[id] = fmt.Errorf("RemoveItems %q: removal not confirmed: %w", id, err)
			}
			return out, err
		}
		for i, id := range chunk {
			if r.errors[i] < 0 {
				out[id] = hresultError("RemoveItems", id, r.errors[i])
				continue
			}
			out[id] = nil
			delete(g.items, id)
		}
	}
	return out, nil
}
