package ql

import (
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Kind classifies a token.
type Kind int

const (
	KindEOF Kind = iota
	KindIdent
	KindKeyword
	KindNumber
	KindString
	KindPunct
)

func (k Kind) String() string {
	switch k {
	case KindEOF:
		return "end of input"
	case KindIdent:
		return "identifier"
	case KindKeyword:
		return "keyword"
	case KindNumber:
		return "number"
	case KindString:
		return "string"
	case KindPunct:
		return "punctuation"
	default:
		return "unknown"
	}
}

// keywords are matched case-insensitively; everything else is an identifier.
var keywords = map[string]bool{
	"SELECT": true, "FROM": true, "WHERE": true,
	"AS": true, "OF": true, "TRANSACTION": true,
	"MATCH": true, "SHAPE": true, "LIKE": true,
	"REQUIRE": true, "OPTIONAL": true, "SIMILARITY": true,
	"WITH": true, "COMPRESSION": true,
}

// Token is one lexeme and where it was found.
//
// ★ Pos is a byte offset and it is part of the contract, not a diagnostic. For a
// public surface the error message is most of the usability, and an error that
// cannot point at a position is the "syntax error" this package refuses to
// produce.
type Token struct {
	Kind Kind
	Text string
	Pos  int
}

func (t Token) String() string {
	if t.Kind == KindEOF {
		return "end of input"
	}
	return fmt.Sprintf("%s %q", t.Kind, t.Text)
}

// Lexer turns text into tokens.
type Lexer struct {
	src string
	pos int
}

// NewLexer returns a lexer over src.
func NewLexer(src string) *Lexer { return &Lexer{src: src} }

// Next returns the next token, or a KindEOF token at the end.
func (l *Lexer) Next() Token {
	l.skipSpace()
	if l.pos >= len(l.src) {
		return Token{Kind: KindEOF, Pos: l.pos}
	}

	start := l.pos
	r, width := utf8.DecodeRuneInString(l.src[l.pos:])

	switch {
	case r == '\'' || r == '"':
		return l.lexString(r, start)
	case unicode.IsDigit(r) || (r == '-' && l.peekIsDigit(l.pos+width)):
		return l.lexNumber(start)
	case unicode.IsLetter(r) || r == '_':
		return l.lexWord(start)
	default:
		return l.lexPunct(start, r, width)
	}
}

// Tokens lexes the whole input, for a test or a diagnostic.
func (l *Lexer) Tokens() []Token {
	var out []Token
	for {
		t := l.Next()
		out = append(out, t)
		if t.Kind == KindEOF {
			return out
		}
	}
}

func (l *Lexer) skipSpace() {
	for l.pos < len(l.src) {
		r, width := utf8.DecodeRuneInString(l.src[l.pos:])
		if !unicode.IsSpace(r) {
			return
		}
		l.pos += width
	}
}

func (l *Lexer) peekIsDigit(at int) bool {
	if at >= len(l.src) {
		return false
	}
	r, _ := utf8.DecodeRuneInString(l.src[at:])
	return unicode.IsDigit(r)
}

func (l *Lexer) lexString(quote rune, start int) Token {
	l.pos += utf8.RuneLen(quote)
	var b strings.Builder
	for l.pos < len(l.src) {
		r, width := utf8.DecodeRuneInString(l.src[l.pos:])
		l.pos += width
		if r == quote {
			return Token{Kind: KindString, Text: b.String(), Pos: start}
		}
		b.WriteRune(r)
	}
	// Unterminated: return what there is, positioned at the opening quote, and
	// let the parser produce the error with the position already correct.
	return Token{Kind: KindString, Text: b.String(), Pos: start}
}

func (l *Lexer) lexNumber(start int) Token {
	if l.src[l.pos] == '-' {
		l.pos++
	}
	for l.pos < len(l.src) {
		r, width := utf8.DecodeRuneInString(l.src[l.pos:])
		if !unicode.IsDigit(r) && r != '.' {
			break
		}
		l.pos += width
	}
	return Token{Kind: KindNumber, Text: l.src[start:l.pos], Pos: start}
}

func (l *Lexer) lexWord(start int) Token {
	for l.pos < len(l.src) {
		r, width := utf8.DecodeRuneInString(l.src[l.pos:])
		if !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '_' && r != ':' && r != '-' {
			break
		}
		l.pos += width
	}
	text := l.src[start:l.pos]
	if keywords[strings.ToUpper(text)] {
		return Token{Kind: KindKeyword, Text: strings.ToUpper(text), Pos: start}
	}
	return Token{Kind: KindIdent, Text: text, Pos: start}
}

func (l *Lexer) lexPunct(start int, r rune, width int) Token {
	l.pos += width
	// Two-character operators.
	if l.pos < len(l.src) {
		next, nextWidth := utf8.DecodeRuneInString(l.src[l.pos:])
		pair := string(r) + string(next)
		if pair == ">=" || pair == "<=" || pair == "!=" || pair == "==" {
			l.pos += nextWidth
			return Token{Kind: KindPunct, Text: pair, Pos: start}
		}
	}
	return Token{Kind: KindPunct, Text: string(r), Pos: start}
}
