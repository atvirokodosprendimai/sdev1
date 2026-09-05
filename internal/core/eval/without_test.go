package eval

import (
	"context"
	"testing"

	"github.com/atvirokodosprendimai/sdev1/internal/core/ports"
	"github.com/atvirokodosprendimai/sdev1/internal/core/temporal"
)

// bounded is a fact that stops being true at `to`.
func bounded(entity, attribute, value string, from, to int64) ports.Datom {
	d := fact(entity, attribute, value, from)
	d.Valid = temporal.Interval{From: from, To: to}
	return d
}

// retracted is the retraction of a fact, valid from `wall`.
func retracted(entity, attribute, value string, wall int64) ports.Datom {
	d := fact(entity, attribute, value, wall)
	d.Assert = false
	d.Valid = temporal.Interval{From: wall, To: temporal.Forever}
	return d
}

// TestAnExcludedAttributeIsNotAlsoRequired is ADR-036's falsifier.
//
// ⚠ The failure this guards against returns NOTHING, which is a completely
// plausible answer — so the assertion is on the rows returned, never merely on
// the absence of an error. ADR-035 rule 4 drops a member missing any attribute
// the statement NAMES; a WITHOUT attribute is named in order to be absent, so
// letting the drop rule reach it makes the clause unsatisfiable.
func TestAnExcludedAttributeIsNotAlsoRequired(t *testing.T) {
	ctx := context.Background()
	r := scanning(map[string][]ports.Datom{
		"ann": {link("ann", "member", "staff", 100), fact("ann", "name", "Ann", 100)},
		"bob": {link("bob", "member", "staff", 100), fact("bob", "name", "Bob", 100)},
		// ⚠ Carries the excluded attribute, so it must be filtered OUT. Without a
		// member like this the clause could be a no-op and still pass.
		"cy": {
			link("cy", "member", "staff", 100),
			fact("cy", "name", "Cy", 100),
			fact("cy", "thirdname", "Q", 100),
		},
	})

	rows, err := Read(ctx, r, parseRead(t, `READ ->name FROM [staff] WITHOUT ->thirdname`), 1000)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if len(rows) == 0 {
		t.Fatal("WITHOUT ->thirdname returned nothing.\n" +
			"That is the failure this test exists for: if the excluded attribute is also " +
			"REQUIRED, every member is dropped for lacking exactly what it was asked to lack, " +
			"and the clause is unsatisfiable. An empty result looks like a correct answer.")
	}
	if want := []string{"name=Ann", "name=Bob"}; !equal(rowStrings(rows), want) {
		t.Errorf("got %v, want %v — cy carries thirdname", rowStrings(rows), want)
	}

	// ★ And it works on a read of ONE entity, with the same meaning: return the
	// entity only if it lacks the named attribute. One rule, not two.
	plain := &fakeReader{datoms: map[string][]ports.Datom{
		"ann": {fact("ann", "name", "Ann", 100)},
		"cy":  {fact("cy", "name", "Cy", 100), fact("cy", "thirdname", "Q", 100)},
	}}
	if got, err := Read(ctx, plain, parseRead(t, `READ name FROM ann WITHOUT thirdname`), 1000); err != nil {
		t.Errorf("Read one entity: %v", err)
	} else if want := []string{"name=Ann"}; !equal(rowStrings(got), want) {
		t.Errorf("one entity got %v, want %v", rowStrings(got), want)
	}
	if got, err := Read(ctx, plain, parseRead(t, `READ name FROM cy WITHOUT thirdname`), 1000); err != nil {
		t.Errorf("Read one entity: %v", err)
	} else if len(got) != 0 {
		t.Errorf("cy carries thirdname and was returned anyway: %v", rowStrings(got))
	}
}

// TestAbsenceIsWhatAnEntityDoesNotCarry checks absence is the negation of
// [ports.Carried] and nothing new.
//
// ★ Four members, four different histories. The retracted one is what separates
// this from a naive "was this datom ever written" scan.
func TestAbsenceIsWhatAnEntityDoesNotCarry(t *testing.T) {
	ctx := context.Background()
	r := scanning(map[string][]ports.Datom{
		// Never had one.
		"ann": {link("ann", "member", "staff", 100), fact("ann", "name", "Ann", 100)},
		// Had one, and it was RETRACTED at 200. Absent from 200 onward.
		"bob": {
			link("bob", "member", "staff", 100),
			fact("bob", "name", "Bob", 100),
			fact("bob", "thirdname", "Q", 100),
			retracted("bob", "thirdname", "Q", 200),
		},
		// Had one over a bounded interval, 0..200. Absent at 300.
		"cy": {
			link("cy", "member", "staff", 100),
			fact("cy", "name", "Cy", 100),
			bounded("cy", "thirdname", "R", 0, 200),
		},
		// Has one, always. Never absent.
		"dee": {
			link("dee", "member", "staff", 100),
			fact("dee", "name", "Dee", 100),
			fact("dee", "thirdname", "S", 100),
		},
	})

	read := func(src string) []string {
		t.Helper()
		rows, err := Read(ctx, r, parseRead(t, src), 1000)
		if err != nil {
			t.Fatalf("Read(%q): %v", src, err)
		}
		return rowStrings(rows)
	}

	// At 300: ann never had one, bob's was retracted, cy's interval has closed.
	// ⚠ bob is the case a second definition of "has" gets wrong.
	if want := []string{"name=Ann", "name=Bob", "name=Cy"}; !equal(read(`READ ->name FROM [staff] WITHOUT ->thirdname AS OF 300`), want) {
		t.Errorf("at 300 got %v, want %v — a retracted attribute and a lapsed interval are "+
			"both ABSENT", read(`READ ->name FROM [staff] WITHOUT ->thirdname AS OF 300`), want)
	}

	// ★ THE POINT: absence is SNAPSHOT-RELATIVE. At 150 bob and cy still carry
	// theirs, so only ann qualifies. "Does not have one" is a question about an
	// instant, not about a history — and a caller reading it as "never had one"
	// would be wrong about both of them.
	if want := []string{"name=Ann"}; !equal(read(`READ ->name FROM [staff] WITHOUT ->thirdname AS OF 150`), want) {
		t.Errorf("at 150 got %v, want %v — bob and cy both carried a thirdname then",
			read(`READ ->name FROM [staff] WITHOUT ->thirdname AS OF 150`), want)
	}

	// dee never qualifies, at any instant.
	for _, src := range []string{
		`READ ->name FROM [staff] WITHOUT ->thirdname AS OF 150`,
		`READ ->name FROM [staff] WITHOUT ->thirdname AS OF 300`,
	} {
		for _, row := range read(src) {
			if row == "name=Dee" {
				t.Errorf("%s returned dee, who carries a thirdname throughout", src)
			}
		}
	}
}

// TestWhereAndWithoutConjoin is ADR-036 rule 1: two clauses, no operator.
func TestWhereAndWithoutConjoin(t *testing.T) {
	ctx := context.Background()
	r := scanning(map[string][]ports.Datom{
		// Satisfies both. The only survivor.
		"ann": {
			link("ann", "member", "staff", 100),
			fact("ann", "name", "Ann", 100), fact("ann", "rank", "3", 100),
		},
		// Right rank, but carries the excluded attribute.
		"bob": {
			link("bob", "member", "staff", 100),
			fact("bob", "name", "Bob", 100), fact("bob", "rank", "3", 100),
			fact("bob", "thirdname", "Q", 100),
		},
		// No thirdname, but the wrong rank.
		"cy": {
			link("cy", "member", "staff", 100),
			fact("cy", "name", "Cy", 100), fact("cy", "rank", "9", 100),
		},
		// No thirdname and the right rank, but nothing to project.
		"dee": {
			link("dee", "member", "staff", 100),
			fact("dee", "rank", "3", 100),
		},
	})

	rows, err := Read(ctx, r,
		parseRead(t, `READ ->name FROM [staff] WHERE ->rank = 3 WITHOUT ->thirdname`), 1000)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	// ★ Each of the three near-misses is excluded for its OWN reason. A test
	// where they all fail the same check proves one rule and implies three.
	if want := []string{"name=Ann"}; !equal(rowStrings(rows), want) {
		t.Fatalf("got %v, want %v — bob has a thirdname, cy's rank is wrong, and dee has no "+
			"name to project", rowStrings(rows), want)
	}

	// And each clause alone admits more, so the conjunction is doing work rather
	// than one clause carrying the whole result.
	onlyWhere, err := Read(ctx, r, parseRead(t, `READ ->name FROM [staff] WHERE ->rank = 3`), 1000)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if want := []string{"name=Ann", "name=Bob"}; !equal(rowStrings(onlyWhere), want) {
		t.Errorf("WHERE alone got %v, want %v", rowStrings(onlyWhere), want)
	}
	onlyWithout, err := Read(ctx, r, parseRead(t, `READ ->name FROM [staff] WITHOUT ->thirdname`), 1000)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if want := []string{"name=Ann", "name=Cy"}; !equal(rowStrings(onlyWithout), want) {
		t.Errorf("WITHOUT alone got %v, want %v", rowStrings(onlyWithout), want)
	}
}
