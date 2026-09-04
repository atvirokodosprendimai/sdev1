package ql

import (
	"errors"
	"strconv"

	"github.com/atvirokodosprendimai/sdev1/internal/core/temporal"
)

// ErrTransactionTimeIsNotYours reports a write that tried to state when it was
// recorded.
//
// ⚠ This is the record's whole point. Valid time is a claim about the world and
// backdating it is the ordinary, correct use of the axis. Transaction time is the
// record of when this system was TOLD, and it is the only thing that makes the
// store auditable — a caller who can set it can claim to have known something
// earlier than they did, and no query can detect it, because every query's
// evidence is the value that was forged.
var ErrTransactionTimeIsNotYours = errors.New("ql: a write states when a fact was TRUE, never when it was recorded; transaction time is assigned by the system")

// WriteOp is what a write does. There are exactly two.
//
// ⚠ There is no UPDATE and no DELETE, and adding one would not be a new feature.
// An update is a new assertion at a later transaction; a deletion is a
// retraction; an erasure is the destruction of a key. A CRUD verb would describe
// a data model this store does not have, and everything a caller then inferred
// about history and erasure would be wrong.
type WriteOp int

const (
	// OpUnset is the zero value and is never valid.
	OpUnset WriteOp = iota
	// OpAssert states that a fact held.
	OpAssert
	// OpRetract states that a fact stopped holding.
	OpRetract
)

func (o WriteOp) String() string {
	switch o {
	case OpAssert:
		return "ASSERT"
	case OpRetract:
		return "RETRACT"
	default:
		return "unset"
	}
}

// WriteOps returns every write verb the language has, which is two.
func WriteOps() []WriteOp { return []WriteOp{OpAssert, OpRetract} }

// Write states a fact, or states that one stopped holding.
//
// It names ONE entity and ONE attribute: the entity is the transaction boundary,
// and the grammar has nowhere to put a second — so a shape that could never
// commit is refused where it is written rather than at the end.
type Write struct {
	Op        WriteOp
	Entity    string
	Attribute string
	// Value is the literal as written, with quotes stripped.
	Value string
	// ValueIsNumber records whether it was written as a number, so nothing has
	// to guess later.
	ValueIsNumber bool
	// From is the start of validity from `VALID FROM t`, or nil when omitted.
	From *int64
	// To is the end from `TO u`, or nil for an open interval.
	To *int64
}

func (*Write) statement() {}

// Interval resolves the validity this write states, given the instant the write
// is being applied at.
//
// ⚠ An omitted clause is `[now, Forever)` — the write's OWN instant — and
// specifically not `[0, Forever)`. Defaulting to zero would silently claim every
// fact had been true since the beginning of time, which is both wrong and
// invisible, because nothing about the stored datom would look unusual.
//
// ★ The default is derived from the write rather than being a constant nobody
// wrote down. The alternative — requiring an explicit timestamp on every
// ordinary write — would force callers to invent one from their own clock, which
// is exactly the skew the hybrid clock exists to survive.
func (w *Write) Interval(now int64) temporal.Interval {
	from := now
	if w.From != nil {
		from = *w.From
	}
	to := int64(temporal.Forever)
	if w.To != nil {
		to = *w.To
	}
	return temporal.Interval{From: from, To: to}
}

// parseWrite reads `ASSERT|RETRACT <entity> <attribute> = <value>
// [VALID FROM t [TO u]]`.
func (p *parser) parseWrite() (Statement, error) {
	w := &Write{}
	switch {
	case p.acceptKeyword("ASSERT"):
		w.Op = OpAssert
	case p.acceptKeyword("RETRACT"):
		w.Op = OpRetract
	default:
		return nil, p.errorAt(p.peek(), "ASSERT or RETRACT")
	}

	entity, err := p.expectIdent("an entity name")
	if err != nil {
		return nil, err
	}
	w.Entity = entity

	attribute, err := p.expectIdent("an attribute name")
	if err != nil {
		return nil, err
	}
	w.Attribute = attribute

	if t := p.peek(); t.Kind != KindPunct || (t.Text != "=" && t.Text != "==") {
		return nil, p.errorAt(t, "= before the value")
	}
	p.next()

	value := p.peek()
	switch value.Kind {
	case KindNumber:
		w.Value, w.ValueIsNumber = value.Text, true
	case KindString, KindIdent:
		w.Value = value.Text
	default:
		return nil, p.errorAt(value, "a value")
	}
	p.next()

	if p.acceptKeyword("VALID") {
		if err := p.expectKeyword("FROM"); err != nil {
			return nil, err
		}
		from, err := p.expectInstant("an instant for VALID FROM")
		if err != nil {
			return nil, err
		}
		w.From = &from

		if p.acceptKeyword("TO") {
			to, err := p.expectInstant("an instant for TO")
			if err != nil {
				return nil, err
			}
			w.To = &to
		}
	}

	// ⚠ Refused by NAME rather than accepted and ignored. Every read statement
	// takes a TRANSACTION clause, so writing one here is what symmetry suggests —
	// and silently ignoring it would leave the caller believing it took effect.
	if t := p.peek(); t.Kind == KindKeyword && t.Text == "TRANSACTION" {
		return nil, &ParseError{
			Pos:      t.Pos,
			Found:    t.String(),
			Expected: "end of statement: " + ErrTransactionTimeIsNotYours.Error(),
		}
	}

	return w, nil
}

// expectInstant reads a whole-number instant.
func (p *parser) expectInstant(what string) (int64, error) {
	t := p.peek()
	if t.Kind != KindNumber {
		return 0, p.errorAt(t, what)
	}
	p.next()
	n, err := strconv.ParseInt(t.Text, 10, 64)
	if err != nil {
		return 0, &ParseError{Pos: t.Pos, Found: t.String(), Expected: what}
	}
	return n, nil
}
