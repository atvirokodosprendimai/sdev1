package topology

import (
	"errors"
	"strings"
	"testing"

	"github.com/atvirokodosprendimai/sdev1/internal/core/hlc"
	"github.com/atvirokodosprendimai/sdev1/internal/core/tx"
)

// authoredWith returns a minimal valid map carrying the given generation field
// verbatim. An empty string omits the field.
func authoredWith(generation string) string {
	var b strings.Builder
	b.WriteString(`{"version":1,`)
	if generation != "" {
		b.WriteString(`"generation":"` + generation + `",`)
	}
	b.WriteString(`"depth":1,"levels":["datacenter","server"],
	  "root":{"level":"datacenter","name":"dc-1","children":[
	    {"level":"server","name":"srv-1","weight":100}]}}`)
	return b.String()
}

func TestAMalformedGenerationIsRefused(t *testing.T) {
	valid := EncodeGeneration(tx.TxID{HLC: hlc.Timestamp{Wall: 1_000_000_000}, Seq: 1})

	// Sanity: the well-formed one must load, or every case below passes for the
	// wrong reason.
	m, err := Load(strings.NewReader(authoredWith(valid)))
	if err != nil {
		t.Fatalf("a well-formed generation was refused: %v", err)
	}
	if !m.Placeable() {
		t.Fatal("a well-formed generation did not survive the load")
	}

	cases := map[string]string{
		"not hex":    "zzzz",
		"too short":  "00ff",
		"too long":   valid + "00",
		"odd length": valid[:len(valid)-1],
		"all zeroes": strings.Repeat("0", len(valid)),
	}
	for name, generation := range cases {
		got, err := Load(strings.NewReader(authoredWith(generation)))
		if !errors.Is(err, ErrBadGeneration) {
			t.Errorf("%s: Load = %v, want ErrBadGeneration — a generation that was written and "+
				"misread is exactly the case where somebody believes their placements are "+
				"reproducible and they are not", name, err)
		}
		if got.Placeable() {
			t.Errorf("%s: the refused map reports itself placeable", name)
		}
	}

	// ⚠ And the ABSENT case is not an error: a map may be read to inspect a
	// cluster's shape without ever placing anything. The refusal belongs at
	// placement, where the consequence is.
	absent, err := Load(strings.NewReader(authoredWith("")))
	if err != nil {
		t.Errorf("a map with no generation was refused at load: %v", err)
	}
	if absent.Placeable() {
		t.Error("a map with no generation reports itself placeable")
	}
}
