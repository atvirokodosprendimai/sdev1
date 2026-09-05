// Command sdev1-serve puts one leaf behind a socket.
//
// ⚠ IT SERVES READS ONLY, AND NOTHING AUTHENTICATES. Anyone who can reach the
// address can read any entity the leaf holds. There is no leader yet, so a write
// served here would be unfenced (ADR-009) and committed at a durability nobody
// has (ADR-020) — it is refused by name instead. Do not expose this to a network
// the operator does not own.
//
// See docs/adr/ADR-045-a-leaf-is-served-over-a-stream.md.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/urfave/cli/v3"

	"github.com/atvirokodosprendimai/sdev1/internal/core/addr"
	"github.com/atvirokodosprendimai/sdev1/internal/core/leafstore"
	"github.com/atvirokodosprendimai/sdev1/internal/core/routing"
	"github.com/atvirokodosprendimai/sdev1/internal/core/serve"
	"github.com/atvirokodosprendimai/sdev1/internal/core/wire"
)

func main() {
	cmd := &cli.Command{
		Name:  "sdev1-serve",
		Usage: "serve one leaf over a stream",
		Description: "Reads only. A write is refused by name, because there is no leader to\n" +
			"fence one and no durability tier to commit it at. NOTHING AUTHENTICATES:\n" +
			"anyone who can reach --addr can read anything the leaf holds.\n\n" +
			"--leaf is this node's own subtree, as depth:hex-prefix (for example 3:0a1b2c).\n" +
			"An incoming key is descended to that depth; if it lands here the read is\n" +
			"served, and if it does not the node answers a redirect from --route.",
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:  "addr",
				Usage: "address to listen on",
				Value: "127.0.0.1:7845",
			},
			&cli.StringFlag{
				Name:     "dir",
				Usage:    "directory holding the leaf's segments",
				Required: true,
			},
			&cli.StringFlag{
				Name:     "leaf",
				Usage:    "the leaf this node holds, as depth:hex-prefix",
				Required: true,
			},
			&cli.StringSliceFlag{
				Name: "route",
				Usage: "a route this node can redirect to, as depth:hex-prefix=epoch@host:port[,host:port]; " +
					"repeat the flag for several",
			},
			&cli.DurationFlag{
				Name:  "read-timeout",
				Usage: "how long one request may take to arrive",
				Value: 10 * time.Second,
			},
			&cli.DurationFlag{
				Name:  "write-timeout",
				Usage: "how long one response may take to leave",
				Value: 10 * time.Second,
			},
			&cli.IntFlag{
				Name:  "max-frame",
				Usage: "largest frame accepted or sent, in bytes",
				Value: wire.MaxFrame,
			},
			&cli.StringFlag{
				Name:     "cert",
				Usage:    "this node's certificate, in PEM",
				Required: true,
			},
			&cli.StringFlag{
				Name:     "key",
				Usage:    "this node's private key, in PEM",
				Required: true,
			},
			&cli.StringFlag{
				Name: "ca",
				Usage: "the certificate authority that signs this cluster's certificates. " +
					"⚠ Declared, never the host's trust store — nil there means every public CA " +
					"on the machine may mint a peer",
				Required: true,
			},
		},
		Action: run,
	}
	if err := cmd.Run(context.Background(), os.Args); err != nil {
		fmt.Fprintln(os.Stderr, "sdev1-serve:", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, cmd *cli.Command) error {
	leaf, err := addr.ParseLeafID(cmd.String("leaf"))
	if err != nil {
		return fmt.Errorf("--leaf: %w", err)
	}

	table, err := routes(cmd.StringSlice("route"))
	if err != nil {
		return err
	}

	store, err := leafstore.Open(cmd.String("dir"), leaf)
	if err != nil {
		return err
	}
	defer func() { _ = store.Close() }()

	srv, err := serve.NewServer(serve.Options{
		Addr:         cmd.String("addr"),
		Leaf:         leaf,
		Store:        store,
		Table:        table,
		ReadTimeout:  cmd.Duration("read-timeout"),
		WriteTimeout: cmd.Duration("write-timeout"),
		MaxFrame:     int(cmd.Int("max-frame")),
		TLS: serve.TLSConfig{
			CertFile: cmd.String("cert"),
			KeyFile:  cmd.String("key"),
			CAFile:   cmd.String("ca"),
		},
	})
	if err != nil {
		return err
	}

	fmt.Fprintf(os.Stderr, "sdev1-serve: leaf %s on %s, %d route(s), reads only, unauthenticated\n",
		leaf, srv.Addr(), table.Len())

	// A signal closes the listener, which ends Serve and waits for the exchange
	// in flight. ⚠ The tail is NOT sealed on the way out: ADR-020 fixed the
	// commit point at memory replicas, and sealing here would make it depend on
	// how the process happened to end.
	stopping, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()
	go func() {
		<-stopping.Done()
		_ = srv.Close()
	}()

	return srv.Serve(ctx)
}

// routes builds this node's table from the --route flags.
//
// Each is `depth:hex-prefix=epoch@host:port[,host:port]`. ⚠ The epoch is required
// rather than defaulted: a redirect without one cannot be ordered, and ADR-008
// rule 5 is what stops two stale nodes bouncing a client between them forever.
func routes(specs []string) (*routing.Table, error) {
	table := routing.NewTable()
	for _, spec := range specs {
		prefixText, rest, ok := strings.Cut(spec, "=")
		if !ok {
			return nil, fmt.Errorf("--route %q: want depth:hex-prefix=epoch@host:port", spec)
		}
		epochText, hopsText, ok := strings.Cut(rest, "@")
		if !ok {
			return nil, fmt.Errorf("--route %q: want an epoch before @, as depth:hex=epoch@host:port", spec)
		}

		prefix, err := addr.ParseLeafID(prefixText)
		if err != nil {
			return nil, fmt.Errorf("--route %q: %w", spec, err)
		}
		epoch, err := strconv.ParseUint(epochText, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("--route %q: epoch %q: %w", spec, epochText, err)
		}
		hops := strings.Split(hopsText, ",")
		if err := table.Insert(routing.Route{Prefix: prefix, NextHops: hops, Epoch: epoch}); err != nil {
			return nil, fmt.Errorf("--route %q: %w", spec, err)
		}
	}
	return table, nil
}
