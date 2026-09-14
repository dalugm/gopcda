package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	opcda "github.com/dalugm/gopcda"
)

type fakeServer struct {
	written any
	writes  int
	closed  bool
	id      string
	err     error
}

func (f *fakeServer) GetServerStatusContext(context.Context) (*opcda.ServerStatus, error) {
	return &opcda.ServerStatus{VendorInfo: "SUPCON"}, f.err
}

func (f *fakeServer) BrowseItemIDs(context.Context) ([]string, error) {
	return []string{"A", "B"}, f.err
}

func (f *fakeServer) ReadItem(_ context.Context, id string) (*opcda.ReadResult, error) {
	f.id = id
	return &opcda.ReadResult{
		ItemID:            id,
		Value:             float32(12.5),
		Quality:           0x18,
		SourceTimestampMs: 1000,
	}, f.err
}
func (f *fakeServer) Disconnect() { f.closed = true }
func TestCommands(t *testing.T) {
	for _, args := range [][]string{{"status"}, {"browse"}, {"read", "13HAD10CT_AVE.VALUE"}} {
		var out, diag bytes.Buffer
		f := &fakeServer{}
		err := run(
			context.Background(),
			args,
			func(k string) string { return map[string]string{"OPCDA_HOST": "host", "OPCDA_CLSID": "clsid"}[k] },
			&out,
			&diag,
			func(ctx context.Context, cfg opcda.ServerConfig) (server, error) {
				if _, ok := ctx.Deadline(); !ok {
					t.Fatal("missing timeout")
				}
				return f, nil
			},
			nil,
		)
		if err != nil || !f.closed {
			t.Fatalf("%v closed=%v", err, f.closed)
		}
		switch args[0] {
		case "status":
			if !strings.Contains(out.String(), "SUPCON") {
				t.Fatal(out.String())
			}
		case "browse":
			if out.String() != "A\nB\n" || !strings.Contains(diag.String(), "2 unique") {
				t.Fatal(out.String(), diag.String())
			}
		case "read":
			var value map[string]any
			if err := json.Unmarshal(out.Bytes(), &value); err != nil {
				t.Fatal(err)
			}
			if f.id != args[1] || value["value"] != 12.5 || value["qualityGood"] != false ||
				value["quality"] != "0x0018" ||
				value["timestamp"] != "1970-01-01T00:00:01Z" {
				t.Fatal(value)
			}
		}
	}
}

func TestValidationAndHelp(t *testing.T) {
	for _, args := range [][]string{nil, {"other"}, {"read"}, {"read", "A", "B"}, {"browse", "extra"}, {"status"}, {"--help"}} {
		var out, diag bytes.Buffer
		err := run(
			context.Background(),
			args,
			func(string) string { return "" },
			&out,
			&diag,
			func(context.Context, opcda.ServerConfig) (server, error) {
				t.Fatal("unexpected connection")
				return nil, nil
			},
			nil,
		)
		if len(args) == 1 && args[0] == "--help" {
			if err != nil || !strings.Contains(out.String(), "usage:") {
				t.Fatal(err)
			}
		} else if err == nil {
			t.Fatal("accepted invalid input")
		}
	}
}

func TestReadFailureClosesAndPrintsNoResult(t *testing.T) {
	var out, diag bytes.Buffer
	want := errors.New("unknown item")
	f := &fakeServer{err: want}
	err := run(context.Background(), []string{"read", "missing"}, func(k string) string {
		if k == "OPCDA_TIMEOUT" {
			return "180s"
		}
		return "x"
	}, &out, &diag, func(context.Context, opcda.ServerConfig) (server, error) { return f, nil }, nil)

	if !errors.Is(err, want) || !f.closed || out.Len() != 0 {
		t.Fatalf("err=%v closed=%v output=%q", err, f.closed, out.String())
	}
}

func (f *fakeServer) WriteItem(_ context.Context, id string, value any) error {
	f.id = id
	f.written = value
	f.writes++
	return f.err
}

func TestFloatWriteCommand(t *testing.T) {
	for _, kind := range []string{"float32", "float64"} {
		var out, diag bytes.Buffer
		f := &fakeServer{}
		args := []string{"write", "TEST.VALUE", "12.5"}
		if kind == "float64" {
			args = append(args, kind)
		}
		err := run(context.Background(), args, func(k string) string {
			if k == "OPCDA_TIMEOUT" {
				return "180s"
			}
			return "x"
		}, &out, &diag, func(context.Context, opcda.ServerConfig) (server, error) { return f, nil }, nil)
		if err != nil || f.writes != 1 || f.id != "TEST.VALUE" || !f.closed {
			t.Fatalf("%+v %v", f, err)
		}
		if kind == "float32" {
			if f.written != float32(12.5) {
				t.Fatal(f.written)
			}
		} else if f.written != float64(12.5) {
			t.Fatal(f.written)
		}
		if !strings.Contains(out.String(), `"acknowledged":true`) {
			t.Fatal(out.String())
		}
	}
}

func TestWriteInvalidValuesNeverConnect(t *testing.T) {
	for _, args := range [][]string{{"write", "A"}, {"write", "A", "NaN"}, {"write", "A", "Inf"}, {"write", "A", "1e50"}, {"write", "A", "1", "int32"}, {"write", "A", "abc"}} {
		err := run(
			context.Background(),
			args,
			func(string) string { return "" },
			&bytes.Buffer{},
			&bytes.Buffer{},
			func(context.Context, opcda.ServerConfig) (server, error) {
				t.Fatal("connected for invalid write")
				return nil, nil
			},
			nil,
		)
		if err == nil {
			t.Fatal(args)
		}
	}
}

func TestWriteFailureDoesNotRetryOrPrintSuccess(t *testing.T) {
	var out, diag bytes.Buffer
	want := errors.New("write denied")
	f := &fakeServer{err: want}
	err := run(context.Background(), []string{"write", "A", "12.5"}, func(k string) string {
		if k == "OPCDA_TIMEOUT" {
			return "180s"
		}
		return "x"
	}, &out, &diag, func(context.Context, opcda.ServerConfig) (server, error) { return f, nil }, nil)
	if !errors.Is(err, want) || f.writes != 1 || !f.closed || out.Len() != 0 {
		t.Fatalf("%+v %v %s", f, err, out.String())
	}
}

func (f *fakeServer) AddGroupContext(context.Context, string, int, float32) (*opcda.Group, error) {
	return nil, errors.New("not expected in this test")
}

func (f *fakeServer) Close(context.Context) error { f.closed = true; return nil }

type blockedStatusServer struct{ fakeServer }

func (s *blockedStatusServer) GetServerStatusContext(
	ctx context.Context,
) (*opcda.ServerStatus, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}

func TestStatusUsesCommandDeadline(t *testing.T) {
	s := &blockedStatusServer{}
	var out, diag bytes.Buffer
	err := run(context.Background(), []string{"status"}, func(k string) string {
		return map[string]string{"OPCDA_HOST": "host", "OPCDA_CLSID": "clsid", "OPCDA_TIMEOUT": "10ms"}[k]
	}, &out, &diag, func(context.Context, opcda.ServerConfig) (server, error) { return s, nil }, nil)
	if !errors.Is(err, context.DeadlineExceeded) || !s.closed || out.Len() != 0 {
		t.Fatal(err, s.closed, out.String())
	}
}
