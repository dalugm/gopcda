package opcda

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

func TestHRESULTMessages(t *testing.T) {
	for _, tc := range []struct {
		code         uint32
		name, detail string
	}{
		{0xc0040007, "OPC_E_UNKNOWNITEMID", "ItemID"},
		{0xc0040006, "OPC_E_BADRIGHTS", "access rights"},
		{0xc0040004, "OPC_E_BADTYPE", "data type"},
		{0x80020005, "DISP_E_TYPEMISMATCH", "Type mismatch."},
		{0x80070005, "E_ACCESSDENIED", "denied"},
		{0xc004000b, "OPC_E_RANGE", "range"},
		{0xc0040001, "OPC_E_INVALIDHANDLE", "handle"},
	} {
		hr := &HRESULTError{Operation: "AddItems", ItemID: "Channel.Device.Tag", Code: tc.code}
		err := fmt.Errorf("request failed: %w", hr)
		for _, part := range []string{tc.name, tc.detail, "AddItems", "Channel.Device.Tag", fmt.Sprintf("0x%08x", tc.code)} {
			if !strings.Contains(err.Error(), part) {
				t.Errorf("missing %q in %q", part, err.Error())
			}
		}
		var got *HRESULTError
		if !errors.As(err, &got) || got != hr {
			t.Fatal("lost typed HRESULT")
		}
	}
}

func TestUnknownHRESULTMessage(t *testing.T) {
	err := &HRESULTError{Operation: "Read", Code: 0x81234567}
	if got := err.Error(); got != "Read: HRESULT 0x81234567" {
		t.Fatal(got)
	}
}
