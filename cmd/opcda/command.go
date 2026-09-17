package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"

	opcda "github.com/dalugm/gopcda"
)

const usage = "usage: opcda resolve PROGID | status | browse | properties ITEM_ID | read ITEM_ID | write ITEM_ID VALUE [TYPE] | poll ITEM_FILE INTERVAL [CYCLES] [cache|device]\nConnection: OPCDA_HOST, OPCDA_CLSID or OPCDA_PROGID, OPCDA_DOMAIN, OPCDA_USERNAME, OPCDA_PASSWORD\nResolve uses the positional ProgID and ignores OPCDA_CLSID and OPCDA_PROGID.\nOptional: OPCDA_TIMEOUT=180s\nWrite types: bool, int8/16/32/64, uint8/16/32/64, float32 (default), float64, string, date, currency, decimal, error, int, uint, empty, null"

type server interface {
	GetServerStatusContext(context.Context) (*opcda.ServerStatus, error)
	BrowseItemIDs(context.Context) ([]string, error)
	ItemProperties(context.Context, string) ([]opcda.ItemProperty, error)
	ReadItem(context.Context, string) (*opcda.ReadResult, error)
	WriteItem(context.Context, string, any) error
	AddGroupContext(context.Context, string, int, float32) (*opcda.Group, error)
	Close(context.Context) error
	Disconnect()
}
type connector func(context.Context, opcda.ServerConfig) (server, error)

func run(
	ctx context.Context,
	args []string,
	getenv func(string) string,
	out, diagnostics io.Writer,
	dial connector,
	resolve func(context.Context, opcda.ServerConfig, string) (string, error),
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
	case "read", "resolve", "properties":
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
	cfg, timeout, err := loadConfig(args[0], getenv)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	// Diagnostics are best effort; result output errors are returned to the caller.
	_, _ = fmt.Fprintf(diagnostics, "Connecting to %s (%s)...\n", cfg.Host, args[0])
	if args[0] == "resolve" {
		id, err := resolve(ctx, cfg, args[1])
		if err != nil {
			return err
		}
		_, err = fmt.Fprintln(out, id)
		return err
	}
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
	case "properties":
		properties, err := srv.ItemProperties(ctx, args[1])
		if err != nil {
			return err
		}
		return printProperties(out, args[1], properties)
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
