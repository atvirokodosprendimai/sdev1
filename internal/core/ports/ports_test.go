package ports

import (
	"context"
	"reflect"
	"strings"
	"testing"
)

// mutatingVerbs names the method-name prefixes that would let a holder change
// stored state. Reader's method set is checked against this, so widening Reader
// is a test failure rather than a review miss.
var mutatingVerbs = []string{
	"Append", "Write", "Put", "Set", "Save", "Store", "Delete", "Remove",
	"Update", "Insert", "Commit", "Retract", "Purge", "Truncate", "Apply",
}

func iface[T any]() reflect.Type { return reflect.TypeOf((*T)(nil)).Elem() }

// TestReadModelCannotWrite is the falsifier ADR-003 names in its Enforced-by
// header.
//
// The rule is not "read models should not write" — that is prose, and prose
// holds until somebody is in a hurry. The rule is that a read model is handed
// something with no write method, so writing is not expressible. This asserts
// exactly that: satisfying Reader does not thereby satisfy Writer.
func TestReadModelCannotWrite(t *testing.T) {
	reader, writer := iface[Reader](), iface[Writer]()

	if reader.Implements(writer) {
		t.Fatal("Reader implements Writer: anything handed a Reader can write, and the " +
			"read/write split is decorative")
	}
	if reader.NumMethod() == 0 {
		t.Fatal("Reader has no methods; the assertion above would pass vacuously")
	}
	if writer.NumMethod() == 0 {
		t.Fatal("Writer has no methods; the assertion above would pass vacuously")
	}
}

// TestReaderHasNoWriteMethod enumerates Reader's method set by name, so adding
// a mutating method to Reader fails here rather than silently widening what
// every read model in the system may do.
func TestReaderHasNoWriteMethod(t *testing.T) {
	reader := iface[Reader]()
	for i := 0; i < reader.NumMethod(); i++ {
		name := reader.Method(i).Name
		for _, verb := range mutatingVerbs {
			if strings.HasPrefix(name, verb) {
				t.Errorf("Reader has method %q, which begins with the mutating verb %q — "+
					"every read model would gain the ability to %s", name, verb, verb)
			}
		}
	}
}

// TestInboundIsSeparateFromReader checks the scan capability stays its own port.
//
// ★ ADR-035 rule 7. `Reader` is entity-addressed — `Load` answers about an
// identifier the caller already holds — and "what points at this" is a scan.
// ⚠ Folding `Referrers` into `Reader` would make EVERY implementation claim it,
// including a future routed remote reader that can serve one entity and cannot
// scan a leaf. The separation is what lets a reader be honest about which of the
// two it can do, and it is checked here because merging them is a one-line change
// that would otherwise be a review miss.
func TestInboundIsSeparateFromReader(t *testing.T) {
	reader, inbound, writer := iface[Reader](), iface[Inbound](), iface[Writer]()

	if inbound.NumMethod() == 0 {
		t.Fatal("Inbound has no methods; every assertion here would pass vacuously")
	}
	if reader.Implements(inbound) {
		t.Error("Reader satisfies Inbound, so every reader now claims it can scan for " +
			"referrers — including ones that can only serve a single entity")
	}
	if inbound.Implements(reader) {
		t.Error("Inbound satisfies Reader, so asking for a scan also demands entity reads; " +
			"the two capabilities must be requestable separately")
	}

	// It is a READ capability, so the same rule that guards Reader guards it.
	if inbound.Implements(writer) {
		t.Error("Inbound implements Writer")
	}
	for i := 0; i < inbound.NumMethod(); i++ {
		name := inbound.Method(i).Name
		for _, verb := range mutatingVerbs {
			if strings.HasPrefix(name, verb) {
				t.Errorf("Inbound has method %q, which begins with the mutating verb %q", name, verb)
			}
		}
	}
}

// TestStoreSatisfiesBothHalves checks Store is exactly Reader plus Writer, so
// the write path is handed one thing rather than assembling two by hand.
func TestStoreSatisfiesBothHalves(t *testing.T) {
	store, reader, writer := iface[Store](), iface[Reader](), iface[Writer]()

	if !store.Implements(reader) {
		t.Error("Store does not satisfy Reader; the write path could not answer its own questions")
	}
	if !store.Implements(writer) {
		t.Error("Store does not satisfy Writer")
	}
	if got, want := store.NumMethod(), reader.NumMethod()+writer.NumMethod(); got != want {
		t.Errorf("Store has %d methods, want %d (Reader's %d plus Writer's %d) — "+
			"Store must be the two halves and nothing extra", got, want, reader.NumMethod(), writer.NumMethod())
	}
}

// TestSnapshotCarriesATransactionIdentifier checks a snapshot is the two values
// the visibility predicate needs and nothing more, so a caller cannot supply one
// axis and silently omit the other.
func TestSnapshotCarriesATransactionIdentifier(t *testing.T) {
	st := reflect.TypeOf(Snapshot{})
	if st.NumField() != 2 {
		t.Fatalf("Snapshot has %d fields, want exactly 2 (a transaction identifier and a business instant)", st.NumField())
	}
	if _, ok := st.FieldByName("At"); !ok {
		t.Error("Snapshot has no At field: the transaction axis is unbound")
	}
	if _, ok := st.FieldByName("ValidAt"); !ok {
		t.Error("Snapshot has no ValidAt field: the business axis is unbound")
	}
}

// TestPublisherCarriesAnIdentifierNotState checks a notification cannot carry
// rendered state, so a slow consumer cannot apply an older render over a newer
// one.
func TestPublisherCarriesAnIdentifierNotState(t *testing.T) {
	pub := iface[Publisher]()
	m, ok := pub.MethodByName("Publish")
	if !ok {
		t.Fatal("Publisher has no Publish method")
	}
	datom := reflect.TypeOf(Datom{})
	for i := 0; i < m.Type.NumIn(); i++ {
		in := m.Type.In(i)
		if in == datom || (in.Kind() == reflect.Slice && in.Elem() == datom) {
			t.Errorf("Publish takes %v: a notification must carry an identifier, never state, "+
				"or an older render can arrive last and be applied", in)
		}
	}
	if m.Type.NumIn() < 2 {
		t.Error("Publish takes too few arguments to name what changed")
	}
}

// TestDatomCarriesRetractionExplicitly checks withdrawal is a flag rather than
// an omission, so "no longer true" is distinguishable from "never recorded".
func TestDatomCarriesRetractionExplicitly(t *testing.T) {
	f, ok := reflect.TypeOf(Datom{}).FieldByName("Assert")
	if !ok {
		t.Fatal("Datom has no Assert field: a retraction could only be expressed as an absence, " +
			"which cannot be distinguished from a fact that was never recorded")
	}
	if f.Type.Kind() != reflect.Bool {
		t.Errorf("Datom.Assert is %v, want a bool", f.Type)
	}
}

// TestReaderIsUsableWithoutAnyWriter is the positive half: a read model built
// against Reader compiles and runs with no writable port anywhere in reach.
func TestReaderIsUsableWithoutAnyWriter(t *testing.T) {
	// A read model. It is handed a Reader and there is nothing else it could
	// have been handed — this function body is the demonstration.
	shapeOf := func(ctx context.Context, r Reader, entity string, at Snapshot) ([]string, error) {
		return r.Attributes(ctx, entity, at)
	}

	got, err := shapeOf(context.Background(), stubReader{attrs: []string{"name", "email"}}, "e", Snapshot{})
	if err != nil {
		t.Fatalf("read model returned an error: %v", err)
	}
	if len(got) != 2 {
		t.Errorf("read model returned %v, want two attributes", got)
	}
}

// stubReader satisfies Reader and deliberately nothing more.
type stubReader struct{ attrs []string }

func (s stubReader) Load(context.Context, string, Snapshot) ([]Datom, error) { return nil, nil }
func (s stubReader) Attributes(context.Context, string, Snapshot) ([]string, error) {
	return s.attrs, nil
}
