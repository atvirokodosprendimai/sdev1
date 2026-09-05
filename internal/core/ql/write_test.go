package ql

import (
	"strings"
	"testing"

	"github.com/atvirokodosprendimai/sdev1/internal/core/temporal"
)

func mustWrite(t *testing.T, src string) *Write {
	t.Helper()
	stmt, err := Parse(src)
	if err != nil {
		t.Fatalf("Parse(%q): %v", src, err)
	}
	w, ok := stmt.(*Write)
	if !ok {
		t.Fatalf("Parse(%q) returned %T, want *Write", src, stmt)
	}
	return w
}

// TestAWriteCannotSetTransactionTime is ADR-022's falsifier.
//
// ⚠ Every READ statement takes a TRANSACTION clause, so making writes
// symmetrical looks like consistency — which is exactly why this is the mistake
// worth a falsifier. The test also asserts the clause STILL parses on a read, so
// a lazy fix that removed the keyword entirely would fail here too.
func TestAWriteCannotSetTransactionTime(t *testing.T) {
	for _, src := range []string{
		`ASSERT planet-7 mass = 5972 TRANSACTION 1700000500`,
		`RETRACT planet-7 mass = 5972 TRANSACTION 1700000500`,
		`ASSERT planet-7 mass = 5972 VALID FROM 100 TRANSACTION 1700000500`,
		`ASSERT planet-7 mass = 5972 VALID FROM 100 TO 200 TRANSACTION 1700000500`,
	} {
		stmt, err := Parse(src)
		if err == nil {
			t.Fatalf("Parse(%q) succeeded and returned %+v.\nA caller who can set transaction time "+
				"can claim to have known something earlier than they did, and NO query can detect it — "+
				"because every query's evidence is the value that was forged.", src, stmt)
		}
		if !strings.Contains(err.Error(), ErrTransactionTimeIsNotYours.Error()) {
			t.Fatalf("Parse(%q) failed with %q, which does not say WHY. A caller who sees "+
				"'unexpected token' will try harder to make it work.", src, err)
		}
	}

	// ⚠ Positive control on the other side of the rule: the same clause is still
	// legal on a READ. Without this, deleting the keyword outright would pass.
	if _, err := Parse(`READ * FROM planet-7 TRANSACTION 1700000500`); err != nil {
		t.Fatalf("TRANSACTION stopped parsing on a read: %v — the refusal must be about WRITES, "+
			"not about the keyword", err)
	}
}

// TestWriteRoundTripsThroughTheAST checks both verbs land where a caller expects.
func TestWriteRoundTripsThroughTheAST(t *testing.T) {
	bare := mustWrite(t, `ASSERT planet-7 mass = 5972`)
	if bare.Op != OpAssert || bare.Entity != "planet-7" || bare.Attribute != "mass" {
		t.Fatalf("parsed to %+v", bare)
	}
	if bare.Value != "5972" || !bare.ValueIsNumber {
		t.Fatalf("value is %q (number=%v), want 5972 as a number", bare.Value, bare.ValueIsNumber)
	}
	if bare.From != nil || bare.To != nil {
		t.Fatalf("a write with no VALID clause carried %v..%v", bare.From, bare.To)
	}

	closed := mustWrite(t, `ASSERT planet-7 class = 'terrestrial' VALID FROM 100 TO 200`)
	if closed.Value != "terrestrial" || closed.ValueIsNumber {
		t.Fatalf("a quoted value came through as %q (number=%v)", closed.Value, closed.ValueIsNumber)
	}
	if closed.From == nil || *closed.From != 100 || closed.To == nil || *closed.To != 200 {
		t.Fatalf("interval parsed to %v..%v", closed.From, closed.To)
	}

	open := mustWrite(t, `ASSERT planet-7 mass = 5972 VALID FROM 100`)
	if open.From == nil || *open.From != 100 {
		t.Fatalf("FROM parsed to %v", open.From)
	}
	if open.To != nil {
		t.Fatalf("an omitted TO produced %d; a fact with no stated end has not ended", *open.To)
	}
	if got := open.Interval(999); got.To != temporal.Forever {
		t.Fatalf("an open interval resolved to %v, want an end of Forever", got)
	}

	retract := mustWrite(t, `RETRACT planet-7 mass = 5972 VALID FROM 500`)
	if retract.Op != OpRetract {
		t.Fatalf("RETRACT parsed as %v", retract.Op)
	}
}

// TestOmittedValidityIsTheWriteInstantNotTheBeginningOfTime checks the default is
// derived from the write rather than being zero.
func TestOmittedValidityIsTheWriteInstantNotTheBeginningOfTime(t *testing.T) {
	w := mustWrite(t, `ASSERT planet-7 mass = 5972`)

	const now = int64(1700000000)
	got := w.Interval(now)

	if got.From == 0 {
		t.Fatal("an omitted VALID clause resolved to instant 0, which silently claims the fact " +
			"had been true since the beginning of time — and nothing about the stored datom would look unusual")
	}
	if got.From != now {
		t.Fatalf("validity starts at %d, want the write's own instant %d", got.From, now)
	}
	if got.To != temporal.Forever {
		t.Fatalf("validity ends at %d, want Forever — a fact with no stated end has not ended", got.To)
	}

	// An explicit clause still wins over the default.
	explicit := mustWrite(t, `ASSERT planet-7 mass = 5972 VALID FROM 42`)
	if explicit.Interval(now).From != 42 {
		t.Fatalf("an explicit VALID FROM was overridden by the default")
	}
}

// TestAWriteNamesOneEntity checks the transaction boundary is enforced by the
// grammar rather than at commit.
func TestAWriteNamesOneEntity(t *testing.T) {
	for _, src := range []string{
		`ASSERT planet-7 planet-9 mass = 5972`,
		`ASSERT planet-7, planet-9 mass = 5972`,
		`ASSERT planet-7 mass = 5972, radius = 6371`,
	} {
		if stmt, err := Parse(src); err == nil {
			t.Fatalf("Parse(%q) succeeded and returned %+v; the entity is the transaction boundary, "+
				"and a shape that could never commit must be refused where it is written rather than at the end",
				src, stmt)
		}
	}
}

// TestWriteVerbsAreAClosedPair checks the language cannot decay towards CRUD.
func TestWriteVerbsAreAClosedPair(t *testing.T) {
	if got := len(WriteOps()); got != 2 {
		t.Fatalf("WriteOps returns %d verbs, want exactly 2", got)
	}

	// ⚠ The direction the language decays in. Asserting only that ASSERT and
	// RETRACT work would not notice a third verb appearing beside them.
	for _, verb := range []string{"INSERT", "UPDATE", "DELETE", "SET", "MERGE", "UPSERT"} {
		src := verb + " planet-7 mass = 5972"
		if stmt, err := Parse(src); err == nil {
			t.Fatalf("Parse(%q) succeeded and returned %+v.\nThe store appends: an update is an "+
				"assertion, a delete a retraction, an erasure a destroyed key. A CRUD verb describes a "+
				"data model this system does not have, and everything the caller then infers about "+
				"history and erasure is wrong — silently.", src, stmt)
		}
	}
}

// TestRetractCarriesItsInterval checks a retraction says when the fact stopped
// holding.
func TestRetractCarriesItsInterval(t *testing.T) {
	stated := mustWrite(t, `RETRACT planet-7 mass = 5972 VALID FROM 500`)
	if got := stated.Interval(1700000000); got.From != 500 {
		t.Fatalf("a stated retraction starts at %d, want 500", got.From)
	}

	// ⚠ An omitted clause retracts from the write's own instant. Retracting a
	// fact as if it had NEVER been true must be stated explicitly, so an
	// omission can never rewrite history by accident.
	const now = int64(1700000000)
	omitted := mustWrite(t, `RETRACT planet-7 mass = 5972`)
	got := omitted.Interval(now)
	if got.From != now {
		t.Fatalf("an omitted retraction interval starts at %d, want the write's instant %d", got.From, now)
	}
	if got.From == 0 {
		t.Fatal("an omitted retraction interval started at 0, which retracts the fact as though it " +
			"had never been true — history rewritten by an omission")
	}
}
