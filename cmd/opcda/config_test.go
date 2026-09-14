package main

import (
	"context"
	"io"
	"testing"

	opcda "github.com/dalugm/gopcda"
)

func TestProgIDConnectionConfiguration(t *testing.T) {
	err := run(context.Background(), []string{"status"}, func(k string) string {
		return map[string]string{"OPCDA_HOST": "target", "OPCDA_PROGID": "Vendor.Server.1", "OPCDA_USERNAME": "operator", "OPCDA_PASSWORD": "test-password"}[k]
	}, io.Discard, io.Discard, func(_ context.Context, cfg opcda.ServerConfig) (server, error) {
		if cfg.ProgID != "Vendor.Server.1" || cfg.CLSID != "" || cfg.Username != "operator" ||
			cfg.Password != "test-password" {
			t.Fatalf("incorrect config: %v", cfg)
		}
		return &fakeServer{}, nil
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
}
