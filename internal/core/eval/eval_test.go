package eval

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/atvirokodosprendimai/sdev1/internal/core/addr"
	"github.com/atvirokodosprendimai/sdev1/internal/core/hlc"
	"github.com/atvirokodosprendimai/sdev1/internal/core/leafstore"
	"github.com/atvirokodosprendimai/sdev1/internal/core/ports"
	"github.com/atvirokodosprendimai/sdev1/internal/core/ql"
	"github.com/atvirokodosprendimai/sdev1/internal/core/temporal"
	"github.com/atvirokodosprendimai/sdev1/internal/core/tx"
)

func testLeaf() addr.LeafID { return addr.TenantFromUint(3).TenantSubtree() }

func at(wall int64) tx.TxID {
	return tx.TxID{HLC: hlc.Timestamp{Wall: wall}, Leaf: testLeaf(), Seq: 1}
}

func fact(entity, attribute, value string, wall int64) ports.Datom {
	return ports.Datom{
		Entity: entity, Attribute: attribute, Value: []byte(value),
		Valid: temporal.Interval{From: 0, To: temporal.Forever},
		TxID:  at(wall), Assert: true,
	}
}

// fakeReader records what it was asked, so a test can see the reads themselves
// rather than only the answer.
//
// ⚠ It deliberately does NOT filter by the snapshot, which a real
// [ports.Reader] does. That is what makes the evaluator's own filter observable:
// against a store the two agree and neither can be seen alone.
// TestASelectRunsAgainstARealLeaf covers the contract-honouring case.
type fakeReader struct {
	datoms map[string][]ports.Datom
	loads  []string
	snaps  []ports.Snapshot
	err    error
}

func (f *fakeReader) Load(_ context.Context, entity string, at ports.Snapshot) ([]ports.Datom, error) {
	f.loads = append(f.loads, entity)
	f.snaps = append(f.snaps, at)
	if f.err != nil {
		return nil, f.err
	}
	return f.datoms[entity], nil
}

func (f *fakeReader) Attributes(_ context.Context, entity string, _ ports.Snapshot) ([]string, error) {
	return nil, nil
}

func parseSelect(t *testing.T, src string) *ql.Select {
	t.Helper()
	stmt, err := ql.Parse(src)
	if err != nil {
		t.Fatalf("Parse(%q): %v", src, err)
	}
	sel, ok := stmt.(*ql.Select)
	if !ok {
		t.Fatalf("Parse(%q) produced %T, want *ql.Select", src, stmt)
	}
	return sel
}

func rowStrings(rows []Row) []string {
	out := make([]string, len(rows))
	for i, r := range rows {
		out[i] = fmt.Sprintf("%s=%s", r.Attribute, r.Value)
	}
	return out
}

func equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// planet3 is an entity carrying two attributes, used where the shape matters more
// than the values.
func planet3() *fakeReader {
	return &fakeReader{datoms: map[string][]ports.Datom{
		"planet-3": {
			fact("planet-3", "mass", "5", 100),
			fact("planet-3", "radius", "6371", 110),
		},
	}}
}

func TestAPredicateThatMatchesNothingReturnsNothing(t *testing.T) {
	// ⚠ This is the behaviour that SHIPPED: the clause parsed, was discarded, and
	// every row came back — no error, no warning, and no way for the caller to
	// tell that the question they asked was not the one answered.
	r := planet3()
	sel := parseSelect(t, `SELECT * FROM planet-3 WHERE mass = "999"`)

	rows, err := Select(context.Background(), r, sel, 1000)
	if err != nil {
		t.Fatalf("Select: %v", err)
	}
	if len(rows) != 0 {
		t.Fatalf("a WHERE that matches nothing returned %v; the clause is being ignored, and the "+
			"caller is given a wider answer than they asked for with no error anywhere",
			rowStrings(rows))
	}
}

func TestAPredicateOnAnUnprojectedAttributeStillFilters(t *testing.T) {
	r := &fakeReader{datoms: map[string][]ports.Datom{
		"planet-7": {
			fact("planet-7", "name", "Terra", 100),
			fact("planet-7", "class", "terrestrial", 110),
		},
	}}

	// The published guide's own example. ⚠ Narrowing to the projection BEFORE
	// testing the predicate leaves nothing to test against, and this returns
	// nothing on data where it should return a row.
	rows, err := Select(context.Background(), r,
		parseSelect(t, `SELECT name FROM planet-7 WHERE class = 'terrestrial'`), 1000)
	if err != nil {
		t.Fatalf("Select: %v", err)
	}
	if want := []string{"name=Terra"}; !equal(rowStrings(rows), want) {
		t.Errorf("got %v, want %v — a predicate must be able to name an attribute the "+
			"projection does not return", rowStrings(rows), want)
	}

	// And it must still filter: the same query against a class that does not match.
	rows, err = Select(context.Background(), r,
		parseSelect(t, `SELECT name FROM planet-7 WHERE class = 'gas giant'`), 1000)
	if err != nil {
		t.Fatalf("Select: %v", err)
	}
	if len(rows) != 0 {
		t.Errorf("got %v for a predicate that does not match, want nothing", rowStrings(rows))
	}
}

func TestASelectReadsOneEntityOnce(t *testing.T) {
	r := &fakeReader{datoms: map[string][]ports.Datom{
		"planet-1": {fact("planet-1", "mass", "1", 100)},
		"planet-2": {fact("planet-2", "mass", "2", 100)},
		"planet-3": {fact("planet-3", "mass", "3", 100)},
	}}

	if _, err := Select(context.Background(), r,
		parseSelect(t, `SELECT * FROM planet-3`), 1000); err != nil {
		t.Fatalf("Select: %v", err)
	}

	// ⚠ Counted, not inferred from the answer. An evaluator that loaded every
	// entity and discarded all but one returns exactly the right rows.
	if want := []string{"planet-3"}; !equal(r.loads, want) {
		t.Errorf("Select performed %v, want exactly %v — a statement that walks a leaf makes "+
			"every query need everything in memory", r.loads, want)
	}
}

func TestAComparisonThatCannotBeMadeIsRefused(t *testing.T) {
	r := &fakeReader{datoms: map[string][]ports.Datom{
		"planet-3": {fact("planet-3", "codename", "blue marble", 100)},
	}}

	rows, err := Select(context.Background(), r,
		parseSelect(t, `SELECT * FROM planet-3 WHERE codename > 5`), 1000)
	if !errors.Is(err, ErrNotComparable) {
		t.Fatalf("comparing a non-numeric value with a number = %v, want ErrNotComparable; "+
			"returning false instead hides a type error inside an ordinary empty result", err)
	}
	if len(rows) != 0 {
		t.Errorf("Select returned %v alongside its error", rowStrings(rows))
	}
}

func TestNumericIsAPropertyOfTheQueryNotTheData(t *testing.T) {
	r := &fakeReader{datoms: map[string][]ports.Datom{
		"planet-3": {fact("planet-3", "v", "10", 100)},
	}}

	// ★ The same stored value, the same operator, two answers — decided by how the
	// literal was WRITTEN. Numerically 10 < 9 is false; as text "10" < "9" is true.
	numeric, err := Select(context.Background(), r,
		parseSelect(t, `SELECT * FROM planet-3 WHERE v < 9`), 1000)
	if err != nil {
		t.Fatalf("numeric Select: %v", err)
	}
	if len(numeric) != 0 {
		t.Errorf("10 < 9 compared numerically returned %v, want nothing", rowStrings(numeric))
	}

	textual, err := Select(context.Background(), r,
		parseSelect(t, `SELECT * FROM planet-3 WHERE v < "9"`), 1000)
	if err != nil {
		t.Fatalf("textual Select: %v", err)
	}
	if len(textual) != 1 {
		t.Errorf(`"10" < "9" compared as text returned %v, want the row — the comparison is `+
			`decided by the query text, not by whether the data happens to parse`, rowStrings(textual))
	}
}

func TestOneSnapshotReachesTheReaderAndTheFilter(t *testing.T) {
	r := &fakeReader{datoms: map[string][]ports.Datom{
		"planet-3": {
			// Visible only from instant 500 onward.
			{
				Entity: "planet-3", Attribute: "mass", Value: []byte("later"),
				Valid: temporal.Interval{From: 500, To: temporal.Forever},
				TxID:  at(100), Assert: true,
			},
			fact("planet-3", "radius", "6371", 110),
		},
	}}

	sel := parseSelect(t, `SELECT * FROM planet-3 AS OF 200`)
	rows, err := Select(context.Background(), r, sel, 9999)
	if err != nil {
		t.Fatalf("Select: %v", err)
	}

	// The reader was handed exactly the instant the clause resolved to — not the
	// clock reading, and not something re-derived.
	if len(r.snaps) != 1 {
		t.Fatalf("the reader was called %d times, want once", len(r.snaps))
	}
	if r.snaps[0].ValidAt != 200 {
		t.Errorf("the reader was handed instant %d, want the 200 the clause resolved to",
			r.snaps[0].ValidAt)
	}

	// And the same instant reached the filter: the datom that starts at 500 is
	// not carried at 200.
	if want := []string{"radius=6371"}; !equal(rowStrings(rows), want) {
		t.Errorf("got %v, want %v — a datom outside the resolved instant is being returned",
			rowStrings(rows), want)
	}
}

func TestARetractedAttributeIsAbsent(t *testing.T) {
	retracted := fact("planet-3", "codename", "blue", 200)
	retracted.Assert = false

	r := &fakeReader{datoms: map[string][]ports.Datom{
		"planet-3": {
			fact("planet-3", "codename", "blue", 100),
			retracted,
			fact("planet-3", "mass", "5", 110),
		},
	}}

	rows, err := Select(context.Background(), r, parseSelect(t, `SELECT * FROM planet-3`), 1000)
	if err != nil {
		t.Fatalf("Select: %v", err)
	}
	if want := []string{"mass=5"}; !equal(rowStrings(rows), want) {
		t.Errorf("got %v, want %v — a retraction suppresses its attribute rather than "+
			"resurfacing the assertion it withdrew", rowStrings(rows), want)
	}
}

func TestEveryOperatorFilters(t *testing.T) {
	r := &fakeReader{datoms: map[string][]ports.Datom{
		"planet-3": {fact("planet-3", "v", "5", 100)},
	}}

	// ⚠ Every operator, with a case that matches and one that does not. An
	// operator accepted and ignored looks exactly like one that matched, so
	// testing a single operator proves nothing about the other six.
	cases := []struct {
		predicate string
		want      bool
	}{
		{`v = 5`, true}, {`v = 6`, false},
		{`v == 5`, true}, {`v == 6`, false},
		{`v != 6`, true}, {`v != 5`, false},
		{`v < 6`, true}, {`v < 5`, false},
		{`v <= 5`, true}, {`v <= 4`, false},
		{`v > 4`, true}, {`v > 5`, false},
		{`v >= 5`, true}, {`v >= 6`, false},
	}
	for _, c := range cases {
		src := `SELECT * FROM planet-3 WHERE ` + c.predicate
		rows, err := Select(context.Background(), r, parseSelect(t, src), 1000)
		if err != nil {
			t.Errorf("%s: %v", src, err)
			continue
		}
		if got := len(rows) > 0; got != c.want {
			t.Errorf("%s returned %d rows, want matched=%t", src, len(rows), c.want)
		}
	}
}

func TestASelectRunsAgainstARealLeaf(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()

	store, err := leafstore.Open(dir, testLeaf())
	if err != nil {
		t.Fatalf("leafstore.Open: %v", err)
	}
	defer func() { _ = store.Close() }()

	if err := store.Append(ctx,
		fact("planet-3", "mass", "5", 100),
		fact("planet-3", "radius", "6371", 110),
	); err != nil {
		t.Fatalf("Append: %v", err)
	}
	if err := store.Seal(ctx); err != nil {
		t.Fatalf("Seal: %v", err)
	}

	// ★ The port is the contract. The same statement, against a real leaf on a
	// disk rather than the fake, must give the same answer — otherwise the tests
	// above are asserting the fake's shape.
	sel := parseSelect(t, `SELECT * FROM planet-3 WHERE mass = 5`)
	rows, err := Select(ctx, store, sel, 1000)
	if err != nil {
		t.Fatalf("Select against a leaf: %v", err)
	}
	if want := []string{"mass=5", "radius=6371"}; !equal(rowStrings(rows), want) {
		t.Errorf("got %v, want %v", rowStrings(rows), want)
	}

	narrow, err := Select(ctx, store, parseSelect(t, `SELECT * FROM planet-3 WHERE mass = 999`), 1000)
	if err != nil {
		t.Fatalf("Select against a leaf: %v", err)
	}
	if len(narrow) != 0 {
		t.Errorf("a WHERE that matches nothing returned %v against a real leaf", rowStrings(narrow))
	}
}
