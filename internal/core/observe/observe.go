package observe

import (
	"errors"
	"fmt"
	"sort"
	"sync"

	"github.com/atvirokodosprendimai/sdev1/internal/core/addr"
	"github.com/atvirokodosprendimai/sdev1/internal/core/tx"
)

var (
	// ErrUndeclaredKind reports an attempt to emit a kind nobody declared.
	//
	// ★ It is raised at EMISSION. Refusing at read time would make vocabulary
	// drift a consumer's problem months after the fact; refusing here makes it
	// fail where it happens.
	ErrUndeclaredKind = errors.New("observe: that event kind is not declared")

	// ErrNoReader reports a declaration that names nobody who consumes it.
	//
	// ⚠ This is the rule that keeps a closed vocabulary from becoming a long,
	// tidy list of things nobody looks at — which is the failure that actually
	// accumulates.
	ErrNoReader = errors.New("observe: a declared kind must name what reads it")

	// ErrDuplicateKind reports two declarations of one kind.
	ErrDuplicateKind = errors.New("observe: that event kind is already declared")

	// ErrUndeclaredField reports a field the declaration did not name.
	ErrUndeclaredField = errors.New("observe: that field is not declared for this kind")
)

// Kind is a declared event identity.
type Kind string

// Declaration is what a kind is, and who reads it.
type Declaration struct {
	Kind Kind
	// Reader names what consumes this kind — a console panel, an alert, a
	// catalogue entry. A declaration without one is refused.
	Reader string
	// Fields are the names an event of this kind may carry. An event carrying
	// anything else is refused, so a consumer's field list cannot drift from the
	// producer's.
	Fields []string
}

// Event is one thing that happened.
//
// ⚠ There is deliberately no message field. A free-form string is what every
// caller wants during an incident, and it is how a declared vocabulary becomes a
// log — after which the console is a grep again.
type Event struct {
	Kind Kind
	// Leaf is what the event concerns.
	Leaf addr.LeafID
	// At orders the event against the data it describes, rather than against
	// wall time.
	At tx.TxID
	// Fields are named values, never a formatted message.
	Fields map[string]string
}

var (
	kindsMu sync.RWMutex
	kinds   = map[Kind]Declaration{}
)

// Register declares a kind.
func Register(d Declaration) error {
	if d.Kind == "" {
		return fmt.Errorf("observe: a declaration needs a kind")
	}
	if d.Reader == "" {
		return fmt.Errorf("%w: %s", ErrNoReader, d.Kind)
	}

	kindsMu.Lock()
	defer kindsMu.Unlock()
	if existing, ok := kinds[d.Kind]; ok {
		return fmt.Errorf("%w: %s is already read by %s", ErrDuplicateKind, d.Kind, existing.Reader)
	}
	fields := make([]string, len(d.Fields))
	copy(fields, d.Fields)
	sort.Strings(fields)
	kinds[d.Kind] = Declaration{Kind: d.Kind, Reader: d.Reader, Fields: fields}
	return nil
}

// Declared returns every declaration, ordered by kind.
//
// The ordering is not cosmetic: Go randomises map iteration, so an unordered
// answer would give a consumer and a test a different vocabulary each run.
func Declared() []Declaration {
	kindsMu.RLock()
	defer kindsMu.RUnlock()
	out := make([]Declaration, 0, len(kinds))
	for _, d := range kinds {
		out = append(out, d)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Kind < out[j].Kind })
	return out
}

// DeclarationFor returns one kind's declaration.
func DeclarationFor(k Kind) (Declaration, bool) {
	kindsMu.RLock()
	defer kindsMu.RUnlock()
	d, ok := kinds[k]
	return d, ok
}

// Emit builds an event, refusing an undeclared kind or an undeclared field.
//
// ★ It returns the event rather than sending it. Where an event goes is a
// stream's business, and separating the two is what lets the vocabulary be
// checked with no transport in existence.
func Emit(k Kind, leaf addr.LeafID, at tx.TxID, fields map[string]string) (Event, error) {
	d, ok := DeclarationFor(k)
	if !ok {
		return Event{}, fmt.Errorf("%w: %s", ErrUndeclaredKind, k)
	}
	for name := range fields {
		if !containsString(d.Fields, name) {
			return Event{}, fmt.Errorf("%w: %s carries %v, and %q is not among them",
				ErrUndeclaredField, k, d.Fields, name)
		}
	}

	held := make(map[string]string, len(fields))
	for name, value := range fields {
		held[name] = value
	}
	return Event{Kind: k, Leaf: leaf, At: at, Fields: held}, nil
}

func containsString(xs []string, x string) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}
