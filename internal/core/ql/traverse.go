package ql

import (
	"errors"
	"strconv"
)

// ErrNoDepth reports a traversal written without a positive depth.
//
// ★ Required rather than defaulted, matching the walk itself. An unbounded walk
// over a graph the caller does not control is a scan they did not ask for, and a
// default would be a number nobody wrote down deciding how much of it to touch.
var ErrNoDepth = errors.New("ql: a traversal needs a positive DEPTH")

// Traverse walks the links out of one entity.
//
// ⚠ It carries exactly ONE time clause, for the whole walk, and there is
// deliberately no per-hop qualifier. A shape query has one per leg and the
// symmetry is tempting — but here it would let a caller ASK for a tree assembled
// from several instants, which is a shape that never existed. Making that
// sayable would turn the defect ADR-023 exists to prevent into a feature.
type Traverse struct {
	// Root is the entity to walk from.
	Root string
	// Depth is how many hops. Always positive.
	Depth int
	// Time is the qualifier as WRITTEN — one clause, applied at every hop.
	Time TimeClause
}

func (*Traverse) statement() {}

// parseTraverse reads `TRAVERSE <entity> DEPTH <n>` with a time clause.
func (p *parser) parseTraverse() (Statement, error) {
	if err := p.expectKeyword("TRAVERSE"); err != nil {
		return nil, err
	}
	root, err := p.expectIdent("an entity to walk from")
	if err != nil {
		return nil, err
	}
	t := &Traverse{Root: root}

	if !p.acceptKeyword("DEPTH") {
		return nil, &ParseError{
			Pos:      p.peek().Pos,
			Found:    p.peek().String(),
			Expected: "keyword DEPTH: " + ErrNoDepth.Error(),
		}
	}
	depth := p.peek()
	if depth.Kind != KindNumber {
		return nil, &ParseError{
			Pos:      depth.Pos,
			Found:    depth.String(),
			Expected: "a depth: " + ErrNoDepth.Error(),
		}
	}
	p.next()
	n, err := strconv.Atoi(depth.Text)
	if err != nil || n <= 0 {
		return nil, &ParseError{
			Pos:      depth.Pos,
			Found:    depth.String(),
			Expected: "a positive whole depth: " + ErrNoDepth.Error(),
		}
	}
	t.Depth = n

	clause, err := p.parseTimeClause()
	if err != nil {
		return nil, err
	}
	t.Time = clause

	return t, nil
}
