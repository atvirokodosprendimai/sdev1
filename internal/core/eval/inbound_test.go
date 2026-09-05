package eval

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/atvirokodosprendimai/sdev1/internal/core/leafstore"
	"github.com/atvirokodosprendimai/sdev1/internal/core/ports"
	"github.com/atvirokodosprendimai/sdev1/internal/core/temporal"
)

// link is an asserted reference datom: `entity attribute = ->target`.
func link(entity, attribute, target string, wall int64) ports.Datom {
	d := fact(entity, attribute, target, wall)
	d.IsReference = true
	return d
}

// unlink is the retraction of one, valid FROM the given instant.
//
// ⚠ The business bound is the point. A retraction valid from 0 says the link
// never held, which is a different fact from "it stopped holding at 200" — and
// only the second is what leaving a set means. Without the bound, `AS OF 150`
// cannot distinguish them and the bitemporal half of this test proves nothing.
func unlink(entity, attribute, target string, wall int64) ports.Datom {
	d := link(entity, attribute, target, wall)
	d.Assert = false
	d.Valid = temporal.Interval{From: wall, To: temporal.Forever}
	return d
}

// scanningReader is a fakeReader that can also say what points at an entity.
//
// ⚠ Its candidate list is deliberately CRUDE: it reports any entity that has
// ever named the target in a reference, asserted or not. That is what a real
// appended index behaves like — it never un-proposes — and it is the only shape
// in which skipping confirmation actually fails.
type scanningReader struct {
	*fakeReader
	// order, when set, is the candidate order returned instead of a sorted one,
	// so a test can prove the evaluator does not depend on the source's order.
	order []string
	scans int
}

func (s *scanningReader) Referrers(_ context.Context, target string, _ ports.Snapshot) ([]string, error) {
	s.scans++
	if s.order != nil {
		return append([]string(nil), s.order...), nil
	}
	var out []string
	for entity, datoms := range s.datoms {
		for _, d := range datoms {
			if d.IsReference && string(d.Value) == target {
				out = append(out, entity)
				break
			}
		}
	}
	return out, nil
}

func scanning(datoms map[string][]ports.Datom) *scanningReader {
	return &scanningReader{fakeReader: &fakeReader{datoms: datoms}}
}

// TestAMemberMissingEitherAttributeIsSkipped is ADR-035's falsifier.
//
// ★ Four members, each excluded or included for a DIFFERENT reason. A test with
// one excluded member passes while two of the three drop rules are broken.
func TestAMemberMissingEitherAttributeIsSkipped(t *testing.T) {
	ctx := context.Background()
	r := scanning(map[string][]ports.Datom{
		// Carries both, and matches. The only member that should survive.
		"ann": {
			link("ann", "member", "staff", 100),
			fact("ann", "name", "Ann", 100),
			fact("ann", "lastname", "a", 100),
		},
		// Carries the projected attribute, and NO lastname to test.
		"bob": {
			link("bob", "member", "staff", 100),
			fact("bob", "name", "Bob", 100),
		},
		// Carries the predicate's attribute and matches, but has no name.
		"cy": {
			link("cy", "member", "staff", 100),
			fact("cy", "lastname", "a", 100),
		},
		// Carries both, and does not match.
		"dee": {
			link("dee", "member", "staff", 100),
			fact("dee", "name", "Dee", 100),
			fact("dee", "lastname", "z", 100),
		},
	})

	rows, err := Read(ctx, r, parseRead(t, `READ ->name FROM [staff] WHERE ->lastname = 'a'`), 1000)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}

	if want := []string{"name=Ann"}; !equal(rowStrings(rows), want) {
		t.Fatalf("got %v, want %v\n"+
			"bob has no lastname to test, cy has no name to project, and dee's lastname is "+
			"wrong. A member missing ANY attribute the statement names contributes no rows.",
			rowStrings(rows), want)
	}
	for _, row := range rows {
		if row.Entity != "ann" {
			t.Errorf("row from %q survived: %+v", row.Entity, row)
		}
	}

	// ⚠ And each exclusion is checked on its own, so a single passing assertion
	// above cannot hide two broken rules.
	only := func(src string) []string {
		t.Helper()
		got, err := Read(ctx, r, parseRead(t, src), 1000)
		if err != nil {
			t.Fatalf("Read(%q): %v", src, err)
		}
		return rowStrings(got)
	}
	// With no predicate, cy is still dropped — it has no `name` to project.
	if want := []string{"name=Ann", "name=Bob", "name=Dee"}; !equal(only(`READ ->name FROM [staff]`), want) {
		t.Errorf("without a predicate got %v, want %v — cy carries no name",
			only(`READ ->name FROM [staff]`), want)
	}
	// Projecting lastname instead drops bob, who has none.
	if want := []string{"lastname=a", "lastname=a", "lastname=z"}; !equal(only(`READ ->lastname FROM [staff]`), want) {
		t.Errorf("projecting lastname got %v, want %v", only(`READ ->lastname FROM [staff]`), want)
	}
}

// TestARetractedReferenceLeavesTheSet checks the datoms decide, not the index.
func TestARetractedReferenceLeavesTheSet(t *testing.T) {
	ctx := context.Background()
	r := scanning(map[string][]ports.Datom{
		"ann": {
			link("ann", "member", "staff", 100),
			fact("ann", "name", "Ann", 100),
		},
		// ⚠ Bob JOINED and then LEFT. The candidate source still proposes him,
		// because an index that appends never un-proposes — which is exactly the
		// case that makes confirmation load-bearing rather than decorative.
		"bob": {
			link("bob", "member", "staff", 100),
			unlink("bob", "member", "staff", 200),
			fact("bob", "name", "Bob", 100),
		},
	})

	// Sanity: the source really does still propose bob, or this test proves
	// nothing about confirmation.
	candidates, err := r.Referrers(ctx, "staff", ports.Snapshot{})
	if err != nil {
		t.Fatalf("Referrers: %v", err)
	}
	if !contains(candidates, "bob") {
		t.Fatal("the candidate source no longer proposes bob, so this test cannot show that " +
			"confirmation is what excludes him")
	}

	rows, err := Read(ctx, r, parseRead(t, `READ ->name FROM [staff] AS OF 300`), 1000)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if want := []string{"name=Ann"}; !equal(rowStrings(rows), want) {
		t.Errorf("got %v, want %v — bob's membership was retracted, and a candidate list is a "+
			"proposal rather than an answer", rowStrings(rows), want)
	}

	// ★ And the past still holds him: the retraction is valid from 200, so at
	// 150 bob was a member. The set is bitemporal because a reference is a datom.
	past, err := Read(ctx, r, parseRead(t, `READ ->name FROM [staff] AS OF 150`), 1000)
	if err != nil {
		t.Fatalf("Read at 150: %v", err)
	}
	if want := []string{"name=Ann", "name=Bob"}; !equal(rowStrings(past), want) {
		t.Errorf("at 150 got %v, want %v — bob was a member then", rowStrings(past), want)
	}
}

// TestAPageIsMembersInAStableOrderAfterTheDrop is ADR-035 rule 5.
func TestAPageIsMembersInAStableOrderAfterTheDrop(t *testing.T) {
	ctx := context.Background()

	// Five candidates, of which `ghost` is dropped for carrying no `name`.
	// ⚠ Each surviving member carries TWO projected attributes, so paging over
	// rows rather than members would cut one in half and be caught here.
	datoms := map[string][]ports.Datom{}
	for _, who := range []string{"ann", "bob", "cy", "dee"} {
		datoms[who] = []ports.Datom{
			link(who, "member", "staff", 100),
			fact(who, "name", strings.ToUpper(who), 100),
			fact(who, "rank", "1", 100),
		}
	}
	datoms["ghost"] = []ports.Datom{
		link("ghost", "member", "staff", 100),
		fact("ghost", "rank", "1", 100),
	}

	r := scanning(datoms)
	// ⚠ A deliberately shuffled candidate order. An evaluator that paged the
	// source's order would be nondeterministic, and a single run might still pass
	// — so the order is fixed to a WRONG one rather than left to chance.
	r.order = []string{"dee", "ghost", "ann", "cy", "bob"}

	read := func(src string) []string {
		t.Helper()
		rows, err := Read(ctx, r, parseRead(t, src), 1000)
		if err != nil {
			t.Fatalf("Read(%q): %v", src, err)
		}
		return rowStrings(rows)
	}

	all := read(`READ ->name, ->rank FROM [staff]`)
	want := []string{"name=ANN", "rank=1", "name=BOB", "rank=1", "name=CY", "rank=1", "name=DEE", "rank=1"}
	if !equal(all, want) {
		t.Fatalf("unpaged got %v, want %v — members are ordered by entity name, and ghost "+
			"carries no name", all, want)
	}

	// LIMIT 2 is two MEMBERS, so four rows: each member keeps all its attributes.
	first := read(`READ ->name, ->rank FROM [staff] LIMIT 2`)
	if w := []string{"name=ANN", "rank=1", "name=BOB", "rank=1"}; !equal(first, w) {
		t.Errorf("LIMIT 2 got %v, want %v — a page is members, not rows", first, w)
	}

	second := read(`READ ->name, ->rank FROM [staff] LIMIT 2 OFFSET 2`)
	if w := []string{"name=CY", "rank=1", "name=DEE", "rank=1"}; !equal(second, w) {
		t.Errorf("LIMIT 2 OFFSET 2 got %v, want %v", second, w)
	}

	// ★ THE PROPERTY: the pages concatenate to the whole, with nothing repeated
	// and nothing skipped. That is what paging MEANS, and it is false for any
	// order that is not total.
	if !equal(append(append([]string{}, first...), second...), all) {
		t.Errorf("page 1 + page 2 = %v, want %v", append(append([]string{}, first...), second...), all)
	}

	// ⚠ The offset counts SURVIVORS. `ghost` was dropped, so offsetting past the
	// four survivors is empty — not "one left because a candidate was skipped".
	if got := read(`READ ->name FROM [staff] LIMIT 5 OFFSET 4`); len(got) != 0 {
		t.Errorf("OFFSET past the survivors returned %v; the page follows the drop", got)
	}

	// `LIMIT 0` asks for nothing, and is not the same as omitting the clause.
	if got := read(`READ ->name FROM [staff] LIMIT 0`); len(got) != 0 {
		t.Errorf("LIMIT 0 returned %v, want nothing", got)
	}
}

// TestAReaderThatCannotScanIsRefused is ADR-035 rule 8.
func TestAReaderThatCannotScanIsRefused(t *testing.T) {
	ctx := context.Background()

	// A plain reader. It holds a member, so an empty answer would look entirely
	// plausible — which is the point.
	plain := &fakeReader{datoms: map[string][]ports.Datom{
		"ann": {link("ann", "member", "staff", 100), fact("ann", "name", "Ann", 100)},
	}}

	rows, err := Read(ctx, plain, parseRead(t, `READ ->name FROM [staff]`), 1000)
	if !errors.Is(err, ErrNoInboundIndex) {
		t.Fatalf("Read = %v, want ErrNoInboundIndex — \"nothing points here\" and \"I cannot "+
			"tell you what points here\" are different answers", err)
	}
	if rows != nil {
		t.Errorf("a refused read returned %v alongside its error", rowStrings(rows))
	}

	// And a reader that CAN scan answers the same statement, so the refusal is
	// about the capability rather than about the statement being unrunnable.
	if got, err := Read(ctx, scanning(plain.datoms), parseRead(t, `READ ->name FROM [staff]`), 1000); err != nil {
		t.Errorf("the same statement against a scanning reader: %v", err)
	} else if want := []string{"name=Ann"}; !equal(rowStrings(got), want) {
		t.Errorf("got %v, want %v", rowStrings(got), want)
	}
}

// TestAnInboundReadRunsAgainstARealLeaf checks `Referrers` is a scan over stored
// datoms rather than a convenience on a test double.
func TestAnInboundReadRunsAgainstARealLeaf(t *testing.T) {
	ctx := context.Background()
	store, err := leafstore.Open(t.TempDir(), testLeaf())
	if err != nil {
		t.Fatalf("leafstore.Open: %v", err)
	}
	defer func() { _ = store.Close() }()

	// ⚠ One entity per Append: the transaction boundary is one entity (ADR-003).
	for _, batch := range [][]ports.Datom{
		{link("ann", "member", "staff", 100), fact("ann", "name", "Ann", 100)},
		{link("bob", "member", "staff", 100), unlink("bob", "member", "staff", 200),
			fact("bob", "name", "Bob", 100)},
		{fact("zed", "name", "Zed", 100)},
	} {
		if err := store.Append(ctx, batch...); err != nil {
			t.Fatalf("Append: %v", err)
		}
	}
	if err := store.Seal(ctx); err != nil {
		t.Fatalf("Seal: %v", err)
	}

	// ★ Sealed to disk first, so this reads through the segment format rather
	// than the live tail — the answer must survive the round trip.
	rows, err := Read(ctx, store, parseRead(t, `READ ->name FROM [staff] AS OF 300`), 1000)
	if err != nil {
		t.Fatalf("Read against a leaf: %v", err)
	}
	if want := []string{"name=Ann"}; !equal(rowStrings(rows), want) {
		t.Fatalf("got %v, want %v — bob left, and zed never pointed at staff",
			rowStrings(rows), want)
	}

	// The same bitemporal answer the fake gives: at 150 bob was still a member.
	past, err := Read(ctx, store, parseRead(t, `READ ->name FROM [staff] AS OF 150`), 1000)
	if err != nil {
		t.Fatalf("Read at 150: %v", err)
	}
	if want := []string{"name=Ann", "name=Bob"}; !equal(rowStrings(past), want) {
		t.Errorf("at 150 got %v, want %v", rowStrings(past), want)
	}

	// ⚠ And a member that points at something ELSE is not in this set, or
	// `Referrers` is answering "has any reference at all".
	other, err := Read(ctx, store, parseRead(t, `READ ->name FROM [contractors]`), 1000)
	if err != nil {
		t.Fatalf("Read of an empty set: %v", err)
	}
	if len(other) != 0 {
		t.Errorf("[contractors] returned %v; nothing points at it", rowStrings(other))
	}
}

func contains(all []string, want string) bool {
	for _, s := range all {
		if s == want {
			return true
		}
	}
	return false
}

// _ asserts at compile time that a leaf and the fake both satisfy the port, so a
// signature drift is a build failure rather than a runtime type assertion that
// quietly falls through to ErrNoInboundIndex.
var (
	_ ports.Inbound = (*leafstore.Store)(nil)
	_ ports.Inbound = (*scanningReader)(nil)
)
