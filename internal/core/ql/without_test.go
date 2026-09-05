package ql

import (
	"errors"
	"testing"
)

// TestWithoutObeysTheAttributeMarker checks the absence clause follows the same
// rule about what an attribute name means as everywhere else.
//
// ★ One rule, applied wherever an attribute name appears. A clause with its own
// marker convention would be a second thing to learn and a second thing to get
// wrong when the join arrives.
func TestWithoutObeysTheAttributeMarker(t *testing.T) {
	// Correct spellings parse, in both sources.
	set := mustParseRead(t, "READ ->name FROM [staff] WITHOUT ->thirdname")
	if got := set.Without; len(got) != 1 || got[0] != "thirdname" {
		t.Errorf("Without = %v, want [thirdname] — the marker is grammar, not part of the name", got)
	}
	one := mustParseRead(t, "READ name FROM planet-7 WITHOUT radius")
	if got := one.Without; len(got) != 1 || got[0] != "radius" {
		t.Errorf("Without = %v, want [radius]", got)
	}

	// Several, comma-separated.
	many := mustParseRead(t, "READ ->name FROM [staff] WITHOUT ->thirdname, ->nickname")
	if got := many.Without; len(got) != 2 || got[0] != "thirdname" || got[1] != "nickname" {
		t.Errorf("Without = %v, want [thirdname nickname]", got)
	}

	// The marker rule, both directions.
	for _, src := range []string{
		"READ ->name FROM [staff] WITHOUT thirdname",
		"READ name FROM planet-7 WITHOUT ->radius",
		"READ ->name FROM [staff] WITHOUT ->a, b",
	} {
		if _, err := Parse(src); !errors.Is(err, ErrJoinNotSupported) {
			t.Errorf("Parse(%q) = %v, want ErrJoinNotSupported — WITHOUT follows the same "+
				"marker rule as the projection and the predicate", src, err)
		}
	}

	// ⚠ An empty clause is refused. `WITHOUT` with nothing after it excludes
	// nothing, which is the same as omitting it — so accepting it would be a
	// statement that says something and means nothing.
	if _, err := Parse("READ ->name FROM [staff] WITHOUT"); err == nil {
		t.Error("an empty WITHOUT parsed; it must name at least one attribute")
	}

	// It sits between WHERE and the page clause, and neither is lost.
	full := mustParseRead(t,
		"READ ->name FROM [staff] WHERE ->rank = 3 WITHOUT ->thirdname LIMIT 5 OFFSET 2 AS OF 900")
	if full.Where == nil || full.Where.Attribute != "rank" {
		t.Errorf("predicate = %+v, want rank = 3", full.Where)
	}
	if len(full.Without) != 1 || full.Without[0] != "thirdname" {
		t.Errorf("Without = %v", full.Without)
	}
	if !full.Page.Has || full.Page.Limit != 5 || full.Page.Offset != 2 {
		t.Errorf("page = %+v, want {5 2 true}", full.Page)
	}
	if full.Time.ValidAt == nil || *full.Time.ValidAt != 900 {
		t.Errorf("AS OF = %v, want 900", full.Time.ValidAt)
	}

	// ★ And no operator joins the two clauses. ADR-011 has no boolean
	// composition, and this is the shape that avoids needing it — so `AND` must
	// still be refused rather than having quietly become reachable.
	if _, err := Parse("READ ->name FROM [staff] WHERE ->rank = 3 AND ->x = 1"); err == nil {
		t.Error("AND parsed in a WHERE; absence is a clause precisely so that boolean " +
			"composition does not arrive as a side effect")
	}

	// A keyword stays addressable as an attribute, as ADR-021 requires. ⚠ The
	// marker still applies: quoting decides whether a word is an IDENTIFIER, and
	// the marker decides WHOSE attribute it is. Two independent questions, so
	// answering one does not excuse the other.
	quoted := mustParseRead(t, "READ ->name FROM [staff] WITHOUT ->`without`")
	if got := quoted.Without; len(got) != 1 || got[0] != "without" {
		t.Errorf("Without = %v, want [without] — reserving a word must not take an attribute "+
			"name away", got)
	}
}
