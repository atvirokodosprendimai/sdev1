// Command sdev1-ql runs query-language statements against an in-memory session.
//
// ⚠ It is a demonstration, not a database. Everything it stores is lost when it
// exits. See internal/core/session for what that means and why it exists.
package main

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/urfave/cli/v3"

	"github.com/atvirokodosprendimai/sdev1/internal/core/addr"
	"github.com/atvirokodosprendimai/sdev1/internal/core/leafstore"
	"github.com/atvirokodosprendimai/sdev1/internal/core/session"
)

func main() {
	cmd := &cli.Command{
		Name:  "sdev1-ql",
		Usage: "run query-language statements against an in-memory session",
		Description: "Statements come from --statements, from a --file, or from standard input,\n" +
			"one per line. Everything is held in memory and lost on exit: this shows what\n" +
			"the language means, not what the storage engine does.",
		Flags: []cli.Flag{
			&cli.StringSliceFlag{
				Name:  "statements",
				Usage: "a statement to run; repeat the flag to run several in order",
			},
			&cli.StringFlag{
				Name:  "file",
				Usage: "read statements from a file, one per line",
			},
			&cli.UintFlag{
				Name:  "tenant",
				Usage: "tenant identifier (0-65535)",
				Value: 7,
			},
			&cli.IntFlag{
				Name: "clock",
				Usage: "start the clock here and advance it by one per statement, so transaction " +
					"identifiers are small and reproducible; omit to use the wall clock",
			},
			&cli.StringFlag{
				Name: "dir",
				Usage: "keep the leaf in this directory, so facts survive the process; " +
					"omit to hold everything in memory",
			},
		},
		Action: run,
	}
	if err := cmd.Run(context.Background(), os.Args); err != nil {
		fmt.Fprintln(os.Stderr, "sdev1-ql:", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, cmd *cli.Command) error {
	statements, err := collect(cmd)
	if err != nil {
		return err
	}
	if len(statements) == 0 {
		return fmt.Errorf("no statements: pass --statements, --file, or pipe them in")
	}

	tenant := addr.TenantFromUint(uint16(cmd.Uint("tenant")))
	s, err := open(tenant, clock(int64(cmd.Int("clock"))), cmd.String("dir"))
	if err != nil {
		return err
	}
	defer func() { _ = s.Close() }()

	for _, src := range statements {
		result, err := s.Run(src)
		if err != nil {
			// Report and keep going: a transcript is more useful than a halt,
			// and a refusal is part of what a reader is here to see.
			fmt.Printf("%s\n  refused: %v\n\n", src, err)
			continue
		}
		report(result)
	}

	// ⚠ AFTER the statements and before the process exits. Sealing earlier
	// succeeds while writing nothing, and the run reports success either way —
	// only a second process reading the directory can tell the difference.
	if err := s.Seal(ctx); err != nil {
		return fmt.Errorf("sealing: %w", err)
	}
	return nil
}

// open returns a session, durable when a directory was named.
func open(tenant addr.TenantID, now func() int64, dir string) (*session.Session, error) {
	if dir == "" {
		return session.New(tenant, now), nil
	}
	store, err := leafstore.Open(dir, tenant.TenantSubtree())
	if err != nil {
		return nil, err
	}
	return session.Open(tenant, now, store)
}

// clock returns the instant source for a session.
//
// ★ With --clock, the wall ADVANCES by one on every reading rather than being
// frozen. A frozen wall would pin every transaction to the same instant and let
// only the logical counter move, which makes `TRANSACTION <n>` useless as a
// bound — every datom would sort after it. Advancing gives each write a distinct,
// small, copy-pasteable identifier, which is what makes a bitemporal example
// teachable at all.
func clock(start int64) func() int64 {
	if start == 0 {
		return func() int64 { return time.Now().UnixNano() }
	}
	tick := start
	return func() int64 {
		v := tick
		tick++
		return v
	}
}

// collect gathers statements from the flags and from standard input.
func collect(cmd *cli.Command) ([]string, error) {
	var out []string
	out = append(out, cmd.StringSlice("statements")...)

	if path := cmd.String("file"); path != "" {
		f, err := os.Open(path)
		if err != nil {
			return nil, err
		}
		defer f.Close()
		out = append(out, readLines(f)...)
	}

	// ⚠ Standard input is a FALLBACK, read only when nothing else supplied a
	// statement. Reading it whenever it is not a terminal hangs forever whenever
	// stdin is an open pipe nobody writes to — which is how every test harness
	// and CI runner invokes a binary. Found by this task's own acceptance fence
	// blocking: the check that runs the binary is what caught it.
	if len(out) == 0 {
		if info, err := os.Stdin.Stat(); err == nil && info.Mode()&os.ModeCharDevice == 0 {
			out = append(out, readLines(os.Stdin)...)
		}
	}
	return out, nil
}

// readLines returns non-empty, non-comment lines.
func readLines(f *os.File) []string {
	var out []string
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "--") {
			continue
		}
		out = append(out, line)
	}
	return out
}

// report prints what one statement did.
func report(r session.Result) {
	fmt.Println(r.Statement)

	switch {
	case r.Wrote != nil:
		verb := "retracted"
		if r.Wrote.Assert {
			verb = "asserted"
		}
		fmt.Printf("  %s  %s %s = %s\n", verb, r.Wrote.Entity, r.Wrote.Attribute, r.Wrote.Value)
		fmt.Printf("  valid   %s\n", r.Wrote.Valid)
		fmt.Printf("  txn     %s\n", r.Wrote.TxID)

	case r.Reached != nil:
		for _, p := range r.Reached {
			fmt.Printf("  %s%s (depth %d)\n", strings.Repeat("  ", p.Depth-1), p.Entity, p.Depth)
		}

	case r.Hits != nil || r.Facets != nil:
		if len(r.Hits) == 0 {
			fmt.Println("  no hits")
		}
		for i, h := range r.Hits {
			fmt.Printf("  %d. %-12s score %.3f\n", i+1, h.Posting.Subject, h.Score)
		}
		for _, f := range r.Facets {
			fmt.Printf("  facet %s (%d matched)\n", f.Attribute, f.Total)
			for _, c := range f.Counts {
				fmt.Printf("    %-14s %d\n", c.Value, c.N)
			}
		}

	default:
		if len(r.Rows) == 0 {
			fmt.Println("  no rows")
		}
		for _, row := range r.Rows {
			fmt.Printf("  %-10s %-12s %s\n", row.Entity, row.Attribute, row.Value)
		}
	}
	fmt.Println()
}
