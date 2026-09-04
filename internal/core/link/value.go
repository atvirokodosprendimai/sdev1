package link

// Kind says whether a value is data or a reference. There are two, and no third.
type Kind int

const (
	// KindUnset is the zero value and is never valid.
	//
	// ★ It exists so a zero-valued Value is detectably wrong rather than
	// silently behaving like a literal — which is the safer of the two to
	// default to, and therefore the one that would hide the mistake longest.
	KindUnset Kind = iota
	// KindLiteral: the bytes are the value.
	KindLiteral
	// KindReference: the bytes name another entity.
	KindReference
)

func (k Kind) String() string {
	switch k {
	case KindLiteral:
		return "literal"
	case KindReference:
		return "reference"
	default:
		return "unset"
	}
}

// Kinds returns every kind a value can have, which is two.
func Kinds() []Kind { return []Kind{KindLiteral, KindReference} }

// Value is a datom's value and what it means.
//
// ⚠ The kind is a FIELD, not a guess. "planet-9" as a name and "planet-9" as a
// link are the same nine bytes; only a stored kind can tell them apart, and
// inferring from shape makes the graph change when unrelated data does.
type Value struct {
	Bytes []byte
	Kind  Kind
}

// Literal returns a value that is data.
func Literal(b []byte) Value { return Value{Bytes: b, Kind: KindLiteral} }

// Ref returns a value that refers to an entity.
func Ref(entity string) Value { return Value{Bytes: []byte(entity), Kind: KindReference} }

// IsReference reports whether this value points at another entity.
func (v Value) IsReference() bool { return v.Kind == KindReference }

// Target returns the entity a reference names, and whether it is one.
//
// ⚠ A literal returns false however much its bytes look like an entity name.
// That is the whole point of storing the kind.
func (v Value) Target() (string, bool) {
	if v.Kind != KindReference {
		return "", false
	}
	return string(v.Bytes), true
}

func (v Value) String() string {
	if v.Kind == KindReference {
		return "->" + string(v.Bytes)
	}
	return string(v.Bytes)
}
