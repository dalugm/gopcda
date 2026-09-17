package opcda

import (
	"errors"
	"fmt"
	"math"
	"time"
)

// automationDate converts an OLE Automation DATE with millisecond precision.
// DATE has no timezone. UTC is the representation convention; do not infer the
// server timezone from the client's local timezone. Negative dates use the
// absolute fractional part as time of day, not a signed duration from the epoch.
// https://learn.microsoft.com/en-us/cpp/atl-mfc-shared/date-type
func automationDate(value float64) (time.Time, error) {
	if math.IsNaN(value) || math.IsInf(value, 0) || value <= -657435 || value >= 2958466 {
		return time.Time{}, fmt.Errorf("invalid VARIANT_DATE value %v", value)
	}
	days, fraction := math.Modf(value)
	date := time.Date(1899, 12, 30, 0, 0, 0, 0, time.UTC).AddDate(0, 0, int(days))
	date = date.Add(time.Duration(math.Round(math.Abs(fraction)*86400000)) * time.Millisecond)
	if date.Year() < 100 || date.Year() > 9999 {
		return time.Time{}, errors.New("VARIANT_DATE outside years 100..9999")
	}
	return date, nil
}
