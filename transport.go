package opcda

import "context"

// connection abstracts the DCOM transport so the high-level API
// doesn't depend on go-msrpc types directly.
type connection interface {
	closeContext(ctx context.Context) error
	writeItem(ctx context.Context, id string, value any) error
	readItem(ctx context.Context, id string) (*ReadResult, error)
	browseItemIDs(ctx context.Context) ([]string, error)
	itemProperties(ctx context.Context, id string) ([]ItemProperty, error)
	addGroup(ctx context.Context, name string, updateRateMs int64, deadband float32) (*Group, error)
	addItems(ctx context.Context, groupHandle int, itemIDs []string) ([]*Item, error)
	removeItems(ctx context.Context, groupHandle int, itemIDs []string) (map[string]error, error)
	readGroup(ctx context.Context, groupHandle int, cache bool) ([]ReadResult, error)
	read(ctx context.Context, groupHandle int, itemIDs []string, cache bool) ([]ReadResult, error)
	write(ctx context.Context, groupHandle int, values map[string]any) (map[string]error, error)
	setGroupActive(
		ctx context.Context,
		groupHandle int,
		active bool,
	) error
	removeGroup(ctx context.Context, groupHandle int) error
	getServerStatus(ctx context.Context) (*ServerStatus, error)
}
