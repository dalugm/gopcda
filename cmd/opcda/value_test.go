package main

import (
	"reflect"
	"testing"
	"time"

	opcda "github.com/dalugm/gopcda"
)

func TestExtendedWriteParsing(t *testing.T) {
	for _, tt := range []struct {
		text, kind string
		want       any
	}{
		{"-922337203685477.5808", "currency", opcda.Currency(-9223372036854775808)},
		{"7.9228162514264337593543950335", "decimal", opcda.Decimal{Hi: 0xffffffff, Lo: 0xffffffffffffffff, Scale: 28}},
		{"2026-09-15T10:00:00+08:00", "date", time.Date(2026, 9, 15, 2, 0, 0, 0, time.UTC)},
		{"0x80004005", "error", opcda.ErrorCode(0x80004005)},
		{"null", "null", opcda.Variant{Type: 1}},
		{"", "empty", opcda.Variant{Type: 0}},
		{"-123", "int", opcda.Variant{Type: 22, Value: int32(-123)}},
		{"4294967295", "uint", opcda.Variant{Type: 23, Value: uint32(0xffffffff)}},
	} {
		got, err := parseWriteValue(tt.text, tt.kind)
		if err != nil || !reflect.DeepEqual(got, tt.want) {
			t.Fatalf("%s: got=%#v want=%#v err=%v", tt.kind, got, tt.want, err)
		}
	}
	for _, tt := range []struct{ text, kind string }{{"1.00001", "currency"}, {"79228162514264337593543950336", "decimal"}, {"NaN", "decimal"}, {"1e3", "currency"}, {"2026-09-15", "date"}, {"1", "null"}, {"abc", "empty"}, {"4294967296", "uint"}} {
		if _, err := parseWriteValue(tt.text, tt.kind); err == nil {
			t.Fatalf("accepted %s %s", tt.text, tt.kind)
		}
	}
}
