package opcda

import "testing"

func TestQualityDescription(t *testing.T) {
	for _, tt := range []struct {
		raw  uint16
		want string
	}{
		{0xc0, "Good"},
		{0xd8, "Good (local override)"},
		{0x00, "Bad (non-specific)"},
		{0x04, "Bad (configuration error)"},
		{0x08, "Bad (not connected)"},
		{0x0c, "Bad (device failure)"},
		{0x10, "Bad (sensor failure)"},
		{0x14, "Bad (last known value)"},
		{0x18, "Bad (communication failure)"},
		{0x1c, "Bad (out of service)"},
		{0x20, "Bad (waiting for initial value)"},
		{0x40, "Uncertain"},
		{0x44, "Uncertain (last usable value)"},
		{0x50, "Uncertain (sensor not accurate)"},
		{0x54, "Uncertain (engineering units exceeded)"},
		{0x58, "Uncertain (sub-normal)"},
		{0x80, "Reserved quality"},
		{0x24, "Bad"},
		{0x48, "Uncertain"},
	} {
		for _, extra := range []uint16{0, 1, 2, 3, 0xab00, 0xff03} {
			if got := QualityDescription(int16(tt.raw | extra)); got != tt.want {
				t.Errorf("quality %#04x: %q, want %q", tt.raw|extra, got, tt.want)
			}
		}
	}
}

func TestQualityClassificationPreservesVendorAndLimitBits(t *testing.T) {
	for _, tt := range []struct {
		raw                  uint16
		good, bad, uncertain bool
	}{
		{0xffdb, true, false, false}, {0xab1b, false, true, false}, {0xff57, false, false, true}, {0xff83, false, false, false},
	} {
		q := int16(tt.raw)
		if QualityIsGood(q) != tt.good || QualityIsBad(q) != tt.bad ||
			QualityIsUncertain(q) != tt.uncertain {
			t.Errorf("misclassified %#04x", tt.raw)
		}
	}
}
