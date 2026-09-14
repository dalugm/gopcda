// Package opcda implements a high-level OPC DA (Data Access) client using
// go-opcda bindings and go-msrpc for cross-platform, pure-Go DCOM transport.
//
// Connect creates a server session. Reuse a Group for periodic batch reads and
// on-demand scalar writes. The cmd tools are optional and are not used by the library.
package opcda

import "fmt"

// Quality bits (OPC DA quality specification).
const (
	QualityMask      = 0xC0
	QualityBad       = 0x00
	QualityUncertain = 0x40
	QualityGood      = 0xC0

	QualityBadConfigError    = 0x04
	QualityBadNotConnected   = 0x08
	QualityBadDeviceFailure  = 0x0C
	QualityBadLastValue      = 0x14
	QualityBadCommFailure    = 0x18
	QualityBadOutOfService   = 0x1C
	QualityGoodLocalOverride = 0xD8
)

// QualityIsGood reports whether the quality class is Good.
func QualityIsGood(q int16) bool { return q&QualityMask == QualityGood }

// QualityIsBad reports whether the quality class is Bad.
func QualityIsBad(q int16) bool { return q&QualityMask == QualityBad }

// QualityIsUncertain reports whether the quality class is Uncertain.
func QualityIsUncertain(q int16) bool { return q&QualityMask == QualityUncertain }

// ServerConfig holds connection parameters for an OPC DA server.
type ServerConfig struct {
	Host     string // server hostname or IP
	Domain   string // Windows domain
	Username string
	Password string
	CLSID    string // COM CLSID (e.g. "{F8582CF2-88FB-11D0-B850-00C0F0104305}")
	ProgID   string // Resolved through remote OPCEnum when CLSID is empty.
}

func (c ServerConfig) String() string {
	return fmt.Sprintf("%s@%s", c.Username, c.Host)
}

// DataValue holds a single OPC item's value, quality, and timestamps.
type DataValue struct {
	ItemID            string
	Value             any // Numeric scalars (including uint64), bool, or nil; conversion depends on the read API.
	Quality           int16
	TimestampMs       int64 // OPC DA server timestamp (from cache)
	SourceTimestampMs int64 // Server-provided timestamp; not proof of independent device sampling.
	Error             error `json:"-"`
}

// ReadResult holds the result of a single item read.
type ReadResult struct {
	ItemID            string
	Value             any
	Quality           int16
	SourceTimestampMs int64
	Error             error `json:"-"`
}
