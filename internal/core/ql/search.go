package ql

import (
	"errors"
	"strconv"
)

// ErrNoSearchLimit reports a search written without a positive limit.
//
// ★ The limit is required rather than defaulted. A default is a number nobody
// wrote down deciding how much of the cluster a query touches, and search is the
// largest fan-out a single request can cause in this system.
var ErrNoSearchLimit = errors.New("ql: a search needs a positive LIMIT")

// Search finds entities by the text inside them.
//
// It is a third statement rather than a predicate because its input is a query
// over an index and its output is RANKED — neither fits the single-predicate
// WHERE, and forcing it there would need the compound predicates the language
// deliberately does not have.
type Search struct {
	// Query is the text to search for, exactly as written. It is analysed
	// later, by the same analyzer the index used.
	Query string
	// Attributes are the attributes searched. Empty is not permitted.
	Attributes []string
	// Facets are the attributes to break the matches down by, or nil.
	Facets []string
	// Limit is how many results are wanted. Always positive.
	Limit int
	// Time is the qualifier as WRITTEN, before defaults — the same clause every
	// other statement carries, rather than a second spelling of it.
	Time TimeClause
}

func (*Search) statement() {}

// parseSearch reads
// `SEARCH <text> IN <attrs> [FACET BY <attrs>] LIMIT <n>` with a time clause.
func (p *parser) parseSearch() (Statement, error) {
	if err := p.expectKeyword("SEARCH"); err != nil {
		return nil, err
	}

	text := p.peek()
	if text.Kind != KindString {
		return nil, p.errorAt(text, "a quoted search query")
	}
	p.next()
	s := &Search{Query: text.Text}

	if err := p.expectKeyword("IN"); err != nil {
		return nil, err
	}
	attrs, err := p.parseAttributeList()
	if err != nil {
		return nil, err
	}
	s.Attributes = attrs

	if p.acceptKeyword("FACET") {
		if err := p.expectKeyword("BY"); err != nil {
			return nil, err
		}
		facets, err := p.parseAttributeList()
		if err != nil {
			return nil, err
		}
		s.Facets = facets
	}

	// ⚠ Required, and refused rather than defaulted. See [ErrNoSearchLimit].
	if !p.acceptKeyword("LIMIT") {
		return nil, &ParseError{
			Pos:      p.peek().Pos,
			Found:    p.peek().String(),
			Expected: "keyword LIMIT: " + ErrNoSearchLimit.Error(),
		}
	}
	limit := p.peek()
	if limit.Kind != KindNumber {
		return nil, &ParseError{
			Pos:      limit.Pos,
			Found:    limit.String(),
			Expected: "a limit: " + ErrNoSearchLimit.Error(),
		}
	}
	p.next()
	n, err := strconv.Atoi(limit.Text)
	if err != nil || n <= 0 {
		return nil, &ParseError{
			Pos:      limit.Pos,
			Found:    limit.String(),
			Expected: "a positive whole limit: " + ErrNoSearchLimit.Error(),
		}
	}
	s.Limit = n

	clause, err := p.parseTimeClause()
	if err != nil {
		return nil, err
	}
	s.Time = clause

	return s, nil
}

// parseAttributeList reads a comma-separated list of attribute names.
func (p *parser) parseAttributeList() ([]string, error) {
	var out []string
	for {
		name, err := p.expectIdent("an attribute name")
		if err != nil {
			return nil, err
		}
		out = append(out, name)
		if t := p.peek(); t.Kind == KindPunct && t.Text == "," {
			p.next()
			continue
		}
		return out, nil
	}
}
