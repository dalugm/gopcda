package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	opcda "github.com/dalugm/gopcda"
)

func TestResolveCommand(t *testing.T) {
	for _, configured := range []bool{false, true} {
		var out, diag bytes.Buffer
		env := map[string]string{
			"OPCDA_HOST":     "host",
			"OPCDA_DOMAIN":   "domain",
			"OPCDA_USERNAME": "user",
			"OPCDA_PASSWORD": "test-password",
			"OPCDA_TIMEOUT":  "2s",
		}
		if configured {
			env["OPCDA_CLSID"], env["OPCDA_PROGID"] = "ignored", "Ignored.Server"
		}
		const id = "12345678-1234-1234-1234-123456789abc"
		err := run(
			t.Context(),
			[]string{"resolve", "Example.Server"},
			func(k string) string { return env[k] },
			&out,
			&diag,
			nil,
			func(ctx context.Context, cfg opcda.ServerConfig, progID string) (string, error) {
				deadline, ok := ctx.Deadline()
				if !ok || time.Until(deadline) > 2*time.Second {
					t.Fatal("missing configured timeout")
				}
				if cfg.Host != "host" || cfg.Domain != "domain" || cfg.Username != "user" ||
					cfg.Password != "test-password" ||
					cfg.CLSID != "" ||
					cfg.ProgID != "" ||
					progID != "Example.Server" {
					t.Fatal("incorrect resolution parameters")
				}
				return id, nil
			},
		)
		if err != nil || out.String() != id+"\n" || !strings.Contains(diag.String(), "host") {
			t.Fatalf("err=%v out=%q diag=%q", err, out.String(), diag.String())
		}
	}
}

func TestResolveValidation(t *testing.T) {
	for _, tc := range []struct {
		args          []string
		host, timeout string
	}{
		{[]string{"resolve"}, "host", ""},
		{[]string{"resolve", ""}, "host", ""},
		{[]string{"resolve", "Example.Server", "extra"}, "host", ""},
		{[]string{"resolve", "Example.Server"}, "", ""},
		{[]string{"resolve", "Example.Server"}, "host", "bad"},
	} {
		err := run(
			t.Context(),
			tc.args,
			func(k string) string { return map[string]string{"OPCDA_HOST": tc.host, "OPCDA_TIMEOUT": tc.timeout}[k] },
			io.Discard,
			io.Discard,
			nil,
			func(context.Context, opcda.ServerConfig, string) (string, error) {
				t.Fatal("unexpected lookup")
				return "", nil
			},
		)
		if err == nil {
			t.Fatalf("accepted invalid input: %v", tc.args)
		}
	}
}

func TestResolveFailurePrintsNoResult(t *testing.T) {
	var out bytes.Buffer
	want := errors.New("lookup failed")
	err := run(
		t.Context(),
		[]string{"resolve", "Example.Server"},
		func(k string) string {
			if k == "OPCDA_HOST" {
				return "host"
			}
			return ""
		},
		&out,
		io.Discard,
		nil,
		func(context.Context, opcda.ServerConfig, string) (string, error) { return "partial", want },
	)
	if !errors.Is(err, want) || out.Len() != 0 {
		t.Fatalf("err=%v out=%q", err, out.String())
	}
}
