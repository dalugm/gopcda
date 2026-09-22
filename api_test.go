package opcda

import (
	"context"
	"errors"
	"math"
	"testing"
	"time"
)

type apiConn struct {
	connection
	rate  int64
	cache bool
	calls int
}

func (c *apiConn) addGroup(_ context.Context, _ string, rate int64, _ float32) (*Group, error) {
	c.calls++
	c.rate = rate
	return &Group{updateRateMs: uint32(rate)}, nil
}

func (c *apiConn) readGroup(_ context.Context, _ int, cache bool) ([]ReadResult, error) {
	c.calls++
	c.cache = cache
	return nil, nil
}

func (c *apiConn) read(_ context.Context, _ int, _ []string, cache bool) ([]ReadResult, error) {
	c.calls++
	c.cache = cache
	return nil, nil
}

func TestAddGroupDuration(t *testing.T) {
	for _, tc := range []struct {
		rate  time.Duration
		valid bool
	}{
		{-time.Millisecond, false},
		{0, false},
		{time.Nanosecond, false},
		{1500 * time.Microsecond, false},
		{time.Millisecond, true},
		{500 * time.Millisecond, true},
		{math.MaxUint32 * time.Millisecond, true},
		{(math.MaxUint32 + 1) * time.Millisecond, false},
	} {
		rate := tc.rate
		t.Run(rate.String(), func(t *testing.T) {
			c := &apiConn{}
			s := &Server{ctx: t.Context(), conn: c}
			g, err := s.AddGroup(t.Context(), "test", rate, 0)
			if !tc.valid {
				if err == nil || g != nil || c.calls != 0 {
					t.Fatalf("invalid duration admitted: group=%v err=%v calls=%d", g, err, c.calls)
				}
				return
			}
			if err != nil || c.calls != 1 || c.rate != rate.Milliseconds() ||
				g.RevisedUpdateRate() != rate {
				t.Fatalf(
					"duration not preserved: group=%v err=%v wire milliseconds=%d",
					g,
					err,
					c.rate,
				)
			}
		})
	}
}

func TestGroupReadSource(t *testing.T) {
	for _, source := range []ReadSource{0, SourceCache, SourceDevice, 3, math.MaxUint32} {
		for _, subset := range []bool{false, true} {
			c := &apiConn{}
			g := &Group{server: &Server{ctx: t.Context(), conn: c}}
			var err error
			if subset {
				_, err = g.ReadItems(t.Context(), nil, source)
			} else {
				_, err = g.Read(t.Context(), source)
			}
			if source != SourceCache && source != SourceDevice {
				if err == nil || c.calls != 0 {
					t.Fatalf("invalid source %d admitted: err=%v calls=%d", source, err, c.calls)
				}
			} else if err != nil || c.calls != 1 || c.cache != (source == SourceCache) {
				t.Fatalf(
					"source %d not preserved: err=%v cache=%v calls=%d",
					source,
					err,
					c.cache,
					c.calls,
				)
			}
		}
	}
}

func TestAddGroupCanceledBeforeTransport(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	c := &apiConn{}
	s := &Server{ctx: t.Context(), conn: c}
	if _, err := s.AddGroup(
		ctx,
		"test",
		time.Second,
		0,
	); !errors.Is(err, context.Canceled) ||
		c.calls != 0 {
		t.Fatalf("canceled setup admitted: err=%v calls=%d", err, c.calls)
	}
}
