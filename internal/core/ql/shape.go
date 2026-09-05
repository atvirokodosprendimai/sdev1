package ql

import (
	"errors"
	"fmt"
)

// ErrNoThreshold reports a shape query written without a metric or without a
// threshold.
//
// ★ It is a parse error rather than a default. A default makes every unqualified
// shape query reproducible only by whoever knows the default, and the value is
// then a constant nobody wrote down.
var ErrNoThreshold = errors.New("ql: a shape query needs a metric and a threshold")

// LegKind says whether a leg must match.
type LegKind int

const (
	// LegKindUnset is the zero value and is never valid.
	LegKindUnset LegKind = iota
	// LegRequired: a leg that matches nothing drops the row.
	LegRequired
	// LegOptional: a leg that matches nothing binds nothing, and the row stays.
	LegOptional
	// LegExcluded: a leg that MATCHES drops the row (ADR-036).
	//
	// ⚠ It is the mirror of LegRequired, and it contributes NO BINDING at all —
	// see [BuildRow]. It is a filter: a subject carrying the attribute is not a
	// weaker match, it is not a match.
	LegExcluded
)

func (k LegKind) String() string {
	switch k {
	case LegRequired:
		return "required"
	case LegOptional:
		return "optional"
	case LegExcluded:
		return "excluded"
	default:
		return "unset"
	}
}

// Leg is one attribute a shape matches on.
type Leg struct {
	Attribute string
	Kind      LegKind
	// Time is this leg's own qualifier. It exists because time is a CLAUSE:
	// under a verb family a per-leg qualifier would need a second grammar.
	Time TimeClause
}

// ShapeQuery finds subjects resembling one subject.
type ShapeQuery struct {
	Subject   string
	Legs      []Leg
	Metric    string
	Threshold float64
	Time      TimeClause
}

func (*ShapeQuery) statement() {}

// Binding is one attribute's value in a result row — bound, or UNBOUND.
//
// ⚠ Unbound is distinct from a binding of the empty string. Conflating them is
// how a consumer silently treats "this subject has no nickname" as "this
// subject's nickname is blank".
type Binding struct {
	Attribute string
	value     string
	bound     bool
}

// Bound returns a binding holding a value.
func Bound(attribute, value string) Binding {
	return Binding{Attribute: attribute, value: value, bound: true}
}

// Unbound returns a binding holding nothing, which is what an optional leg that
// matched nothing produces.
func Unbound(attribute string) Binding {
	return Binding{Attribute: attribute}
}

// IsBound reports whether the binding holds a value.
func (b Binding) IsBound() bool { return b.bound }

// Value returns the value and whether there was one.
func (b Binding) Value() (string, bool) { return b.value, b.bound }

func (b Binding) String() string {
	if !b.bound {
		return b.Attribute + "=<unbound>"
	}
	return fmt.Sprintf("%s=%q", b.Attribute, b.value)
}

// Row is one result: a binding per leg, in the order the legs were written.
type Row struct {
	Subject  string
	Bindings []Binding
}

// Get returns the binding for an attribute.
func (r Row) Get(attribute string) (Binding, bool) {
	for _, b := range r.Bindings {
		if b.Attribute == attribute {
			return b, true
		}
	}
	return Binding{}, false
}

// BuildRow assembles a result row from what a leg matched.
//
// ★ This is the match semantics, and it is a pure function so that the rule is
// decidable before any evaluator exists: an evaluator supplies `matched` and
// this decides what the row looks like.
//
// ⚠ A REQUIRED leg that matched nothing drops the row. An OPTIONAL leg that
// matched nothing yields an UNBOUND binding and the row is RETURNED. If the
// optional case dropped the row too, `OPTIONAL` would mean the same as
// `REQUIRE` — and the difference would only show on data where the leg is
// sometimes absent, which is not the data anyone tests with.
//
// ⚠ An EXCLUDED leg that MATCHED drops the row, and one that matched nothing
// contributes NO BINDING (ADR-036 rule 6). ★ Binding it as `Unbound` would be
// actively wrong: `Unbound` already means "an OPTIONAL leg matched nothing", so
// the two would render identically while saying opposite things — one that the
// subject was asked for a value and had none, the other that it was required to
// have none. An excluded leg is a FILTER, and its answer is already carried by
// the row existing at all.
//
// ⚠ This narrows ADR-011's "one binding per leg" to one per leg THAT PROJECTS.
// A consumer indexing Bindings positionally against Legs would be wrong.
func BuildRow(subject string, legs []Leg, matched map[string]string) (Row, bool) {
	row := Row{Subject: subject}
	for _, leg := range legs {
		value, ok := matched[leg.Attribute]
		switch {
		case leg.Kind == LegExcluded:
			if ok {
				return Row{}, false
			}
			// Matched nothing, which is what was asked. It binds nothing.
		case ok:
			row.Bindings = append(row.Bindings, Bound(leg.Attribute, value))
		case leg.Kind == LegRequired:
			return Row{}, false
		default:
			row.Bindings = append(row.Bindings, Unbound(leg.Attribute))
		}
	}
	return row, true
}

// parseShape reads `MATCH SHAPE LIKE <subject> REQUIRE ... OPTIONAL ...
// SIMILARITY <metric> >= <threshold>` with an optional time clause.
func (p *parser) parseShape() (Statement, error) {
	if err := p.expectKeyword("MATCH"); err != nil {
		return nil, err
	}
	if err := p.expectKeyword("SHAPE"); err != nil {
		return nil, err
	}
	if err := p.expectKeyword("LIKE"); err != nil {
		return nil, err
	}
	subject, err := p.expectIdent("a subject to match against")
	if err != nil {
		return nil, err
	}
	q := &ShapeQuery{Subject: subject}

	if p.acceptKeyword("REQUIRE") {
		legs, err := p.parseLegs(LegRequired)
		if err != nil {
			return nil, err
		}
		q.Legs = append(q.Legs, legs...)
	}
	if p.acceptKeyword("OPTIONAL") {
		legs, err := p.parseLegs(LegOptional)
		if err != nil {
			return nil, err
		}
		q.Legs = append(q.Legs, legs...)
	}
	// ⚠ Through the SAME parseLegs the other two use, so an excluded leg carries
	// its own time clause without that being a special case. ADR-011's central
	// property is that time is a clause and therefore attaches per leg; a leg
	// kind that could not carry one would be the first exception to the thing
	// that record exists to hold. "Did not have a nickname AS OF 1900" is a real
	// question.
	if p.acceptKeyword("WITHOUT") {
		legs, err := p.parseLegs(LegExcluded)
		if err != nil {
			return nil, err
		}
		q.Legs = append(q.Legs, legs...)
	}

	if !p.acceptKeyword("SIMILARITY") {
		return nil, &ParseError{
			Pos:      p.peek().Pos,
			Found:    p.peek().String(),
			Expected: "keyword SIMILARITY: " + ErrNoThreshold.Error(),
		}
	}
	metric, err := p.expectIdent("a similarity metric")
	if err != nil {
		return nil, err
	}
	q.Metric = metric

	op := p.peek()
	if op.Kind != KindPunct || (op.Text != ">=" && op.Text != ">") {
		return nil, &ParseError{
			Pos:      op.Pos,
			Found:    op.String(),
			Expected: ">= or >: " + ErrNoThreshold.Error(),
		}
	}
	p.next()

	threshold := p.peek()
	if threshold.Kind != KindNumber {
		return nil, &ParseError{
			Pos:      threshold.Pos,
			Found:    threshold.String(),
			Expected: "a threshold: " + ErrNoThreshold.Error(),
		}
	}
	p.next()
	value, err := parseFloat(threshold.Text)
	if err != nil {
		return nil, &ParseError{Pos: threshold.Pos, Found: threshold.String(), Expected: "a numeric threshold"}
	}
	q.Threshold = value

	clause, err := p.parseTimeClause()
	if err != nil {
		return nil, err
	}
	q.Time = clause

	return q, nil
}

// parseLegs reads a comma-separated attribute list, each leg optionally carrying
// its own time clause.
func (p *parser) parseLegs(kind LegKind) ([]Leg, error) {
	var legs []Leg
	for {
		name, err := p.expectIdent("an attribute name")
		if err != nil {
			return nil, err
		}
		leg := Leg{Attribute: name, Kind: kind}

		// A per-leg time qualifier, which exists because time is a clause.
		if t := p.peek(); t.Kind == KindKeyword && (t.Text == "AS" || t.Text == "TRANSACTION") {
			clause, err := p.parseTimeClause()
			if err != nil {
				return nil, err
			}
			leg.Time = clause
		}
		legs = append(legs, leg)

		if t := p.peek(); t.Kind == KindPunct && t.Text == "," {
			p.next()
			continue
		}
		return legs, nil
	}
}
