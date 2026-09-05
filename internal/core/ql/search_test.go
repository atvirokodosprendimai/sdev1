package ql

import (
	"reflect"
	"strings"
	"testing"
)

func mustSearch(t *testing.T, src string) *Search {
	t.Helper()
	stmt, err := Parse(src)
	if err != nil {
		t.Fatalf("Parse(%q): %v", src, err)
	}
	s, ok := stmt.(*Search)
	if !ok {
		t.Fatalf("Parse(%q) returned %T, want *Search", src, stmt)
	}
	return s
}

// TestSearchRoundTripsThroughTheAST checks a full statement lands where a caller
// expects it.
func TestSearchRoundTripsThroughTheAST(t *testing.T) {
	got := mustSearch(t, `SEARCH 'red dwarf' IN description, notes FACET BY class, discovered_by LIMIT 20`)

	want := &Search{
		Query:      "red dwarf",
		Attributes: []string{"description", "notes"},
		Facets:     []string{"class", "discovered_by"},
		Limit:      20,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Parse = %+v, want %+v", got, want)
	}

	// Keywords are case-insensitive here as everywhere else.
	lower := mustSearch(t, `search 'red dwarf' in description limit 5`)
	if lower.Query != "red dwarf" || lower.Limit != 5 {
		t.Fatalf("a lower-case search parsed to %+v", lower)
	}
	if len(lower.Facets) != 0 {
		t.Fatalf("a search with no FACET BY carried facets: %v", lower.Facets)
	}
}

// TestSearchWithoutALimitDoesNotParse checks the limit is required rather than
// defaulted.
//
// ⚠ It asserts a parse FAILURE, not that a limit is present in the tree. A
// default would satisfy the second and defeat the rule entirely.
func TestSearchWithoutALimitDoesNotParse(t *testing.T) {
	for _, src := range []string{
		`SEARCH 'red dwarf' IN description`,
		`SEARCH 'red dwarf' IN description LIMIT 0`,
		`SEARCH 'red dwarf' IN description LIMIT -3`,
		`SEARCH 'red dwarf' IN description LIMIT many`,
		`SEARCH 'red dwarf' IN description FACET BY class`,
	} {
		stmt, err := Parse(src)
		if err == nil {
			t.Fatalf("Parse(%q) succeeded and returned %+v; an unranked, unlimited search is a "+
				"full scan with extra steps, and search is the largest fan-out one request can cause", src, stmt)
		}
		if !strings.Contains(err.Error(), ErrNoSearchLimit.Error()) {
			t.Fatalf("Parse(%q) failed with %q, which does not say the limit is the problem — a "+
				"caller learns nothing it can act on", src, err)
		}
	}

	// Positive control: the same statement WITH a limit parses, so the refusals
	// above are about the limit and not about the statement.
	if _, err := Parse(`SEARCH 'red dwarf' IN description LIMIT 1`); err != nil {
		t.Fatalf("a search with a positive limit was refused: %v", err)
	}
}

// TestSearchCarriesTheSameTimeClause checks search inherits both axes rather
// than growing a second spelling of them.
func TestSearchCarriesTheSameTimeClause(t *testing.T) {
	cases := []struct {
		src       string
		validAt   *int64
		hasAsOf   bool
		asOfWall  int64
		wantLimit int
	}{
		{src: `SEARCH 'x' IN d LIMIT 5`, wantLimit: 5},
		{src: `SEARCH 'x' IN d LIMIT 5 AS OF 1700000000`, validAt: ptr(int64(1700000000)), wantLimit: 5},
		{src: `SEARCH 'x' IN d LIMIT 5 TRANSACTION 1700000500`, hasAsOf: true, asOfWall: 1700000500, wantLimit: 5},
		{src: `SEARCH 'x' IN d LIMIT 5 AS OF 1700000000 TRANSACTION 1700000500`, validAt: ptr(int64(1700000000)), hasAsOf: true, asOfWall: 1700000500, wantLimit: 5},
	}

	for _, tc := range cases {
		s := mustSearch(t, tc.src)
		if s.Limit != tc.wantLimit {
			t.Fatalf("%q: limit %d, want %d", tc.src, s.Limit, tc.wantLimit)
		}
		switch {
		case tc.validAt == nil && s.Time.ValidAt != nil:
			t.Fatalf("%q: carried a valid-time instant %d", tc.src, *s.Time.ValidAt)
		case tc.validAt != nil && (s.Time.ValidAt == nil || *s.Time.ValidAt != *tc.validAt):
			t.Fatalf("%q: valid-time instant is %v, want %d", tc.src, s.Time.ValidAt, *tc.validAt)
		}
		switch {
		case !tc.hasAsOf && s.Time.AsOf != nil:
			t.Fatalf("%q: bound the transaction axis when nothing asked it to", tc.src)
		case tc.hasAsOf && (s.Time.AsOf == nil || s.Time.AsOf.HLC.Wall != tc.asOfWall):
			t.Fatalf("%q: transaction axis is %v, want wall %d", tc.src, s.Time.AsOf, tc.asOfWall)
		}

		// It is the SAME clause, so it resolves through the same table.
		resolved := s.Time.Resolve(1700009999)
		if resolved.ValidAt == nil {
			t.Fatalf("%q: Resolve left valid time unbound", tc.src)
		}
	}
}

func ptr[T any](v T) *T { return &v }

// TestQuotedIdentifierSurvivesAKeywordCollision is the escape hatch that pays
// for the five new keywords.
//
// ⚠ It quotes words that GENUINELY collide. Quoting an ordinary identifier would
// pass even against a lexer that ignored backticks entirely, which is the shape
// this test would otherwise have.
func TestQuotedIdentifierSurvivesAKeywordCollision(t *testing.T) {
	for _, name := range []string{"limit", "in", "select", "by", "facet", "search", "from", "where"} {
		// Unquoted, it is a keyword and the projection is refused. That is the
		// collision this test exists because of.
		if _, err := Parse("READ " + name + " FROM planet-7"); err == nil {
			t.Fatalf("the unquoted keyword %q parsed as an attribute name; if that is now legal, "+
				"this test is asserting a collision that no longer exists", name)
		}

		stmt, err := Parse("READ `" + name + "` FROM planet-7")
		if err != nil {
			t.Fatalf("READ `%s`: %v — an attribute whose name collides with a keyword must stay "+
				"addressable, or adding a keyword silently orphans data that parsed yesterday", name, err)
		}
		sel := stmt.(*Read)
		if len(sel.Attributes) != 1 || sel.Attributes[0] != name {
			t.Fatalf("READ `%s` projected %v", name, sel.Attributes)
		}
	}

	// It works in a WHERE too, and the quotes are not part of the name.
	stmt, err := Parse("READ * FROM planet-7 WHERE `limit` > 10")
	if err != nil {
		t.Fatalf("a quoted attribute in WHERE: %v", err)
	}
	if got := stmt.(*Read).Where.Attribute; got != "limit" {
		t.Fatalf("the quoted attribute came through as %q, want %q", got, "limit")
	}

	// A quoted ordinary word is still just that word, so quoting is never wrong.
	plain := mustRead(t, "READ `mass` FROM planet-7")
	if plain.Attributes[0] != "mass" {
		t.Fatalf("quoting an ordinary name changed it to %q", plain.Attributes[0])
	}
}

func mustRead(t *testing.T, src string) *Read {
	t.Helper()
	stmt, err := Parse(src)
	if err != nil {
		t.Fatalf("Parse(%q): %v", src, err)
	}
	return stmt.(*Read)
}

// TestSearchFacetsAreOptional checks FACET BY may be omitted or repeated.
func TestSearchFacetsAreOptional(t *testing.T) {
	none := mustSearch(t, `SEARCH 'x' IN d LIMIT 5`)
	if none.Facets != nil {
		t.Fatalf("facets are %v, want nil when FACET BY is absent", none.Facets)
	}

	one := mustSearch(t, `SEARCH 'x' IN d FACET BY class LIMIT 5`)
	if !reflect.DeepEqual(one.Facets, []string{"class"}) {
		t.Fatalf("facets are %v, want [class]", one.Facets)
	}

	many := mustSearch(t, `SEARCH 'x' IN d, e, f FACET BY class, era, origin LIMIT 5`)
	if !reflect.DeepEqual(many.Attributes, []string{"d", "e", "f"}) {
		t.Fatalf("attributes are %v", many.Attributes)
	}
	if !reflect.DeepEqual(many.Facets, []string{"class", "era", "origin"}) {
		t.Fatalf("facets are %v", many.Facets)
	}

	// FACET without BY is a refusal rather than a silent acceptance.
	if _, err := Parse(`SEARCH 'x' IN d FACET class LIMIT 5`); err == nil {
		t.Fatal("FACET without BY parsed")
	}
}

// TestNewKeywordsAreReserved checks the collision this task creates is real, so
// the quoting above is load-bearing rather than decorative.
func TestNewKeywordsAreReserved(t *testing.T) {
	for _, word := range []string{"SEARCH", "IN", "FACET", "BY", "LIMIT"} {
		for _, spelling := range []string{word, strings.ToLower(word)} {
			tokens := NewLexer(spelling).Tokens()
			if len(tokens) < 1 || tokens[0].Kind != KindKeyword {
				t.Fatalf("%q lexed as %v, want a keyword", spelling, tokens[0].Kind)
			}
			if tokens[0].Text != word {
				t.Fatalf("%q normalised to %q, want %q", spelling, tokens[0].Text, word)
			}
		}
	}
}
