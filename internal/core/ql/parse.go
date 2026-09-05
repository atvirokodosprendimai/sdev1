package ql

import (
	"errors"
	"fmt"
	"strconv"

	"github.com/atvirokodosprendimai/sdev1/internal/core/hlc"
	"github.com/atvirokodosprendimai/sdev1/internal/core/tx"
)

// ErrSelectRenamed reports that a statement began with SELECT, which ADR-034
// replaced with READ.
//
// ★ The old verb is REFUSED rather than accepted as an alias, and refused BY
// NAME rather than merely unknown: every example written before the rename says
// SELECT, so a caller who types it should be told what to type instead. Two
// spellings of one verb would be two things to document and a permanent question
// about which is canonical.
var ErrSelectRenamed = errors.New("ql: SELECT was renamed to READ")

// ErrJoinNotSupported reports an attribute whose `->` marker disagrees with what
// FROM named.
//
// ★ Inside `FROM [e]` every attribute belongs to a MEMBER and is written `->a`. A
// bare `a` would have to mean `e`'s OWN attribute — a join, which is not
// implemented — so it is refused rather than treated as a synonym. ⚠ That refusal
// is what keeps the join addable: if both spellings parsed today, adding it later
// would silently change what already-written statements mean.
var ErrJoinNotSupported = errors.New("ql: an attribute of the entity named by FROM cannot be " +
	"read alongside its referrers; that is a join, and it is not implemented")

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
	// Err is the sentinel this failure matches, when it has one. Most parse
	// errors are positional and carry none.
	Err error
}

func (e *ParseError) Error() string {
	return fmt.Sprintf("ql: at byte %d: found %s, expected %s", e.Pos, e.Found, e.Expected)
}

// Unwrap exposes [ParseError.Err] so a caller can match a named refusal with
// [errors.Is] instead of comparing message text.
func (e *ParseError) Unwrap() error { return e.Err }

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
	case t.Kind == KindKeyword && t.Text == "READ":
		return p.parseRead()
	case t.Kind == KindKeyword && t.Text == "SELECT":
		// ⚠ Refused by name, not merely unrecognised. See [ErrSelectRenamed].
		return nil, &ParseError{
			Pos:      t.Pos,
			Found:    t.String(),
			Expected: "READ, which replaced SELECT",
			Err:      ErrSelectRenamed,
		}
	case t.Kind == KindKeyword && t.Text == "MATCH":
		return p.parseShape()
	case t.Kind == KindKeyword && t.Text == "SEARCH":
		return p.parseSearch()
	case t.Kind == KindKeyword && (t.Text == "ASSERT" || t.Text == "RETRACT"):
		return p.parseWrite()
	case t.Kind == KindKeyword && t.Text == "TRAVERSE":
		return p.parseTraverse()
	default:
		return nil, p.errorAt(t, "READ, MATCH, SEARCH, ASSERT, RETRACT or TRAVERSE")
	}
}

func (p *parser) parseRead() (Statement, error) {
	if err := p.expectKeyword("READ"); err != nil {
		return nil, err
	}

	sel := &Read{}
	// `*` projects everything; otherwise a comma-separated list.
	var projected []attribute
	if t := p.peek(); t.Kind == KindPunct && t.Text == "*" {
		p.next()
	} else {
		for {
			a, err := p.parseAttribute()
			if err != nil {
				return nil, err
			}
			projected = append(projected, a)
			sel.Attributes = append(sel.Attributes, a.name)
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
	entity, inbound, err := p.parseSource()
	if err != nil {
		return nil, err
	}
	sel.Entity, sel.Inbound = entity, inbound

	// ⚠ Checked AFTER the source, because that is what decides which spelling is
	// correct — the projection is written first and can only be judged second.
	if err := p.checkMarkers(projected, sel); err != nil {
		return nil, err
	}

	if p.acceptKeyword("WHERE") {
		pred, on, err := p.parsePredicate()
		if err != nil {
			return nil, err
		}
		if err := p.checkMarkers([]attribute{on}, sel); err != nil {
			return nil, err
		}
		sel.Where = pred
	}

	// ⚠ A CLAUSE of its own, not a predicate (ADR-036 rule 1). `WHERE` holds
	// exactly one comparison by ADR-011, so folding absence into it would force
	// `AND` into the grammar to express "has this and lacks that" — which is the
	// only form anybody asks. Two clauses conjoin by being two clauses.
	if p.acceptKeyword("WITHOUT") {
		excluded, err := p.parseMarkedAttributes()
		if err != nil {
			return nil, err
		}
		if err := p.checkMarkers(excluded, sel); err != nil {
			return nil, err
		}
		for _, a := range excluded {
			sel.Without = append(sel.Without, a.name)
		}
	}

	page, err := p.parsePage(sel)
	if err != nil {
		return nil, err
	}
	sel.Page = page

	clause, err := p.parseTimeClause()
	if err != nil {
		return nil, err
	}
	sel.Time = clause

	return sel, nil
}

// attribute is one attribute name as WRITTEN: its spelling, whether it carried
// the `->` marker, and where it was, so a marker that disagrees with the source
// can be reported at the place the caller typed it.
type attribute struct {
	name   string
	marked bool
	pos    int
}

// parseAttribute reads `name` or `->name`.
//
// ⚠ The marker is GRAMMAR and never part of the name, exactly as the backticks
// around a quoted identifier are not. Storing it would make `->name` and `name`
// different attributes in the store, which is a data model invented by a parser.
func (p *parser) parseAttribute() (attribute, error) {
	t := p.peek()
	marked := false
	if t.Kind == KindPunct && t.Text == RefMarker {
		p.next()
		marked = true
	}
	name, err := p.expectIdent("an attribute name")
	if err != nil {
		return attribute{}, err
	}
	return attribute{name: name, marked: marked, pos: t.Pos}, nil
}

// parseSource reads what FROM names: `e` is one entity, `[e]` is the SET of
// entities that point at it (ADR-035).
//
// ★ The identifier is stored without its brackets. They are a property of the
// SOURCE, not of the name — the same entity is addressed either way, and which
// question is being asked is what differs.
func (p *parser) parseSource() (entity string, inbound bool, err error) {
	t := p.peek()
	if !(t.Kind == KindPunct && t.Text == "[") {
		name, err := p.expectIdent("an entity name")
		return name, false, err
	}
	p.next()
	name, err := p.expectIdent("an entity name")
	if err != nil {
		return "", false, err
	}
	if closing := p.peek(); !(closing.Kind == KindPunct && closing.Text == "]") {
		return "", false, p.errorAt(closing, `a closing "]"`)
	}
	p.next()
	return name, true, nil
}

// checkMarkers refuses an attribute whose marker disagrees with the source.
//
// ⚠ ADR-035 rule 3, and it is the rule that keeps a join addable. Inside an
// inbound read a bare name would have to mean the INDEX entity's own attribute,
// which is a join and is not implemented. If the two spellings were synonyms
// today, adding the join later would silently change what already-written
// statements mean rather than failing them.
func (p *parser) checkMarkers(attrs []attribute, sel *Read) error {
	for _, a := range attrs {
		if a.marked == sel.Inbound {
			continue
		}
		found := "attribute " + strconv.Quote(a.name)
		expected := strconv.Quote(RefMarker+a.name) +
			", because FROM names a set: a bare name would read " + strconv.Quote(sel.Entity) +
			"'s own attribute, which is a join and is not implemented"
		if a.marked {
			found = "attribute " + strconv.Quote(RefMarker+a.name)
			expected = strconv.Quote(a.name) +
				", because FROM names one entity; write FROM [" + sel.Entity +
				"] to read the entities that point at it"
		}
		return &ParseError{Pos: a.pos, Found: found, Expected: expected, Err: ErrJoinNotSupported}
	}
	return nil
}

// parsePage reads `LIMIT n [OFFSET m]`.
//
// ⚠ Refused on a read of ONE entity: its attributes are a shape rather than a
// sequence, so there is nothing to page and any answer would be arbitrary.
func (p *parser) parsePage(sel *Read) (Page, error) {
	var page Page

	if t := p.peek(); t.Kind == KindKeyword && t.Text == "OFFSET" {
		return page, p.errorAt(t, "keyword LIMIT before OFFSET, since an offset with no limit "+
			"names a starting point and no page")
	}

	limit := p.peek()
	if !p.acceptKeyword("LIMIT") {
		return page, nil
	}
	if !sel.Inbound {
		return page, p.errorAt(limit, "no paging clause, because a read of one entity returns "+
			"its attributes and has nothing to page; write FROM ["+sel.Entity+"] to read a set")
	}

	n, err := p.parseBound("a row count that is zero or more")
	if err != nil {
		return page, err
	}
	page.Limit, page.Has = n, true

	if p.acceptKeyword("OFFSET") {
		off, err := p.parseBound("an offset that is zero or more")
		if err != nil {
			return page, err
		}
		page.Offset = off
	}
	return page, nil
}

// parseBound reads one non-negative integer bound.
//
// ⚠ A negative bound is refused rather than clamped. `LIMIT -1` means nothing,
// and silently reading it as zero or as unlimited picks one of two opposite
// answers on the caller's behalf.
func (p *parser) parseBound(what string) (int64, error) {
	t := p.peek()
	if t.Kind != KindNumber {
		return 0, p.errorAt(t, what)
	}
	p.next()
	n, err := strconv.ParseInt(t.Text, 10, 64)
	if err != nil || n < 0 {
		return 0, &ParseError{Pos: t.Pos, Found: t.String(), Expected: what}
	}
	return n, nil
}

// parsePredicate reads one comparison, and reports the attribute AS WRITTEN so
// the caller of this function can check its marker against the source.
func (p *parser) parsePredicate() (*Predicate, attribute, error) {
	on, err := p.parseAttribute()
	if err != nil {
		return nil, attribute{}, err
	}
	op := p.peek()
	if op.Kind != KindPunct {
		return nil, attribute{}, p.errorAt(op, "a comparison operator")
	}
	switch op.Text {
	case "=", "==", "!=", "<", ">", "<=", ">=":
	default:
		return nil, attribute{}, p.errorAt(op, "a comparison operator")
	}
	p.next()

	val := p.peek()
	switch val.Kind {
	case KindNumber:
		p.next()
		return &Predicate{Attribute: on.name, Op: op.Text, Value: val.Text, ValueIsNumber: true}, on, nil
	case KindString, KindIdent:
		p.next()
		return &Predicate{Attribute: on.name, Op: op.Text, Value: val.Text}, on, nil
	default:
		return nil, attribute{}, p.errorAt(val, "a value")
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

// parseMarkedAttributes reads one or more comma-separated attributes, each of
// which may carry the `->` marker.
//
// ⚠ Distinct from [parser.parseAttributeList], which serves `SEARCH IN` and
// takes plain names. SEARCH names attributes to search, not attributes of a
// member, so the marker rule does not apply there and must not leak in.
func (p *parser) parseMarkedAttributes() ([]attribute, error) {
	var out []attribute
	for {
		a, err := p.parseAttribute()
		if err != nil {
			return nil, err
		}
		out = append(out, a)
		if t := p.peek(); t.Kind == KindPunct && t.Text == "," {
			p.next()
			continue
		}
		return out, nil
	}
}
