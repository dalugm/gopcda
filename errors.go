package opcda

import (
	"errors"
	"fmt"

	"github.com/oiweiwei/go-msrpc/msrpc/erref/hresult"
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
	result := fmt.Sprintf("HRESULT 0x%08x", e.Code)
	name, description := describeOPCError(e.Code)
	if name == "" {
		if standard, ok := errors.AsType[*hresult.Error](hresult.FromCode(e.Code)); ok {
			name, description = standard.Name, standard.Details
		}
	}
	if name != "" {
		result += fmt.Sprintf(" (%s): %s", name, description)
	}
	if e.ItemID != "" {
		return fmt.Sprintf("%s %q: %s", e.Operation, e.ItemID, result)
	}
	return fmt.Sprintf("%s: %s", e.Operation, result)
}

func describeOPCError(code uint32) (string, string) {
	switch code {
	case 0xc0040001:
		return "OPC_E_INVALIDHANDLE", "the item handle is invalid"
	case 0xc0040004:
		return "OPC_E_BADTYPE", "the item does not accept the requested data type"
	case 0xc0040006:
		return "OPC_E_BADRIGHTS", "the operation is not permitted by the item's access rights"
	case 0xc0040007:
		return "OPC_E_UNKNOWNITEMID", "the ItemID is not available in the server address space"
	case 0xc004000b:
		return "OPC_E_RANGE", "the value is outside the item's allowed range"
	default:
		return "", ""
	}
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
