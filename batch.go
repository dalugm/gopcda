package opcda

import (
	"cmp"
	"context"
	"errors"
	"math"
	"slices"

	"github.com/oiweiwei/go-msrpc/msrpc/dcom"
	"github.com/oiweiwei/go-msrpc/msrpc/dcom/oaut"
)

func (c *dcomConn) addItems(ctx context.Context, handle int, ids []string) ([]*Item, error) {
	g, err := c.getGroup(handle)
	if err != nil {
		return nil, err
	}
	if err := g.acquire(ctx); err != nil {
		return nil, err
	}
	defer g.release()
	if err := c.keepaliveError(); err != nil {
		return nil, err
	}
	out := make([]*Item, len(ids))
	pending := []string{}
	positions := map[string][]int{}
	for i, id := range ids {
		out[i] = &Item{ItemID: id}
		if err := validateItemID(id); err != nil {
			out[i].Error = err
			continue
		}
		if v, ok := g.items[id]; ok {
			out[i] = publicItem(id, v)
			continue
		}
		if _, ok := positions[id]; !ok {
			pending = append(pending, id)
		}
		positions[id] = append(positions[id], i)
		out[i].Error = errors.New("item not added")
	}
	for start := 0; start < len(pending); start += maxBatchItems {
		chunk := pending[start:min(start+maxBatchItems, len(pending))]
		clients := make([]uint32, len(chunk))
		for i := range chunk {
			if g.nextClientHandle == math.MaxUint32 {
				return out, errors.New("client handle range exhausted")
			}
			g.nextClientHandle++
			clients[i] = g.nextClientHandle
		}
		r := &batchAddResponse{count: len(chunk)}
		if err := g.itemConn.Invoke(
			ctx,
			&opcOp{
				opNum:       3,
				interfaceID: iopcItemMgtIID.GUID().UUID(),
				req:         &batchAddRequest{ids: chunk, clients: clients},
				resp:        r,
			},
			dcom.WithIPID(g.itemIPID),
		); err != nil {
			return out, err
		}
		if r.hresult < 0 {
			return out, hresultError("AddItems", "", r.hresult)
		}
		if len(r.items) != len(chunk) || len(r.errors) != len(chunk) {
			return out, errors.New("AddItems: missing result arrays")
		}
		for i, id := range chunk {
			if r.errors[i] < 0 {
				for _, j := range positions[id] {
					out[j].Error = hresultError("AddItems", id, r.errors[i])
				}
				continue
			}
			v := registeredItem{
				r.items[i].handle,
				clients[i],
				r.items[i].rights,
				r.items[i].canonical,
			}
			g.items[id] = v
			for _, j := range positions[id] {
				out[j] = publicItem(id, v)
			}
		}
	}
	return out, nil
}

func publicItem(id string, v registeredItem) *Item {
	return &Item{ItemID: id, ClientHandle: int(v.client), ServerHandle: int(v.handle)}
}

func (c *dcomConn) read(
	ctx context.Context,
	handle int,
	ids []string,
	cache bool,
) ([]ReadResult, error) {
	g, err := c.getGroup(handle)
	if err != nil {
		return nil, err
	}
	if err := g.acquire(ctx); err != nil {
		return nil, err
	}
	defer g.release()
	return c.readLocked(ctx, g, ids, cache)
}

func (c *dcomConn) readGroup(
	ctx context.Context,
	handle int,
	cache bool,
) ([]ReadResult, error) {
	g, err := c.getGroup(handle)
	if err != nil {
		return nil, err
	}
	if err := g.acquire(ctx); err != nil {
		return nil, err
	}
	defer g.release()
	ids := make([]string, 0, len(g.items))
	for id := range g.items {
		ids = append(ids, id)
	}
	slices.SortFunc(ids, func(a, b string) int {
		return cmp.Compare(g.items[a].client, g.items[b].client)
	})
	return c.readLocked(ctx, g, ids, cache)
}

// readLocked requires the caller to hold the group gate through the last RPC.
func (c *dcomConn) readLocked(
	ctx context.Context,
	g *persistentGroup,
	ids []string,
	cache bool,
) ([]ReadResult, error) {
	if err := c.keepaliveError(); err != nil {
		return nil, err
	}
	out := make([]ReadResult, len(ids))
	observed := make([]bool, len(ids))
	known := []string{}
	positions := []int{}
	handles := []uint32{}
	for i, id := range ids {
		out[i] = ReadResult{ItemID: id, Error: ErrItemNotRegistered}
		if v, ok := g.items[id]; ok {
			known = append(known, id)
			positions = append(positions, i)
			handles = append(handles, v.handle)
		} else {
			observed[i] = true
		}
	}
	partialResults := func() []ReadResult {
		partial := out[:0]
		for i, result := range out {
			if observed[i] {
				partial = append(partial, result)
			}
		}
		return partial
	}
	for start := 0; start < len(known); start += maxBatchItems {
		end := min(start+maxBatchItems, len(known))
		r := &batchReadResponse{count: end - start}
		if err := g.syncConn.Invoke(
			ctx,
			&opcOp{
				opNum:       3,
				interfaceID: iopcSyncIOIID.GUID().UUID(),
				req:         &batchReadRequest{handles: handles[start:end], cache: cache},
				resp:        r,
			},
			dcom.WithIPID(g.syncIPID),
		); err != nil {
			return partialResults(), err
		}
		values, err := r.results(known[start:end])
		if err != nil {
			return partialResults(), err
		}
		for i, v := range values {
			out[positions[start+i]] = v
			observed[positions[start+i]] = true
		}
	}
	return out, nil
}

func (c *dcomConn) write(
	ctx context.Context,
	handle int,
	values map[string]any,
) (map[string]error, error) {
	g, err := c.getGroup(handle)
	if err != nil {
		return unattemptedWrites(values, err), err
	}
	if err := g.acquire(ctx); err != nil {
		return unattemptedWrites(values, err), err
	}
	defer g.release()
	if err := c.keepaliveError(); err != nil {
		return unattemptedWrites(values, err), err
	}
	ids := make([]string, 0, len(values))
	for id := range values {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	result := make(map[string]error, len(values))
	known := []string{}
	handles := []uint32{}
	variants := []*oaut.Variant{}
	for _, id := range ids {
		v, ok := g.items[id]
		if !ok {
			result[id] = errors.Join(ErrWriteNotAttempted, ErrItemNotRegistered)
			continue
		}
		// AddItems access rights are hints; the Write response decides permission.
		variant, err := writeVariant(values[id])
		if err != nil {
			result[id] = errors.Join(ErrWriteNotAttempted, err)
			continue
		}
		known = append(known, id)
		handles = append(handles, v.handle)
		variants = append(variants, variant)
		result[id] = ErrWriteNotAttempted
	}
	for start := 0; start < len(known); start += maxBatchItems {
		end := min(start+maxBatchItems, len(known))
		r := &batchWriteResponse{count: end - start}
		err := g.syncConn.Invoke(
			ctx,
			&opcOp{
				opNum:       4,
				interfaceID: iopcSyncIOIID.GUID().UUID(),
				req: &batchWriteRequest{
					handles: handles[start:end],
					values:  variants[start:end],
				},
				resp: r,
			},
			dcom.WithIPID(g.syncIPID),
		)
		if err != nil {
			for _, id := range known[start:end] {
				result[id] = unknownWrite(err)
			}
			return result, unknownWrite(err)
		}
		if r.hresult < 0 {
			err = hresultError("Write", "", r.hresult)
			for _, id := range known[start:end] {
				result[id] = err
			}
			return result, err
		}
		if len(r.errors) != end-start {
			err = unknownWrite(errors.New("Write: missing item results"))
			for _, id := range known[start:end] {
				result[id] = err
			}
			return result, err
		}
		for i, id := range known[start:end] {
			result[id] = nil
			if r.errors[i] < 0 {
				result[id] = hresultError("Write", id, r.errors[i])
			}
		}
	}
	return result, nil
}
