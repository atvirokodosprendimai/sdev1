package link

import (
	"errors"
	"reflect"
	"testing"

	"github.com/atvirokodosprendimai/sdev1/internal/core/temporal"
)

// graphAt is a fixture whose SHAPE differs between instants.
//
// ⚠ That is the whole point. A same-instant test on a static graph proves
// nothing, because every instant gives the same answer and a per-hop
// implementation passes.
type graphAt struct {
	// edges maps an instant to the graph as it stood then.
	edges map[int64]map[string][]string
	// asked records every snapshot the walk resolved with, so the test can
	// assert they were all identical rather than only checking the answer.
	asked []temporal.Query
	// values lets a resolver be built from typed values, for the kind test.
	values map[string][]Value
}

func (g *graphAt) References(entity string, at temporal.Query) ([]string, error) {
	g.asked = append(g.asked, at)

	if g.values != nil {
		var out []string
		for _, v := range g.values[entity] {
			if target, ok := v.Target(); ok {
				out = append(out, target)
			}
		}
		return out, nil
	}

	instant := int64(0)
	if at.ValidAt != nil {
		instant = *at.ValidAt
	}
	return g.edges[instant][entity], nil
}

func snapshotAt(instant int64) temporal.Query {
	return temporal.Query{ValidAt: &instant}
}

// TestEveryHopResolvesAtOneInstant is ADR-023's falsifier.
//
// The graph is deliberately different at the two instants:
//
//	at 100:  root -> a -> a1
//	at 200:  root -> a -> a2
//
// A walk at 100 must return exactly {a, a1}. A per-hop implementation reading the
// root at 100 and its children at "now" would return {a, a2} — a tree assembled
// from two instants, in which every node is real and which never existed.
func TestEveryHopResolvesAtOneInstant(t *testing.T) {
	g := &graphAt{edges: map[int64]map[string][]string{
		100: {"root": {"a"}, "a": {"a1"}},
		200: {"root": {"a"}, "a": {"a2"}},
	}}

	got, err := Walk(g, "root", snapshotAt(100), 5)
	if err != nil {
		t.Fatalf("Walk: %v", err)
	}
	want := []Path{{Entity: "a", Depth: 1}, {Entity: "a1", Depth: 2}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Walk at 100 = %+v, want %+v.\nAn answer containing a2 is a tree assembled from "+
			"two instants: every node in it is real, and the shape never existed at any moment.", got, want)
	}

	// ⚠ Asserting only the answer is weaker than it looks — an implementation
	// could reach it by luck on this fixture. Every snapshot the resolver saw
	// must be the same one.
	if len(g.asked) < 2 {
		t.Fatalf("the walk resolved %d times; it cannot demonstrate a shared snapshot", len(g.asked))
	}
	for i, q := range g.asked {
		if q.ValidAt == nil || *q.ValidAt != 100 {
			t.Fatalf("hop %d resolved at %v, want 100 — every hop of one walk must use one snapshot",
				i, q.ValidAt)
		}
	}

	// The other instant gives the other answer, so the fixture really does
	// differ and the test above is not passing on a static graph.
	later, err := Walk(g, "root", snapshotAt(200), 5)
	if err != nil {
		t.Fatalf("Walk: %v", err)
	}
	if reflect.DeepEqual(later, want) {
		t.Fatal("the fixture returns the same graph at both instants, so this test could not " +
			"detect a per-hop resolution at all")
	}
}

// TestAReferenceIsAStoredKindNotAGuess checks edges come from the kind field.
func TestAReferenceIsAStoredKindNotAGuess(t *testing.T) {
	// "planet-9" appears twice: once as a LITERAL that merely looks like an
	// entity name, once as a real reference.
	g := &graphAt{values: map[string][]Value{
		"root":     {Literal([]byte("planet-9")), Ref("planet-1")},
		"planet-1": {},
	}}

	got, err := Walk(g, "root", snapshotAt(100), 3)
	if err != nil {
		t.Fatalf("Walk: %v", err)
	}
	want := []Path{{Entity: "planet-1", Depth: 1}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Walk = %+v, want %+v — a literal whose bytes spell an entity name must not be "+
			"followed, or every string resembling an identifier becomes an accidental edge", got, want)
	}

	// And the value model says so directly.
	if _, ok := Literal([]byte("planet-9")).Target(); ok {
		t.Fatal("a literal reported itself as a reference")
	}
	target, ok := Ref("planet-9").Target()
	if !ok || target != "planet-9" {
		t.Fatalf("a reference reported %q, %v", target, ok)
	}
	if len(Kinds()) != 2 {
		t.Fatalf("Kinds returns %d values, want exactly 2", len(Kinds()))
	}
	if KindUnset != 0 {
		t.Fatal("the zero Kind is not KindUnset, so a zero-valued Value would behave like a real kind")
	}
}

// TestACycleIsReportedNotTruncated checks a loop is named.
//
// ⚠ The fixture uses a THREE-node loop, not a self-loop. A walk that only checks
// "did I just come from here" passes a self-loop and runs forever on this one.
func TestACycleIsReportedNotTruncated(t *testing.T) {
	g := &graphAt{edges: map[int64]map[string][]string{
		100: {"root": {"a"}, "a": {"b"}, "b": {"root"}},
	}}

	got, err := Walk(g, "root", snapshotAt(100), 10)
	if !errors.Is(err, ErrCycle) {
		t.Fatalf("Walk over a loop returned (%+v, %v), want ErrCycle — a truncated path reads "+
			"exactly like a complete one", got, err)
	}
	if got != nil {
		t.Fatalf("a refused walk still returned a path: %+v", got)
	}

	// Positive control: the same shape without the closing edge walks fine, so
	// the refusal is about the cycle and not about the fixture.
	g2 := &graphAt{edges: map[int64]map[string][]string{
		100: {"root": {"a"}, "a": {"b"}},
	}}
	if _, err := Walk(g2, "root", snapshotAt(100), 10); err != nil {
		t.Fatalf("an acyclic walk was refused: %v", err)
	}
}

// TestAWalkRefusesAnUnboundedDepth checks the bound is required.
func TestAWalkRefusesAnUnboundedDepth(t *testing.T) {
	g := &graphAt{edges: map[int64]map[string][]string{100: {"root": {"a"}}}}

	for _, depth := range []int{0, -1} {
		if _, err := Walk(g, "root", snapshotAt(100), depth); !errors.Is(err, ErrDepthRequired) {
			t.Fatalf("Walk with depth %d returned %v, want ErrDepthRequired — an unbounded walk "+
				"over a graph the caller does not control is a scan they did not ask for", depth, err)
		}
	}
	if _, err := Walk(g, "root", snapshotAt(100), 1); err != nil {
		t.Fatalf("a walk with a positive depth was refused: %v", err)
	}
}

// TestAMissingRetractedAndErasedTargetAreOneAnswer checks a traversal cannot be
// used to ask whether a subject was erased.
func TestAMissingRetractedAndErasedTargetAreOneAnswer(t *testing.T) {
	// Three roots, each pointing at a target the resolver cannot resolve — for
	// three different reasons the resolver deliberately does not distinguish.
	g := &graphAt{edges: map[int64]map[string][]string{
		100: {
			"never-existed": {},
			"retracted":     {},
			"erased":        {},
		},
	}}

	answers := make([][]Path, 0, 3)
	for _, root := range []string{"never-existed", "retracted", "erased"} {
		got, err := Walk(g, root, snapshotAt(100), 3)
		if err != nil {
			t.Fatalf("Walk(%q): %v — an unresolvable target must be an ordinary absence, not an "+
				"error; an error here rebuilds the existence oracle crypto-shredding removes", root, err)
		}
		answers = append(answers, got)
	}

	for i := 1; i < len(answers); i++ {
		if !reflect.DeepEqual(answers[0], answers[i]) {
			t.Fatalf("the three unresolvable cases differ: %+v vs %+v.\nThey must be byte-identical, "+
				"or a caller can ask whether a subject was erased by walking to it.", answers[0], answers[i])
		}
	}
}

// TestWalkRespectsItsDepthBound checks the bound cuts at the right place.
func TestWalkRespectsItsDepthBound(t *testing.T) {
	g := &graphAt{edges: map[int64]map[string][]string{
		100: {"root": {"a"}, "a": {"b"}, "b": {"c"}, "c": {"d"}},
	}}

	got, err := Walk(g, "root", snapshotAt(100), 2)
	if err != nil {
		t.Fatalf("Walk: %v", err)
	}
	want := []Path{{Entity: "a", Depth: 1}, {Entity: "b", Depth: 2}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Walk with depth 2 = %+v, want %+v — the bound keeps the prefix nearest the root",
			got, want)
	}

	full, err := Walk(g, "root", snapshotAt(100), 10)
	if err != nil {
		t.Fatalf("Walk: %v", err)
	}
	if len(full) != 4 {
		t.Fatalf("an unbounded-enough walk reached %d entities, want 4", len(full))
	}
}
