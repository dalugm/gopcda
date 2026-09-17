package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"

	opcda "github.com/dalugm/gopcda"
)

type pollConfig struct {
	ids      []string
	interval time.Duration
	cycles   int
	cache    bool
}

func parsePoll(args []string) (*pollConfig, error) {
	if len(args) < 3 || len(args) > 5 {
		return nil, errors.New("usage: opcda poll ITEM_FILE INTERVAL [CYCLES] [cache|device]")
	}
	interval, err := time.ParseDuration(args[2])
	if err != nil || interval < time.Millisecond || interval > time.Hour {
		return nil, errors.New("poll interval must be between 1ms and 1h")
	}
	cycles := 20
	if len(args) >= 4 {
		cycles, err = strconv.Atoi(args[3])
		if err != nil || cycles < 1 {
			return nil, errors.New("poll cycles must be positive")
		}
	}
	cache := true
	if len(args) == 5 {
		switch args[4] {
		case "cache":
		case "device":
			cache = false
		default:
			return nil, errors.New("poll source must be cache or device")
		}
	}
	data, err := os.ReadFile(args[1])
	if err != nil {
		return nil, err
	}
	ids := []string{}
	seen := map[string]bool{}
	for _, line := range strings.Split(string(data), "\n") {
		id := strings.TrimSpace(line)
		if id != "" && !seen[id] {
			ids = append(ids, id)
			seen[id] = true
		}
	}
	if len(ids) == 0 {
		return nil, errors.New("item file is empty")
	}
	return &pollConfig{ids: ids, interval: interval, cycles: cycles, cache: cache}, nil
}

type pollingGroup interface {
	AddItems(context.Context, []string) ([]*opcda.Item, error)
	Read(context.Context, bool) ([]opcda.ReadResult, error)
	Remove(context.Context) error
	RevisedUpdateRate() time.Duration
}

func runPoll(
	ctx context.Context,
	cfg *pollConfig,
	group pollingGroup,
	out, diagnostics io.Writer,
) (retErr error) {
	defer func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		retErr = errors.Join(retErr, group.Remove(cleanup))
	}()
	items, err := group.AddItems(ctx, cfg.ids)
	if err != nil {
		return err
	}
	for _, item := range items {
		if item.Error != nil {
			return fmt.Errorf("AddItems %s: %w", item.ItemID, item.Error)
		}
	}
	// Diagnostics are best effort; result output errors are returned to the caller.
	_, _ = fmt.Fprintf(
		diagnostics,
		"Polling %d items; requested %s, revised %s; cache=%t\n",
		len(items),
		cfg.interval,
		group.RevisedUpdateRate(),
		cfg.cache,
	)
	ticker := time.NewTicker(cfg.interval)
	defer ticker.Stop()
	durations := make([]float64, 0, cfg.cycles)
	missed := 0
	good, bad, failed := 0, 0, 0
	firstError := ""
	for i := 0; i < cfg.cycles; i++ {
		if i > 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-ticker.C:
			}
		}
		start := time.Now()
		values, err := group.Read(ctx, cfg.cache)
		elapsed := time.Since(start)
		if err != nil {
			return err
		}
		if len(values) != len(cfg.ids) {
			return fmt.Errorf("read count %d, expected %d", len(values), len(cfg.ids))
		}
		durations = append(durations, float64(elapsed)/float64(time.Millisecond))
		if elapsed > cfg.interval {
			missed++
		}
		for _, v := range values {
			if v.Error != nil {
				failed++
				if firstError == "" {
					firstError = v.ItemID + ": " + v.Error.Error()
				}
			} else if opcda.QualityIsGood(v.Quality) {
				good++
			} else {
				bad++
			}
		}
	}
	slices.Sort(durations)
	sum := 0.0
	for _, v := range durations {
		sum += v
	}
	return json.NewEncoder(out).Encode(struct {
		Items          int     `json:"items"`
		Cycles         int     `json:"cycles"`
		RequestedMS    int64   `json:"requestedMs"`
		RevisedMS      int64   `json:"revisedMs"`
		Cache          bool    `json:"cache"`
		MeanMS         float64 `json:"meanMs"`
		P95MS          float64 `json:"p95Ms"`
		MaxMS          float64 `json:"maxMs"`
		DeadlineMisses int     `json:"deadlineMisses"`
		Good           int     `json:"goodSamples"`
		Bad            int     `json:"badQualitySamples"`
		Failed         int     `json:"failedSamples"`
		FirstError     string  `json:"firstError,omitempty"`
	}{len(items), cfg.cycles, cfg.interval.Milliseconds(), group.RevisedUpdateRate().Milliseconds(), cfg.cache, sum / float64(len(durations)), durations[(len(durations)*95+99)/100-1], durations[len(durations)-1], missed, good, bad, failed, firstError})
}
