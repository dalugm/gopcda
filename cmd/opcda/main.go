// opcda provides status, browse, read and write commands for the opcda library.
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"os/signal"
	"strconv"
	"time"

	opcda "github.com/dalugm/gopcda"
)

const usage = "usage: opcda status | browse | read ITEM_ID | write ITEM_ID VALUE [TYPE] | poll ITEM_FILE INTERVAL [CYCLES] [cache|device]\nConnection: OPCDA_HOST, OPCDA_CLSID or OPCDA_PROGID, OPCDA_DOMAIN, OPCDA_USERNAME, OPCDA_PASSWORD\nOptional: OPCDA_TIMEOUT=180s\nWrite types: bool, int8/16/32/64, uint8/16/32/64, float32 (default), float64, string"

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	if err := run(ctx, os.Args[1:], os.Getenv, os.Stdout, os.Stderr, connect); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

type server interface {
	GetServerStatusContext(context.Context) (*opcda.ServerStatus, error)
	BrowseItemIDs(context.Context) ([]string, error)
	ReadItem(context.Context, string) (*opcda.ReadResult, error)
	WriteItem(context.Context, string, any) error
	AddGroupContext(context.Context, string, int, float32) (*opcda.Group, error)
	Close(context.Context) error
	Disconnect()
}
type connector func(context.Context, opcda.ServerConfig) (server, error)

func connect(ctx context.Context, cfg opcda.ServerConfig) (server, error) {
	return opcda.Connect(ctx, cfg)
}

func run(
	ctx context.Context,
	args []string,
	getenv func(string) string,
	out, diagnostics io.Writer,
	dial connector,
) (retErr error) {
	if len(args) == 1 && (args[0] == "--help" || args[0] == "-h" || args[0] == "help") {
		_, err := fmt.Fprintln(out, usage)
		return err
	}
	if len(args) == 0 {
		return fmt.Errorf("%s", usage)
	}
	var writeValue any
	var poll *pollConfig
	switch args[0] {
	case "status", "browse":
		if len(args) != 1 {
			return fmt.Errorf("%s", usage)
		}
	case "read":
		if len(args) != 2 || args[1] == "" {
			return fmt.Errorf("%s", usage)
		}
	case "write":
		if len(args) < 3 || len(args) > 4 || args[1] == "" {
			return fmt.Errorf("%s", usage)
		}
		kind := "float32"
		if len(args) == 4 {
			kind = args[3]
		}
		var err error
		writeValue, err = parseWriteValue(args[2], kind)
		if err != nil {
			return err
		}
	case "poll":
		var err error
		poll, err = parsePoll(args)
		if err != nil {
			return err
		}
	default:
		return fmt.Errorf("unknown command %q\n%s", args[0], usage)
	}
	cfg := opcda.ServerConfig{
		Host:     getenv("OPCDA_HOST"),
		Domain:   getenv("OPCDA_DOMAIN"),
		Username: getenv("OPCDA_USERNAME"),
		Password: getenv("OPCDA_PASSWORD"),
		CLSID:    getenv("OPCDA_CLSID"),
		ProgID:   getenv("OPCDA_PROGID"),
	}
	if cfg.Host == "" || (cfg.CLSID == "" && cfg.ProgID == "") {
		return fmt.Errorf(
			"set OPCDA_HOST and either OPCDA_CLSID or OPCDA_PROGID; credentials use OPCDA_DOMAIN, OPCDA_USERNAME and OPCDA_PASSWORD",
		)
	}
	timeout := 180 * time.Second
	if value := getenv("OPCDA_TIMEOUT"); value != "" {
		var err error
		timeout, err = time.ParseDuration(value)
		if err != nil || timeout <= 0 {
			return fmt.Errorf("OPCDA_TIMEOUT must be a positive duration such as 180s")
		}
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	// Diagnostics are best effort; result output errors are returned to the caller.
	_, _ = fmt.Fprintf(diagnostics, "Connecting to %s (%s)...\n", cfg.Host, args[0])
	srv, err := dial(ctx, cfg)
	if err != nil {
		return err
	}
	defer func() {
		cleanup, done := context.WithTimeout(context.Background(), 10*time.Second)
		defer done()
		retErr = errors.Join(retErr, srv.Close(cleanup))
	}()
	switch args[0] {
	case "poll":
		g, err := srv.AddGroupContext(ctx, "", int(poll.interval.Milliseconds()), 0)
		if err != nil {
			return err
		}
		return runPoll(ctx, poll, g, out, diagnostics)
	case "status":
		status, err := srv.GetServerStatusContext(ctx)
		if err != nil {
			return err
		}
		_, err = fmt.Fprintf(
			out,
			"Server status: vendor=%q version=%q state=%d\n",
			status.VendorInfo,
			status.ProductVersion,
			status.State,
		)
		return err
	case "browse":
		ids, err := srv.BrowseItemIDs(ctx)
		if err != nil {
			return err
		}
		w := bufio.NewWriter(out)
		for _, id := range ids {
			if _, err := fmt.Fprintln(w, id); err != nil {
				return err
			}
		}
		if err := w.Flush(); err != nil {
			return err
		}
		_, err = fmt.Fprintf(diagnostics, "Browsed %d unique ItemIDs.\n", len(ids))
		return err
	case "write":
		if err := srv.WriteItem(ctx, args[1], writeValue); err != nil {
			return err
		}
		return json.NewEncoder(out).Encode(struct {
			ItemID       string `json:"itemId"`
			Value        any    `json:"value"`
			Type         string `json:"type"`
			Acknowledged bool   `json:"acknowledged"`
		}{args[1], writeValue, fmt.Sprintf("%T", writeValue), true})
	case "read":
		result, err := srv.ReadItem(ctx, args[1])
		if err != nil {
			return err
		}
		return printResult(out, result)
	}
	return nil
}

func printResult(out io.Writer, r *opcda.ReadResult) error {
	encoder := json.NewEncoder(out)
	encoder.SetIndent("", "  ")
	return encoder.Encode(struct {
		ItemID      string `json:"itemId"`
		Value       any    `json:"value"`
		Type        string `json:"type"`
		Quality     string `json:"quality"`
		QualityGood bool   `json:"qualityGood"`
		Timestamp   string `json:"timestamp"`
		TimestampMs int64  `json:"timestampMs"`
	}{r.ItemID, r.Value, fmt.Sprintf("%T", r.Value), fmt.Sprintf("0x%04X", uint16(r.Quality)), opcda.QualityIsGood(r.Quality), time.UnixMilli(r.SourceTimestampMs).UTC().Format(time.RFC3339Nano), r.SourceTimestampMs})
}

func parseWriteValue(text, kind string) (any, error) {
	switch kind {
	case "string":
		return text, nil
	case "bool":
		return strconv.ParseBool(text)
	case "int8", "int16", "int32", "int64":
		bits, _ := strconv.Atoi(kind[3:])
		value, err := strconv.ParseInt(text, 10, bits)
		if err != nil {
			return nil, fmt.Errorf("invalid %s value %q: %w", kind, text, err)
		}
		switch bits {
		case 8:
			return int8(value), nil
		case 16:
			return int16(value), nil
		case 32:
			return int32(value), nil
		default:
			return value, nil
		}
	case "uint8", "uint16", "uint32", "uint64":
		bits, _ := strconv.Atoi(kind[4:])
		value, err := strconv.ParseUint(text, 10, bits)
		if err != nil {
			return nil, fmt.Errorf("invalid %s value %q: %w", kind, text, err)
		}
		switch bits {
		case 8:
			return uint8(value), nil
		case 16:
			return uint16(value), nil
		case 32:
			return uint32(value), nil
		default:
			return value, nil
		}
	}

	bits := 32
	switch kind {
	case "float32":
	case "float64":
		bits = 64
	default:
		return nil, fmt.Errorf(
			"unsupported write type %q; use bool, fixed-width integers, float32, float64, or string",
			kind,
		)
	}
	value, err := strconv.ParseFloat(text, bits)
	if err != nil || math.IsNaN(value) || math.IsInf(value, 0) {
		return nil, fmt.Errorf("invalid finite %s value %q", kind, text)
	}
	if bits == 32 {
		return float32(value), nil
	}
	return value, nil
}
