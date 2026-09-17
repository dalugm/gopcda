package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"math"
	"testing"

	opcda "github.com/dalugm/gopcda"
)

type propertyServer struct {
	fakeServer
	properties []opcda.ItemProperty
}

func TestPropertiesKeepDescriptionWhenAnotherValueCannotBeJSONEncoded(t *testing.T) {
	var out bytes.Buffer
	err := printProperties(&out, "Pump.Value", []opcda.ItemProperty{
		{ID: 101, Name: "Item Description", DataType: 8, Value: "Pump speed"},
		{ID: 2, Name: "Value", DataType: 5, Value: math.NaN()},
	})
	var got struct {
		DescriptionStatus string
		Properties        []struct {
			Value any
			Error string
		}
	}
	if err == nil {
		t.Fatal("expected a non-JSON value error")
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.DescriptionStatus != "available" || len(got.Properties) != 2 ||
		got.Properties[0].Value != "Pump speed" ||
		got.Properties[1].Error == "" {
		t.Fatalf("output=%s", out.String())
	}
}

func (f *fakeServer) ItemProperties(context.Context, string) ([]opcda.ItemProperty, error) {
	return nil, f.err
}

func (f *propertyServer) ItemProperties(
	_ context.Context,
	id string,
) ([]opcda.ItemProperty, error) {
	f.id = id
	return f.properties, f.err
}

func TestPropertiesCommandReportsDescriptionStates(t *testing.T) {
	for _, tt := range []struct {
		name, status string
		properties   []opcda.ItemProperty
		failed       bool
	}{
		{"missing", "notProvided", nil, false},
		{"empty", "empty", []opcda.ItemProperty{{ID: 101, Name: "Item Description", DataType: 8, Value: ""}}, false},
		{"available", "available", []opcda.ItemProperty{{ID: 101, Name: "Item Description", DataType: 8, Value: "\u6d41\u91cf"}}, false},
		{"failure", "error", []opcda.ItemProperty{{ID: 101, Name: "Item Description", DataType: 8, Error: errors.New("access denied")}}, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			f := &propertyServer{properties: tt.properties}
			var out, diag bytes.Buffer
			err := run(
				t.Context(),
				[]string{"properties", "Pump.Value"},
				func(k string) string { return map[string]string{"OPCDA_HOST": "host", "OPCDA_CLSID": "clsid"}[k] },
				&out,
				&diag,
				func(context.Context, opcda.ServerConfig) (server, error) { return f, nil },
				nil,
			)
			if (err != nil) != tt.failed || !f.closed || f.id != "Pump.Value" {
				t.Fatalf("err=%v server=%+v", err, f)
			}
			var got struct {
				ItemID            string
				DescriptionStatus string
				Properties        []struct {
					ID    uint32
					Value any
					Error string
				}
			}
			if err := json.Unmarshal(out.Bytes(), &got); err != nil {
				t.Fatal(err)
			}
			if got.ItemID != "Pump.Value" || got.DescriptionStatus != tt.status ||
				len(got.Properties) != len(tt.properties) {
				t.Fatalf("output=%s", out.String())
			}
			if tt.failed && got.Properties[0].Error == "" {
				t.Fatalf("missing error: %s", out.String())
			}
			if tt.name == "available" && got.Properties[0].Value != "\u6d41\u91cf" {
				t.Fatalf("lost description: %s", out.String())
			}
		})
	}
}

func TestPropertiesCommandConnects(t *testing.T) {
	want := errors.New("connection unavailable")
	var out, diag bytes.Buffer
	err := run(t.Context(), []string{"properties", "Pump.Value"}, func(k string) string {
		return map[string]string{"OPCDA_HOST": "host", "OPCDA_CLSID": "clsid"}[k]
	}, &out, &diag, func(context.Context, opcda.ServerConfig) (server, error) { return nil, want }, nil)
	if !errors.Is(err, want) {
		t.Fatalf("properties did not reach connection: %v", err)
	}
}
