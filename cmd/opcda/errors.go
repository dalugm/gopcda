package main

import (
	"errors"

	opcda "github.com/dalugm/gopcda"
)

// formatCommandError adds write outcome details at the CLI boundary. The library
// remains silent, and run returns its original error chain for programmatic use.
func formatCommandError(command string, err error) string {
	message := err.Error()
	if command == "write" {
		switch {
		case errors.Is(err, opcda.ErrWriteAcknowledged):
			return message + "\nThe server acknowledged the write, but cleanup failed. Do not repeat the write solely because this command failed."
		case errors.Is(err, opcda.ErrWriteOutcomeUnknown):
			return message + "\nWrite outcome unknown: the request may have executed. Verify the value before retrying."
		case errors.Is(err, opcda.ErrWriteNotAttempted):
			message += "\nWrite not attempted: no write request was sent."
		}
	}
	var hr *opcda.HRESULTError
	if !errors.As(err, &hr) {
		return message
	}
	// A single-item write registers the item before issuing any Write RPC.
	// Do not infer this from the numeric code alone: Write itself may fail too.
	if command == "write" && hr.Operation == "AddItems" &&
		!errors.Is(err, opcda.ErrWriteNotAttempted) {
		message += "\nWrite not attempted: item registration failed."
	}
	return message
}
