package ql

import (
	"errors"
	"slices"
	"strings"
	"testing"
)

// TestLexerSpansAndPositions checks tokens carry accurate byte offsets, so an
// error can point at the right place.
func TestLexerSpansAndPositions(t *testing.T) {
	src := `SELECT name, age FROM person WHERE age >= 30 AS OF -500`
	tokens := NewLexer(src).Tokens()

	if len(tokens) == 0 || tokens[len(tokens)-1].Kind != KindEOF {
		t.Fatal("the token stream does not end with EOF")
	}

	// Every token's position points at its own text in the source.
	for i, tok := range tokens {
		if tok.Kind == KindEOF {
			continue
		}
		if tok.Pos < 0 || tok.Pos >= len(src) {
			t.Fatalf("token %d (%s) has position %d, outside the %d-byte source",
				i, tok, tok.Pos, len(src))
		}
		if tok.Kind == KindIdent || tok.Kind == KindNumber || tok.Kind == KindPunct {
			if !strings.HasPrefix(src[tok.Pos:], tok.Text) {
				t.Errorf("token %d (%s) claims position %d, where the source reads %q",
					i, tok, tok.Pos, src[tok.Pos:min(tok.Pos+len(tok.Text), len(src))])
			}
		}
	}

	// Keywords are recognised case-insensitively and normalised.
	lower := NewLexer("select FROM Where").Tokens()
	for _, tok := range lower[:3] {
		if tok.Kind != KindKeyword {
			t.Errorf("%q lexed as %s, want a keyword", tok.Text, tok.Kind)
		}
		if tok.Text != strings.ToUpper(tok.Text) {
			t.Errorf("keyword %q was not normalised to upper case", tok.Text)
		}
	}

	// An identifier that merely contains a keyword is not one.
	ident := NewLexer("selection").Tokens()
	if ident[0].Kind != KindIdent {
		t.Errorf("%q lexed as %s, want an identifier", ident[0].Text, ident[0].Kind)
	}

	// Strings, negative numbers and two-character operators.
	mixed := NewLexer(`'a b' -12 >= != <=`).Tokens()
	want := []string{"a b", "-12", ">=", "!=", "<="}
	for i, w := range want {
		if i >= len(mixed) || mixed[i].Text != w {
			t.Errorf("token %d = %q, want %q", i, mixed[i].Text, w)
		}
	}
	if mixed[0].Kind != KindString {
		t.Errorf("quoted text lexed as %s, want a string", mixed[0].Kind)
	}
	if mixed[1].Kind != KindNumber {
		t.Errorf("a negative number lexed as %s, want a number", mixed[1].Kind)
	}
}

// TestParseErrorNamesPositionAndExpectation checks the error message is part of
// the contract rather than a bare "syntax error".
func TestParseErrorNamesPositionAndExpectation(t *testing.T) {
	for _, c := range []struct {
		src      string
		expected string
	}{
		{"", "SELECT"},
		{"DELETE FROM person", "SELECT"},
		{"SELECT FROM person", "an attribute"},
		{"SELECT name person", "keyword FROM"},
		{"SELECT name FROM", "an entity"},
		{"SELECT name FROM person WHERE", "an attribute"},
		{"SELECT name FROM person WHERE age", "comparison operator"},
		{"SELECT name FROM person WHERE age >", "a value"},
		{"SELECT name FROM person AS 500", "keyword OF"},
		{"SELECT name FROM person AS OF person", "an instant"},
		{"SELECT name FROM person TRANSACTION person", "a transaction"},
		{"SELECT name FROM person EXTRA", "end of statement"},
	} {
		_, err := Parse(c.src)
		if err == nil {
			t.Errorf("Parse(%q) succeeded, want an error", c.src)
			continue
		}
		var pe *ParseError
		if !errors.As(err, &pe) {
			t.Errorf("Parse(%q) returned %T, want a *ParseError", c.src, err)
			continue
		}
		if pe.Pos < 0 {
			t.Errorf("Parse(%q): position %d", c.src, pe.Pos)
		}
		if !strings.Contains(pe.Expected, c.expected) {
			t.Errorf("Parse(%q): expected %q, want it to mention %q", c.src, pe.Expected, c.expected)
		}
		if pe.Found == "" {
			t.Errorf("Parse(%q): the error does not say what was found", c.src)
		}
		if msg := pe.Error(); !strings.Contains(msg, "expected") || !strings.Contains(msg, "found") {
			t.Errorf("Parse(%q): message %q does not say both what was found and what was "+
				"expected — for a public contract the error is most of the usability", c.src, msg)
		}
	}

	// A valid statement produces no error, so the cases above are about the
	// failures rather than about a parser that refuses everything.
	if _, err := Parse("SELECT name FROM person"); err != nil {
		t.Errorf("a valid statement was refused: %v", err)
	}
}

// TestSelectRoundTripsThroughTheAST checks a statement parses to the AST a
// caller expects, with each of the four time-clause shapes.
func TestSelectRoundTripsThroughTheAST(t *testing.T) {
	sel := parseSelect(t, "SELECT name, email FROM person WHERE age >= 30")
	if !slices.Equal(sel.Attributes, []string{"name", "email"}) {
		t.Errorf("attributes = %v, want [name email]", sel.Attributes)
	}
	if sel.Entity != "person" {
		t.Errorf("entity = %q, want person", sel.Entity)
	}
	if sel.Where == nil {
		t.Fatal("the WHERE clause was dropped")
	}
	if sel.Where.Attribute != "age" || sel.Where.Op != ">=" || sel.Where.Value != "30" {
		t.Errorf("predicate = %+v, want age >= 30", *sel.Where)
	}
	if !sel.Where.ValueIsNumber {
		t.Error("a numeric literal was not recorded as one, so an evaluator would have to guess")
	}

	// `*` projects everything, which is the empty attribute list.
	star := parseSelect(t, "SELECT * FROM person")
	if len(star.Attributes) != 0 {
		t.Errorf("SELECT * gave attributes %v, want none — empty means every attribute", star.Attributes)
	}

	// No WHERE means no predicate, rather than an empty one that matches nothing.
	if star.Where != nil {
		t.Error("a statement with no WHERE carries a predicate")
	}

	// A string literal is not marked as a number.
	str := parseSelect(t, "SELECT name FROM person WHERE city = 'Vilnius'")
	if str.Where.Value != "Vilnius" || str.Where.ValueIsNumber {
		t.Errorf("predicate = %+v, want the string Vilnius", *str.Where)
	}

	// All four time-clause shapes attach to the same statement form, which is
	// the property that made a clause preferable to a verb family.
	for _, c := range []struct {
		src        string
		hasInstant bool
		hasTx      bool
	}{
		{"SELECT name FROM person", false, false},
		{"SELECT name FROM person AS OF 500", true, false},
		{"SELECT name FROM person AS OF 500 TRANSACTION 900", true, true},
		{"SELECT name FROM person TRANSACTION 900", false, true},
	} {
		got := parseSelect(t, c.src)
		if (got.Time.ValidAt != nil) != c.hasInstant {
			t.Errorf("%q: instant present = %v, want %v", c.src, got.Time.ValidAt != nil, c.hasInstant)
		}
		if (got.Time.AsOf != nil) != c.hasTx {
			t.Errorf("%q: transaction present = %v, want %v", c.src, got.Time.AsOf != nil, c.hasTx)
		}
		// The rest of the statement is unaffected by which clause is attached.
		if got.Entity != "person" || !slices.Equal(got.Attributes, []string{"name"}) {
			t.Errorf("%q: the time clause changed the rest of the statement: %+v", c.src, got)
		}
	}
}
