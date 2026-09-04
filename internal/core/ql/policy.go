package ql

import (
	"strconv"
	"strings"

	"github.com/atvirokodosprendimai/sdev1/internal/core/segment"
)

// PolicyScope is what a storage-policy clause governs.
//
// ⚠ There is exactly ONE value, and no way to express the other. A policy clause
// sets what the NEXT write produces; every block records how it was written, so
// changing a policy reinterprets nothing already stored. The language
// deliberately has no syntax for re-encoding what exists, and the absence is
// enforced by there being no scope that means it.
type PolicyScope int

// PolicyNewWritesOnly is the only scope a policy clause can have.
const PolicyNewWritesOnly PolicyScope = 1

func (s PolicyScope) String() string {
	if s == PolicyNewWritesOnly {
		return "new writes only"
	}
	return "unset"
}

// PolicyScopes returns every scope the language can express — which is one.
func PolicyScopes() []PolicyScope { return []PolicyScope{PolicyNewWritesOnly} }

// PolicyClause names the codec new writes should use.
type PolicyClause struct {
	Codec segment.CodecID
	// Name is the codec as written, for a diagnostic.
	Name string
	// Scope is always [PolicyNewWritesOnly]; the field exists so a reader of the
	// AST sees the limit rather than having to know it.
	Scope PolicyScope
}

// codecsByName are the codecs a caller may name. They resolve to identifiers the
// segment format already understands, so the language adds no second registry.
var codecsByName = map[string]segment.CodecID{
	"none":     segment.CodecIdentity,
	"identity": segment.CodecIdentity,
	"zstd":     segment.CodecZstd,
}

// ParsePolicyClause reads `WITH COMPRESSION <codec>`.
//
// ★ It is a standalone entry point because the statement that CARRIES it is a
// write, and no write statement exists yet. Defining the clause now fixes what a
// policy means; which statements accept it is decided when there is one.
func ParsePolicyClause(src string) (PolicyClause, error) {
	p := &parser{tokens: NewLexer(src).Tokens()}

	if err := p.expectKeyword("WITH"); err != nil {
		return PolicyClause{}, err
	}
	if err := p.expectKeyword("COMPRESSION"); err != nil {
		return PolicyClause{}, err
	}

	t := p.peek()
	if t.Kind != KindIdent && t.Kind != KindKeyword {
		return PolicyClause{}, p.errorAt(t, "a codec name")
	}
	p.next()

	codec, ok := codecsByName[strings.ToLower(t.Text)]
	if !ok {
		return PolicyClause{}, &ParseError{
			Pos:      t.Pos,
			Found:    t.String(),
			Expected: "a known codec: " + knownCodecs(),
		}
	}

	if rest := p.peek(); rest.Kind != KindEOF {
		return PolicyClause{}, p.errorAt(rest, "end of clause")
	}

	return PolicyClause{Codec: codec, Name: strings.ToLower(t.Text), Scope: PolicyNewWritesOnly}, nil
}

func knownCodecs() string {
	names := make([]string, 0, len(codecsByName))
	for name := range codecsByName {
		names = append(names, name)
	}
	// Stable for an error message.
	for i := 0; i < len(names); i++ {
		for j := i + 1; j < len(names); j++ {
			if names[j] < names[i] {
				names[i], names[j] = names[j], names[i]
			}
		}
	}
	return strings.Join(names, ", ")
}

func parseFloat(s string) (float64, error) { return strconv.ParseFloat(s, 64) }
