package opcda

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// Group represents an OPC DA group.
type Group struct {
	mu           sync.RWMutex
	server       *Server
	name         string
	updateRateMs int
	deadband     float32
	active       bool
	handle       int // server-allocated group handle
}

// Item represents an OPC DA item within a group.
type Item struct {
	Error        error `json:"-"` // Per-item AddItems failure; handles are valid only when nil.
	ItemID       string
	ClientHandle int
	ServerHandle int
	group        *Group
}

// AddItems adds items to this group.
// AddItems returns one entry per requested ItemID. Item.Error carries per-item
// failure; successful entries are retained even if a later RPC batch fails.
func (g *Group) AddItems(ctx context.Context, itemIDs []string) ([]*Item, error) {
	ctx, done := g.server.operationContext(ctx)
	defer done()
	if err := g.server.operationError(ctx); err != nil {
		return nil, err
	}
	items, err := g.server.conn.addItems(ctx, g.handle, itemIDs)
	for _, it := range items {
		if it == nil || it.Error != nil {
			continue
		}
		it.group = g
	}
	if err != nil {
		return items, fmt.Errorf("opcda: add items to %q: %w", g.name, err)
	}
	return items, nil
}

// RevisedUpdateRate returns the server-accepted sampling/cache update period.
func (g *Group) RevisedUpdateRate() time.Duration {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return time.Duration(g.updateRateMs) * time.Millisecond
}

// Read performs a synchronous cache read on all items in the group.
// cache=true reads from OPC cache; cache=false reads from device.
// Results follow registration order. Like ReadItems, it preserves observations
// from completed batches alongside a later operation error.
func (g *Group) Read(ctx context.Context, cache bool) ([]ReadResult, error) {
	ctx, done := g.server.operationContext(ctx)
	defer done()
	if err := g.server.operationError(ctx); err != nil {
		return nil, err
	}
	return g.server.conn.readGroup(ctx, g.handle, cache)
}

// ReadItems performs a synchronous cache read on specific items.
// If an operation fails, it returns only observed results, in request order,
// alongside the error. Registered items in uncompleted batches are omitted.
func (g *Group) ReadItems(ctx context.Context, itemIDs []string, cache bool) ([]ReadResult, error) {
	ctx, done := g.server.operationContext(ctx)
	defer done()
	if err := g.server.operationError(ctx); err != nil {
		return nil, err
	}
	return g.server.conn.read(ctx, g.handle, itemIDs, cache)
}

// Write writes values to items in this group.
func (g *Group) Write(ctx context.Context, values map[string]any) (map[string]error, error) {
	ctx, done := g.server.operationContext(ctx)
	defer done()
	if err := g.server.operationError(ctx); err != nil {
		return unattemptedWrites(values, err), err
	}
	return g.server.conn.write(ctx, g.handle, values)
}

// SetActive activates or deactivates the group.
func (g *Group) SetActive(ctx context.Context, active bool) error {
	ctx, done := g.server.operationContext(ctx)
	defer done()
	if err := g.server.operationError(ctx); err != nil {
		return err
	}
	g.mu.RLock()
	rate, deadband := g.updateRateMs, g.deadband
	g.mu.RUnlock()
	if err := g.server.conn.setGroupState(ctx, g.handle, active, rate, deadband); err != nil {
		return fmt.Errorf("opcda: set group %q active=%v: %w", g.name, active, err)
	}
	g.mu.Lock()
	g.active = active
	g.mu.Unlock()
	return nil
}

// Remove removes the group from the server.
func (g *Group) Remove(ctx context.Context) error {
	ctx, done := g.server.operationContext(ctx)
	defer done()
	if err := g.server.operationError(ctx); err != nil {
		return err
	}
	return g.server.conn.removeGroup(ctx, g.handle)
}

// RemoveItems unregisters items without removing the group or closing its session.
// Results contain one entry per distinct ItemID; nil means removed or already
// unregistered. Per-item failures keep their registrations. On an operation error,
// earlier successful removals remain visible, but unconfirmed registrations are
// retained: close the group/session before reuse because remote state is uncertain.
func (g *Group) RemoveItems(ctx context.Context, itemIDs []string) (map[string]error, error) {
	ctx, done := g.server.operationContext(ctx)
	defer done()
	if err := g.server.operationError(ctx); err != nil {
		return removalErrors(itemIDs, err), err
	}
	return g.server.conn.removeItems(ctx, g.handle, itemIDs)
}
