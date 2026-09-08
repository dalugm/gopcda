package opcda

import (
	"errors"
	"fmt"
)

var (
	// ErrClosed indicates the server session has been closed.
	ErrClosed = errors.New("OPC connection is closed")
	// ErrGroupClosed indicates a removed or closed group.
	ErrGroupClosed = errors.New("OPC group is closed or removed")
	// ErrWriteOutcomeUnknown means a write may have executed. Do not blindly retry.
	ErrWriteOutcomeUnknown = errors.New("write outcome unknown")
	// ErrWriteNotAttempted indicates no write RPC was attempted for this item.
	ErrWriteNotAttempted = errors.New("write not attempted")
	// ErrWriteAcknowledged indicates writing succeeded but subsequent cleanup failed.
	ErrWriteAcknowledged = errors.New(
		"write acknowledged by server, but cleanup failed; do not blindly retry",
	)
	// ErrItemNotRegistered indicates an ItemID has no handle in this group.
	ErrItemNotRegistered = errors.New("item is not registered in this group")
)

// HRESULTError preserves the server's 32-bit COM result for errors.As.
// ItemID is empty for call-level results or operations without a known item.
type HRESULTError struct {
	Operation string
	ItemID    string
	Code      uint32
}

func (e *HRESULTError) Error() string {
	if e.ItemID != "" {
		return fmt.Sprintf("%s %q: HRESULT 0x%08x", e.Operation, e.ItemID, e.Code)
	}
	return fmt.Sprintf("%s: HRESULT 0x%08x", e.Operation, e.Code)
}

func hresultError(operation, itemID string, code int32) error {
	return &HRESULTError{Operation: operation, ItemID: itemID, Code: uint32(code)}
}
func unknownWrite(err error) error { return fmt.Errorf("%w: %w", ErrWriteOutcomeUnknown, err) }

func unattemptedWrites(values map[string]any, cause error) map[string]error {
	result := make(map[string]error, len(values))
	for id := range values {
		result[id] = errors.Join(ErrWriteNotAttempted, cause)
	}
	return result
}
