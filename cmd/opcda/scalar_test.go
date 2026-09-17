package main

import (
	"math"
	"reflect"
	"testing"
)

func TestScalarWriteParsing(t *testing.T) {
	for _, tc := range []struct {
		kind, text string
		want       any
	}{
		{"bool", "true", true},
		{"bool", "false", false},
		{"int8", "-128", int8(-128)},
		{"int16", "-32768", int16(-32768)},
		{"int32", "-2147483648", int32(math.MinInt32)},
		{"int64", "-9223372036854775808", int64(math.MinInt64)},
		{"uint8", "255", uint8(255)},
		{"uint16", "65535", uint16(65535)},
		{"uint32", "4294967295", uint32(math.MaxUint32)},
		{"uint64", "18446744073709551615", uint64(math.MaxUint64)},
		{"string", "", ""},
		{"string", " a\x00b\U0001f680 ", " a\x00b\U0001f680 "},
	} {
		got, err := parseWriteValue(tc.text, tc.kind)
		if err != nil || !reflect.DeepEqual(got, tc.want) {
			t.Fatalf("%s: %v (%T), %v", tc.kind, got, got, err)
		}
	}
	for _, tc := range [][2]string{{"int8", "128"}, {"int8", "-129"}, {"uint8", "256"}, {"uint64", "-1"}, {"uint64", "18446744073709551616"}, {"int64", "9223372036854775808"}, {"bool", "yes"}, {"int", "2147483648"}, {"uint", "4294967296"}} {
		if _, err := parseWriteValue(tc[1], tc[0]); err == nil {
			t.Fatalf("accepted %v", tc)
		}
	}
}
