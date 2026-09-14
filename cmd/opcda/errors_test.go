package main

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	opcda "github.com/dalugm/gopcda"
)

func TestCommandErrorDetails(t *testing.T) {
	unknownID := &opcda.HRESULTError{Operation: "AddItems", Code: 0xc0040007}
	for _, tc := range []struct {
		name, command string
		err           error
		want, avoid   string
	}{
		{"missing write item", "write", fmt.Errorf("write %q: %w", "Device.Tag", unknownID), "OPC_E_UNKNOWNITEMID", "Write outcome unknown"},
		{"missing read item", "read", unknownID, "OPC_E_UNKNOWNITEMID", "Write not attempted"},
		{"unknown item during write", "write", &opcda.HRESULTError{Operation: "Write", Code: 0xc0040007}, "OPC_E_UNKNOWNITEMID", "Write not attempted"},
		{"rights", "write", &opcda.HRESULTError{Operation: "Write", Code: 0xc0040006}, "access rights", "Write not attempted"},
		{"type", "write", &opcda.HRESULTError{Operation: "Write", Code: 0xc0040004}, "data type", "Write not attempted"},
		{"range", "write", &opcda.HRESULTError{Operation: "Write", Code: 0xc004000b}, "allowed range", "Write not attempted"},
		{"uncertain", "write", errors.Join(opcda.ErrWriteOutcomeUnknown, unknownID), "Verify the value before retrying", "Write not attempted"},
		{"acknowledged cleanup", "write", errors.Join(opcda.ErrWriteAcknowledged, errors.New("cleanup failed")), "server acknowledged", "Write not attempted"},
		{"not attempted", "write", opcda.ErrWriteNotAttempted, "Write not attempted", "Write outcome unknown"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := formatCommandError(tc.command, tc.err)
			if !strings.Contains(got, tc.err.Error()) || !strings.Contains(got, tc.want) ||
				strings.Contains(got, tc.avoid) || strings.Contains(got, "Hint:") {
				t.Fatalf("unexpected diagnostic: %s", got)
			}
		})
	}
	got := formatCommandError("write", unknownID)
	if !strings.Contains(got, "Write not attempted: item registration failed") {
		t.Fatal(got)
	}
}

func TestCommandErrorFallback(t *testing.T) {
	err := errors.New("connection failed")
	if got := formatCommandError("write", err); got != err.Error() {
		t.Fatal(got)
	}
}
