package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	opcda "github.com/dalugm/gopcda"
)

type fakePollingGroup struct {
	reads   int
	removed bool
	fail    error
	addErr  error
}

func (g *fakePollingGroup) AddItems(context.Context, []string) ([]*opcda.Item, error) {
	return []*opcda.Item{{ItemID: "A", Error: g.addErr}}, nil
}

func (g *fakePollingGroup) Read(context.Context, opcda.ReadSource) ([]opcda.ReadResult, error) {
	g.reads++
	return []opcda.ReadResult{{ItemID: "A", Quality: 0xc0}}, g.fail
}
func (g *fakePollingGroup) Remove(context.Context) error     { g.removed = true; return nil }
func (g *fakePollingGroup) RevisedUpdateRate() time.Duration { return 600 * time.Millisecond }
func TestPollUsesOneGroupAndReportsRevisedRate(t *testing.T) {
	g := &fakePollingGroup{}
	var out, diag bytes.Buffer
	if err := runPoll(
		context.Background(),
		&pollConfig{
			ids:      []string{"A"},
			interval: time.Millisecond,
			cycles:   3,
			source:   opcda.SourceCache,
		},
		g,
		&out,
		&diag,
	); err != nil {
		t.Fatal(err)
	}
	var v map[string]any
	if err := json.Unmarshal(out.Bytes(), &v); err != nil {
		t.Fatal(err)
	}
	if g.reads != 3 || !g.removed || v["revisedMs"] != float64(600) ||
		v["goodSamples"] != float64(3) {
		t.Fatalf("%+v %v", g, v)
	}
}

func TestPollFailureCleansUp(t *testing.T) {
	g := &fakePollingGroup{fail: errors.New("read failed")}
	err := runPoll(
		context.Background(),
		&pollConfig{ids: []string{"A"}, interval: time.Millisecond, cycles: 3},
		g,
		&bytes.Buffer{},
		&bytes.Buffer{},
	)
	if !errors.Is(err, g.fail) || !g.removed || g.reads != 1 {
		t.Fatal(err, g)
	}
}

func TestPollPreservesAddItemError(t *testing.T) {
	want := &opcda.HRESULTError{Operation: "AddItems", ItemID: "A", Code: 0x80004005}
	g := &fakePollingGroup{addErr: want}
	err := runPoll(
		context.Background(),
		&pollConfig{ids: []string{"A"}, interval: time.Millisecond, cycles: 1},
		g,
		&bytes.Buffer{},
		&bytes.Buffer{},
	)
	var hr *opcda.HRESULTError
	if !errors.As(err, &hr) || hr != want || !g.removed || g.reads != 0 {
		t.Fatal(err, g)
	}
}
