package ql

import (
	"fmt"
	"strconv"

	"github.com/atvirokodosprendimai/sdev1/internal/core/hlc"
	"github.com/atvirokodosprendimai/sdev1/internal/core/tx"
)

// ParseError says where a parse failed and what was expected there.
//
// ★ It is part of the public contract rather than a diagnostic. A parser that
// answers "syntax error" has failed at its actual job, which for a language is
// telling a caller what to write instead.
type ParseError struct {
	// Pos is the byte offset of the token that could not be accepted.
	Pos int
	// Found describes what was there.
	Found string
	// Expected describes what would have been accepted.
	Expected string
}

func (e *ParseError) Error() string {
	return fmt.Sprintf("ql: at byte %d: found %s, expected %s", e.Pos, e.Found, e.Expected)
}

// parser is a hand-written recursive-descent parser over the token stream.
type parser struct {
	tokens []Token
	at     int
}

// Parse turns a statement into an AST.
func Parse(src string) (Statement, error) {
	p := &parser{tokens: NewLexer(src).Tokens()}
	stmt, err := p.statement()
	if err != nil {
		return nil, err
	}
	if t := p.peek(); t.Kind != KindEOF {
		return nil, p.errorAt(t, "end of statement")
	}
	return stmt, nil
}

func (p *parser) peek() Token {
	if p.at >= len(p.tokens) {
		return Token{Kind: KindEOF}
	}
	return p.tokens[p.at]
}

func (p *parser) next() Token {
	t := p.peek()
	if p.at < len(p.tokens) {
		p.at++
	}
	return t
}

func (p *parser) errorAt(t Token, expected string) error {
	return &ParseError{Pos: t.Pos, Found: t.String(), Expected: expected}
}

// acceptKeyword consumes a keyword if it is next.
func (p *parser) acceptKeyword(word string) bool {
	if t := p.peek(); t.Kind == KindKeyword && t.Text == word {
		p.next()
		return true
	}
	return false
}

func (p *parser) expectKeyword(word string) error {
	if p.acceptKeyword(word) {
		return nil
	}
	return p.errorAt(p.peek(), "keyword "+word)
}

func (p *parser) expectIdent(what string) (string, error) {
	t := p.peek()
	if t.Kind != KindIdent {
		return "", p.errorAt(t, what)
	}
	p.next()
	return t.Text, nil
}

func (p *parser) statement() (Statement, error) {
	switch t := p.peek(); {
	case t.Kind == KindKeyword && t.Text == "SELECT":
		return p.parseSelect()
	case t.Kind == KindKeyword && t.Text == "MATCH":
		return p.parseShape()
	case t.Kind == KindKeyword && t.Text == "SEARCH":
		return p.parseSearch()
	case t.Kind == KindKeyword && (t.Text == "ASSERT" || t.Text == "RETRACT"):
		return p.parseWrite()
	default:
		return nil, p.errorAt(t, "SELECT, MATCH, SEARCH, ASSERT or RETRACT")
	}
}

func (p *parser) parseSelect() (Statement, error) {
	if err := p.expectKeyword("SELECT"); err != nil {
		return nil, err
	}

	sel := &Select{}
	// `*` projects everything; otherwise a comma-separated list.
	if t := p.peek(); t.Kind == KindPunct && t.Text == "*" {
		p.next()
	} else {
		for {
			name, err := p.expectIdent("an attribute name")
			if err != nil {
				return nil, err
			}
			sel.Attributes = append(sel.Attributes, name)
			if t := p.peek(); t.Kind == KindPunct && t.Text == "," {
				p.next()
				continue
			}
			break
		}
	}

	if err := p.expectKeyword("FROM"); err != nil {
		return nil, err
	}
	entity, err := p.expectIdent("an entity name")
	if err != nil {
		return nil, err
	}
	sel.Entity = entity

	if p.acceptKeyword("WHERE") {
		pred, err := p.parsePredicate()
		if err != nil {
			return nil, err
		}
		sel.Where = pred
	}

	clause, err := p.parseTimeClause()
	if err != nil {
		return nil, err
	}
	sel.Time = clause

	return sel, nil
}

func (p *parser) parsePredicate() (*Predicate, error) {
	attr, err := p.expectIdent("an attribute name")
	if err != nil {
		return nil, err
	}
	op := p.peek()
	if op.Kind != KindPunct {
		return nil, p.errorAt(op, "a comparison operator")
	}
	switch op.Text {
	case "=", "==", "!=", "<", ">", "<=", ">=":
	default:
		return nil, p.errorAt(op, "a comparison operator")
	}
	p.next()

	val := p.peek()
	switch val.Kind {
	case KindNumber:
		p.next()
		return &Predicate{Attribute: attr, Op: op.Text, Value: val.Text, ValueIsNumber: true}, nil
	case KindString, KindIdent:
		p.next()
		return &Predicate{Attribute: attr, Op: op.Text, Value: val.Text}, nil
	default:
		return nil, p.errorAt(val, "a value")
	}
}

// parseTimeClause reads `AS OF t` and `TRANSACTION u`, in either combination.
//
// ⚠ It records what was WRITTEN and applies no default. Defaults belong to
// [TimeClause.Resolve], which forwards to the one implementation of the table.
func (p *parser) parseTimeClause() (TimeClause, error) {
	var clause TimeClause

	if p.acceptKeyword("AS") {
		if err := p.expectKeyword("OF"); err != nil {
			return clause, err
		}
		t := p.peek()
		if t.Kind != KindNumber {
			return clause, p.errorAt(t, "an instant")
		}
		p.next()
		instant, err := strconv.ParseInt(t.Text, 10, 64)
		if err != nil {
			return clause, &ParseError{Pos: t.Pos, Found: t.String(), Expected: "an integer instant"}
		}
		clause.ValidAt = &instant
	}

	if p.acceptKeyword("TRANSACTION") {
		t := p.peek()
		if t.Kind != KindNumber {
			return clause, p.errorAt(t, "a transaction reference")
		}
		p.next()
		wall, err := strconv.ParseInt(t.Text, 10, 64)
		if err != nil {
			return clause, &ParseError{Pos: t.Pos, Found: t.String(), Expected: "an integer transaction reference"}
		}
		// A transaction is written as its clock reading. A canonical textual form
		// for a full identifier is deferred with the evaluator, since nothing yet
		// produces one for a caller to copy.
		id := tx.TxID{HLC: hlc.Timestamp{Wall: wall}}
		clause.AsOf = &id
	}

	return clause, nil
}
