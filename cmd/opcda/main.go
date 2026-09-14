// opcda provides discovery, status, browse, read and write commands for the opcda library.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"

	opcda "github.com/dalugm/gopcda"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	if err := run(
		ctx,
		os.Args[1:],
		os.Getenv,
		os.Stdout,
		os.Stderr,
		connect,
		opcda.ResolveProgID,
	); err != nil {
		command := ""
		if len(os.Args) > 1 {
			command = os.Args[1]
		}
		fmt.Fprintln(os.Stderr, formatCommandError(command, err))
		os.Exit(1)
	}
}

func connect(ctx context.Context, cfg opcda.ServerConfig) (server, error) {
	return opcda.Connect(ctx, cfg)
}
