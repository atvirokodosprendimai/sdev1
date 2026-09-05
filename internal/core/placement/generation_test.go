package placement

import (
	"errors"
	"strings"
	"testing"

	"github.com/atvirokodosprendimai/sdev1/internal/core/hlc"
	"github.com/atvirokodosprendimai/sdev1/internal/core/topology"
	"github.com/atvirokodosprendimai/sdev1/internal/core/tx"
)

// mapSource returns an authored map, with the given generation field spliced in.
// An empty generation means the field is omitted entirely.
func mapSource(generation string) string {
	var b strings.Builder
	b.WriteString(`{"version":1,`)
	if generation != "" {
		b.WriteString(`"generation":"` + generation + `",`)
	}
	b.WriteString(`"depth":1,"levels":["datacenter","rack","server"],
	  "root":{"level":"datacenter","name":"dc-1","children":[
	    {"level":"rack","name":"rack-a","children":[
	      {"level":"server","name":"srv-1","weight":100},
	      {"level":"server","name":"srv-2","weight":100}]}]}}`)
	return b.String()
}

func loadSource(t *testing.T, src string) topology.Map {
	t.Helper()
	m, err := topology.Load(strings.NewReader(src))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	return m
}

func generationOf(wall int64, seq uint32) string {
	return topology.EncodeGeneration(tx.TxID{HLC: hlc.Timestamp{Wall: wall}, Seq: seq})
}

func TestPlacementRefusesAMapThatCannotSayWhichItIs(t *testing.T) {
	anonymous := loadSource(t, mapSource(""))
	if anonymous.Placeable() {
		t.Fatal("a map loaded without a generation reports itself placeable")
	}

	leaf := leafFor(t, "planet-3", anonymous.Depth)
	got, err := Resolve(leaf, anonymous)
	if !errors.Is(err, ErrNoGeneration) {
		t.Fatalf("Resolve against a map with no generation = %v, want ErrNoGeneration — a "+
			"placement nobody can reproduce is a segment nobody can find, and a zero generation "+
			"read as 'generation zero' makes every unidentified map the same map", err)
	}
	if len(got) != 0 {
		t.Errorf("Resolve returned %v alongside its refusal", got)
	}

	// ⚠ The same map WITH a generation must resolve. Without this half the test
	// proves only that the fixture was broken.
	named := loadSource(t, mapSource(generationOf(1_000_000_000, 1)))
	if !named.Placeable() {
		t.Fatal("a map loaded with a generation does not report itself placeable")
	}
	targets, err := Resolve(leafFor(t, "planet-3", named.Depth), named)
	if err != nil {
		t.Fatalf("Resolve against the same map with a generation: %v", err)
	}
	if len(targets) == 0 {
		t.Error("Resolve returned no targets for a placeable map")
	}
}

func TestGenerationIsNotTheFormatVersion(t *testing.T) {
	first := loadSource(t, mapSource(generationOf(1_000_000_000, 1)))
	second := loadSource(t, mapSource(generationOf(2_000_000_000, 1)))

	// ⚠ The trap this record exists to remove: a field called Version, meaning
	// the FILE FORMAT, sitting where somebody looks for the map's identity.
	if first.FormatVersion != second.FormatVersion {
		t.Fatalf("the fixtures differ in format version (%d and %d); this test needs them equal "+
			"so that only the generation distinguishes them",
			first.FormatVersion, second.FormatVersion)
	}
	if first.Generation == second.Generation {
		t.Fatal("two maps with different authored generations are indistinguishable — the " +
			"identity is being taken from the format version, which is a constant, so every " +
			"map in the cluster would claim the same generation forever")
	}
	if first.Generation.Compare(second.Generation) >= 0 {
		t.Errorf("generations do not order by transaction: %v does not precede %v",
			first.Generation, second.Generation)
	}
}

func TestAGenerationIsAuthoredNotAssigned(t *testing.T) {
	src := mapSource(generationOf(1_000_000_000, 7))

	// ⚠ The same bytes, twice. A generation minted at load would differ between
	// these two, and the same file would then be a different map in every process
	// that read it — which is the failure a generation exists to fix.
	first := loadSource(t, src)
	second := loadSource(t, src)

	if first.Generation != second.Generation {
		t.Fatalf("the same file loaded twice yielded %v and %v; the generation is being "+
			"assigned at load rather than read from the map",
			first.Generation, second.Generation)
	}
	if !first.Placeable() {
		t.Error("the authored generation did not survive the load")
	}
}
