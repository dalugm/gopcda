package main

import (
	"context"
	"io"
	"math"
	"reflect"
	"testing"

	opcda "github.com/dalugm/gopcda"
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
	for _, tc := range [][2]string{{"int8", "128"}, {"int8", "-129"}, {"uint8", "256"}, {"uint64", "-1"}, {"uint64", "18446744073709551616"}, {"int64", "9223372036854775808"}, {"bool", "yes"}, {"int", "1"}, {"uint", "1"}} {
		if _, err := parseWriteValue(tc[1], tc[0]); err == nil {
			t.Fatalf("accepted %v", tc)
		}
	}
}

func TestProgIDConnectionConfiguration(t *testing.T) {
	err := run(context.Background(), []string{"status"}, func(k string) string {
		return map[string]string{"OPCDA_HOST": "target", "OPCDA_PROGID": "Vendor.Server.1", "OPCDA_USERNAME": "operator", "OPCDA_PASSWORD": "test-password"}[k]
	}, io.Discard, io.Discard, func(_ context.Context, cfg opcda.ServerConfig) (server, error) {
		if cfg.ProgID != "Vendor.Server.1" || cfg.CLSID != "" || cfg.Username != "operator" ||
			cfg.Password != "test-password" {
			t.Fatalf("incorrect config: %v", cfg)
		}
		return &fakeServer{}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
