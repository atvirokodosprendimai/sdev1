package ql

import (
	"errors"
	"strings"
	"testing"

	"github.com/atvirokodosprendimai/sdev1/internal/core/segment"
)

func parseShapeQuery(t *testing.T, src string) *ShapeQuery {
	t.Helper()
	stmt, err := Parse(src)
	if err != nil {
		t.Fatalf("Parse(%q): %v", src, err)
	}
	q, ok := stmt.(*ShapeQuery)
	if !ok {
		t.Fatalf("Parse(%q) returned %T, want *ShapeQuery", src, stmt)
	}
	return q
}

// TestOptionalLegYieldsAnUnboundValue checks an optional leg that matched
// nothing appears in the row as UNBOUND, rather than being absent.
//
// ⚠ Unbound must be distinguishable from a binding of the empty string.
// Conflating them is how a consumer treats "this subject has no nickname" as
// "this subject's nickname is blank".
func TestOptionalLegYieldsAnUnboundValue(t *testing.T) {
	legs := []Leg{
		{Attribute: "employer", Kind: LegRequired},
		{Attribute: "nickname", Kind: LegOptional},
	}
	matched := map[string]string{"employer": "acme"}

	row, kept := BuildRow("person:alice", legs, matched)
	if !kept {
		t.Fatal("the row was dropped although every REQUIRED leg matched")
	}

	nick, present := row.Get("nickname")
	if !present {
		t.Fatal("the optional leg is absent from the row entirely; it must be present and unbound, " +
			"or a consumer cannot tell 'no value' from 'no such leg'")
	}
	if nick.IsBound() {
		t.Error("the optional leg is bound although nothing matched it")
	}
	if v, ok := nick.Value(); ok || v != "" {
		t.Errorf("an unbound binding yielded (%q, %v), want (\"\", false)", v, ok)
	}

	// And it is NOT the same as a binding of the empty string.
	empty := Bound("nickname", "")
	if !empty.IsBound() {
		t.Error("a binding of the empty string reports itself unbound; the two must differ")
	}
	if empty == nick {
		t.Error("an unbound binding equals a binding of the empty string")
	}
	if !strings.Contains(nick.String(), "unbound") {
		t.Errorf("an unbound binding renders as %q, which does not say it is unbound", nick.String())
	}

	// The bound leg is bound, so the test is about the optional one rather than
	// about a BuildRow that binds nothing.
	emp, _ := row.Get("employer")
	if v, ok := emp.Value(); !ok || v != "acme" {
		t.Errorf("the required leg bound (%q, %v), want (acme, true)", v, ok)
	}
}

// TestOptionalLegNeverDropsTheRow is the falsifier for ADR-011's rule 5.
func TestOptionalLegNeverDropsTheRow(t *testing.T) {
	legs := []Leg{
		{Attribute: "employer", Kind: LegRequired},
		{Attribute: "nickname", Kind: LegOptional},
		{Attribute: "city", Kind: LegOptional},
	}

	// Every optional leg matches nothing. The row survives.
	row, kept := BuildRow("person:alice", legs, map[string]string{"employer": "acme"})
	if !kept {
		t.Fatal("a row was dropped because its OPTIONAL legs matched nothing — that makes " +
			"OPTIONAL a synonym for REQUIRE, and the difference is undetectable on any dataset " +
			"where the legs happen always to be present")
	}
	if len(row.Bindings) != len(legs) {
		t.Errorf("the row carries %d bindings, want one per leg (%d)", len(row.Bindings), len(legs))
	}
	for _, attr := range []string{"nickname", "city"} {
		b, ok := row.Get(attr)
		if !ok || b.IsBound() {
			t.Errorf("optional leg %q: present=%v bound=%v; want present and unbound", attr, ok, b.IsBound())
		}
	}

	// Bindings keep the order the legs were written, so a consumer can rely on it.
	for i, leg := range legs {
		if row.Bindings[i].Attribute != leg.Attribute {
			t.Errorf("binding %d is %q, want %q — bindings follow leg order",
				i, row.Bindings[i].Attribute, leg.Attribute)
		}
	}
}

// TestRequiredLegDoesDropTheRow is the control that makes the two tests above
// mean something.
//
// ⚠ Without it, "an optional leg does not drop the row" says nothing — it would
// hold for an implementation that never drops any row at all.
func TestRequiredLegDoesDropTheRow(t *testing.T) {
	legs := []Leg{
		{Attribute: "employer", Kind: LegRequired},
		{Attribute: "nickname", Kind: LegOptional},
	}

	if _, kept := BuildRow("person:alice", legs, map[string]string{"nickname": "al"}); kept {
		t.Fatal("a row survived although its REQUIRED leg matched nothing; the two kinds of leg " +
			"are then indistinguishable and OPTIONAL means nothing")
	}

	// With the required leg matched, it survives — so the drop is about the leg
	// kind rather than about BuildRow refusing everything.
	if _, kept := BuildRow("person:alice", legs, map[string]string{"employer": "acme"}); !kept {
		t.Error("a row was dropped although its required leg matched")
	}

	// Every required leg must match, not just one.
	two := []Leg{
		{Attribute: "employer", Kind: LegRequired},
		{Attribute: "city", Kind: LegRequired},
	}
	if _, kept := BuildRow("x", two, map[string]string{"employer": "acme"}); kept {
		t.Error("a row survived with one of two required legs unmatched")
	}
	if _, kept := BuildRow("x", two, map[string]string{"employer": "acme", "city": "Vilnius"}); !kept {
		t.Error("a row was dropped with both required legs matched")
	}
}

// TestShapeQueryRequiresAMetricAndThreshold checks a well-formed shape query
// parses, and that each half of the requirement is refused on its own.
func TestShapeQueryRequiresAMetricAndThreshold(t *testing.T) {
	q := parseShapeQuery(t,
		"MATCH SHAPE LIKE person REQUIRE employer, city OPTIONAL nickname SIMILARITY jaccard >= 0.8 AS OF 500")

	if q.Subject != "person" {
		t.Errorf("subject = %q, want person", q.Subject)
	}
	if q.Metric != "jaccard" {
		t.Errorf("metric = %q, want jaccard", q.Metric)
	}
	if q.Threshold != 0.8 {
		t.Errorf("threshold = %v, want 0.8", q.Threshold)
	}
	if len(q.Legs) != 3 {
		t.Fatalf("parsed %d legs, want 3", len(q.Legs))
	}
	for i, want := range []struct {
		attr string
		kind LegKind
	}{{"employer", LegRequired}, {"city", LegRequired}, {"nickname", LegOptional}} {
		if q.Legs[i].Attribute != want.attr || q.Legs[i].Kind != want.kind {
			t.Errorf("leg %d = %s %s, want %s %s", i, q.Legs[i].Kind, q.Legs[i].Attribute,
				want.kind, want.attr)
		}
	}
	// The statement's own time clause attached.
	if q.Time.ValidAt == nil || *q.Time.ValidAt != 500 {
		t.Errorf("the statement time clause = %v, want 500", q.Time.ValidAt)
	}

	// A per-leg time qualifier, which is why time is a clause rather than a verb.
	perLeg := parseShapeQuery(t,
		"MATCH SHAPE LIKE person REQUIRE employer AS OF 100 OPTIONAL nickname SIMILARITY jaccard >= 0.5")
	if perLeg.Legs[0].Time.ValidAt == nil || *perLeg.Legs[0].Time.ValidAt != 100 {
		t.Errorf("the per-leg time clause = %v, want 100 — attaching per leg is the property "+
			"the clause form was chosen for", perLeg.Legs[0].Time.ValidAt)
	}
	if perLeg.Legs[1].Time.ValidAt != nil {
		t.Error("a leg without its own clause acquired one")
	}

	// Each half of the requirement is refused SEPARATELY, so neither refusal
	// stands in for the other.
	//
	// ⚠ Each case asserts what the error SAYS, not merely that one occurred.
	// Asserting "an error" is not enough: with the SIMILARITY guard removed, the
	// next check in the parser still errors — so a test that only counts errors
	// passes with the mechanism gone. A surviving mutant found exactly that here.
	for _, c := range []struct {
		src      string
		expected string
	}{
		{"MATCH SHAPE LIKE person REQUIRE employer", "SIMILARITY"},
		{"MATCH SHAPE LIKE person OPTIONAL nickname", "SIMILARITY"},
		{"MATCH SHAPE LIKE person REQUIRE employer SIMILARITY jaccard", "threshold"},
		{"MATCH SHAPE LIKE person REQUIRE employer SIMILARITY >= 0.8", "metric"},
		{"MATCH SHAPE LIKE person REQUIRE employer SIMILARITY jaccard >= person", "threshold"},
	} {
		_, err := Parse(c.src)
		if err == nil {
			t.Errorf("Parse(%q) succeeded; a shape query without a stated metric and threshold "+
				"is a result nobody can reproduce", c.src)
			continue
		}
		var pe *ParseError
		if !errors.As(err, &pe) {
			t.Errorf("Parse(%q) returned %T, want a *ParseError", c.src, err)
			continue
		}
		if !strings.Contains(pe.Expected, c.expected) {
			t.Errorf("Parse(%q): expected %q, want it to mention %q — an error from some LATER "+
				"check would pass a test that only counts errors, while the guard this case is "+
				"about had been removed", c.src, pe.Expected, c.expected)
		}
	}

	// A query missing the whole SIMILARITY clause must cite the requirement
	// itself, so the message tells a caller what the language needs.
	_, err := Parse("MATCH SHAPE LIKE person REQUIRE employer")
	var pe *ParseError
	if !errors.As(err, &pe) || !strings.Contains(pe.Expected, ErrNoThreshold.Error()) {
		t.Errorf("a query with no SIMILARITY clause reported %v, want it to cite %q",
			err, ErrNoThreshold.Error())
	}

	// And there is no default threshold to fall back on.
	if _, err := Parse("MATCH SHAPE LIKE person REQUIRE employer SIMILARITY jaccard >= "); err == nil {
		t.Error("a missing threshold acquired a default")
	}
}

// TestPolicyClauseAppliesToNewDataOnly checks the clause names a codec the
// segment format already knows, and that the language cannot express re-encoding.
func TestPolicyClauseAppliesToNewDataOnly(t *testing.T) {
	for _, c := range []struct {
		src   string
		codec segment.CodecID
	}{
		{"WITH COMPRESSION zstd", segment.CodecZstd},
		{"WITH COMPRESSION none", segment.CodecIdentity},
		{"WITH COMPRESSION identity", segment.CodecIdentity},
		{"with compression ZSTD", segment.CodecZstd},
	} {
		clause, err := ParsePolicyClause(c.src)
		if err != nil {
			t.Fatalf("ParsePolicyClause(%q): %v", c.src, err)
		}
		if clause.Codec != c.codec {
			t.Errorf("%q gave codec %d, want %d", c.src, clause.Codec, c.codec)
		}
		if clause.Scope != PolicyNewWritesOnly {
			t.Errorf("%q gave scope %v, want new writes only", c.src, clause.Scope)
		}
	}

	// ⚠ There is exactly ONE scope, so there is no way to say "re-encode what
	// exists". The absence is the enforcement.
	if got := len(PolicyScopes()); got != 1 {
		t.Errorf("there are %d policy scopes, want exactly 1 — a second would let the language "+
			"express re-encoding existing data, which every block's own header makes unnecessary "+
			"and which no syntax here should be able to request", got)
	}

	// An unknown codec is refused by name, listing what is known.
	_, err := ParsePolicyClause("WITH COMPRESSION brotli")
	if err == nil {
		t.Fatal("an unknown codec was accepted")
	}
	var pe *ParseError
	if !errors.As(err, &pe) {
		t.Fatalf("returned %T, want a *ParseError", err)
	}
	if !strings.Contains(pe.Expected, "zstd") {
		t.Errorf("the error does not list the known codecs: %q", pe.Expected)
	}

	// Trailing junk is refused rather than ignored.
	if _, err := ParsePolicyClause("WITH COMPRESSION zstd EXTRA"); err == nil {
		t.Error("trailing tokens after a policy clause were ignored")
	}
	if _, err := ParsePolicyClause("COMPRESSION zstd"); err == nil {
		t.Error("a clause missing its WITH keyword was accepted")
	}
}
