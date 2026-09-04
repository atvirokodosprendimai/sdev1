package search

import (
	"strings"
	"unicode"
)

// Term is one analysed token.
//
// It is a distinct type rather than a bare string so a caller cannot pass raw
// text where an analysed term is required — the two look identical and behave
// very differently, because only one of them has been normalised.
type Term string

// Analyze turns text into the terms an index would hold.
//
// ★ It is deliberately the simplest thing that is testable: lower-case, split on
// anything that is not a letter or a digit, drop what is left empty. Stemming,
// stop words and language detection are all corpus decisions, and choosing them
// now would bake in a language nobody measured against.
//
// It is a pure function of its input. An index and a query must analyse text the
// same way or a term will be stored in one form and looked up in another, and
// the search simply returns nothing with no error anywhere.
func Analyze(text string) []Term {
	fields := strings.FieldsFunc(strings.ToLower(text), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
	if len(fields) == 0 {
		return nil
	}
	terms := make([]Term, 0, len(fields))
	for _, f := range fields {
		terms = append(terms, Term(f))
	}
	return terms
}
