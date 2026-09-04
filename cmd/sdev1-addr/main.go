// Command sdev1-addr answers "where does this entity live, and why".
//
// It hashes an entity identifier, prints the byte-by-byte descent through the
// address trie, and resolves the leaf against a topology map to the ordered set
// of targets that hold it. It contacts nothing: the whole answer is computed
// from the key and the map, which is the property the addressing model exists to
// provide, and this command is the cheapest way for an operator to see it.
//
// The decision it demonstrates is recorded in docs/adr/ADR-001-address-space.md.
package main

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"text/tabwriter"

	"github.com/urfave/cli/v3"

	"github.com/atvirokodosprendimai/sdev1/internal/core/addr"
	"github.com/atvirokodosprendimai/sdev1/internal/core/placement"
	"github.com/atvirokodosprendimai/sdev1/internal/core/topology"
)

func main() {
	os.Exit(run(context.Background(), os.Args, os.Stdout, os.Stderr))
}

// report is the whole answer, and the single source for both output forms so
// the text and JSON renderings cannot drift apart.
type report struct {
	Entity  string    `json:"entity"`
	Key     string    `json:"key"`
	Depth   uint8     `json:"depth"`
	Leaf    string    `json:"leaf"`
	Descent []descent `json:"descent"`
	Targets []string  `json:"targets"`
	Spread  []string  `json:"spread,omitempty"`
	Spreads string    `json:"spread_level,omitempty"`
	Nearest []string  `json:"nearest,omitempty"`
	From    string    `json:"nearest_from,omitempty"`
	Levels  []string  `json:"levels"`
}

// descent is one hop: the byte of the key consumed at this level and the child
// it selects.
type descent struct {
	Level string `json:"level"`
	Byte  string `json:"byte"`
	Child int    `json:"child"`
}

// run is main's body, taking its streams so tests can drive the real command
// rather than a reimplementation of it. It returns the process exit code.
func run(ctx context.Context, args []string, out, errOut io.Writer) int {
	cmd := &cli.Command{
		Name:  "sdev1-addr",
		Usage: "show where an entity lives in the address trie, and why",
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:     "topology",
				Usage:    "path to a topology map",
				Required: true,
			},
			&cli.StringFlag{
				Name:     "entity",
				Usage:    "entity identifier to place",
				Required: true,
			},
			&cli.StringFlag{
				Name:  "spread-level",
				Usage: "level label to spread targets across, e.g. rack",
			},
			&cli.StringFlag{
				Name:  "from",
				Usage: "node to order targets by proximity to",
			},
			&cli.BoolFlag{
				Name:  "json",
				Usage: "emit the same answer as JSON",
			},
		},
		Action: func(ctx context.Context, c *cli.Command) error {
			r, err := build(c.String("topology"), c.String("entity"),
				c.String("spread-level"), c.String("from"))
			if err != nil {
				return err
			}
			if c.Bool("json") {
				enc := json.NewEncoder(out)
				enc.SetIndent("", "  ")
				return enc.Encode(r)
			}
			return renderText(out, r)
		},
	}
	if err := cmd.Run(ctx, args); err != nil {
		fmt.Fprintln(errOut, "sdev1-addr:", err)
		return 1
	}
	return 0
}

// build computes the whole answer. Every failure is returned rather than
// printed, so a broken topology exits non-zero: a diagnostic that exits 0 on a
// bad input is worse than no diagnostic.
func build(topologyPath, entity, spreadLevel, from string) (report, error) {
	f, err := os.Open(topologyPath)
	if err != nil {
		return report{}, fmt.Errorf("open topology: %w", err)
	}
	defer f.Close()

	m, err := topology.Load(f)
	if err != nil {
		return report{}, fmt.Errorf("load topology: %w", err)
	}

	key := addr.KeyOf(entity)
	leaf, err := addr.Descend(key, m.Depth)
	if err != nil {
		return report{}, fmt.Errorf("descend: %w", err)
	}

	r := report{
		Entity: entity,
		Key:    hex.EncodeToString(key[:]),
		Depth:  m.Depth,
		Leaf:   leaf.String(),
		Levels: m.Levels,
	}
	for i, b := range leaf.Bytes() {
		level := ""
		if i < len(m.Levels) {
			level = m.Levels[i]
		}
		r.Descent = append(r.Descent, descent{
			Level: level,
			Byte:  fmt.Sprintf("0x%02x", b),
			Child: int(b),
		})
	}

	targets, err := placement.Resolve(leaf, m)
	if err != nil {
		return report{}, fmt.Errorf("resolve: %w", err)
	}
	r.Targets = targets

	if spreadLevel != "" {
		idx := m.LevelIndex(spreadLevel)
		if idx < 0 {
			return report{}, fmt.Errorf("spread level %q is not declared by this map (levels: %v)",
				spreadLevel, m.Levels)
		}
		r.Spread = placement.Spread(targets, m, idx)
		r.Spreads = spreadLevel
	}
	if from != "" {
		if _, err := m.Node(from); err != nil {
			return report{}, fmt.Errorf("--from: %w", err)
		}
		r.Nearest = placement.Nearest(targets, from, m)
		r.From = from
	}
	return r, nil
}

// renderText writes the operator-facing form. It reports exactly what the JSON
// form reports; the two are rendered from one value so they cannot disagree.
func renderText(out io.Writer, r report) error {
	tw := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	fmt.Fprintf(tw, "entity\t%s\n", r.Entity)
	fmt.Fprintf(tw, "key\t%s\n", r.Key)
	fmt.Fprintf(tw, "depth\t%d\n", r.Depth)
	fmt.Fprintf(tw, "leaf\t%s\n", r.Leaf)
	if err := tw.Flush(); err != nil {
		return err
	}

	fmt.Fprintln(out, "\ndescent")
	for i, d := range r.Descent {
		fmt.Fprintf(out, "  hop %d  level %-12s byte %s  child %d\n", i+1, d.Level, d.Byte, d.Child)
	}

	fmt.Fprintln(out, "\ntargets")
	for i, t := range r.Targets {
		fmt.Fprintf(out, "  %d. %s\n", i+1, t)
	}
	if r.Spread != nil {
		fmt.Fprintf(out, "\nspread across %s\n", r.Spreads)
		for i, t := range r.Spread {
			fmt.Fprintf(out, "  %d. %s\n", i+1, t)
		}
	}
	if r.Nearest != nil {
		fmt.Fprintf(out, "\nnearest to %s\n", r.From)
		for i, t := range r.Nearest {
			fmt.Fprintf(out, "  %d. %s\n", i+1, t)
		}
	}
	return nil
}
