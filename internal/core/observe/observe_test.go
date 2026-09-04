package observe

import (
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/atvirokodosprendimai/sdev1/internal/core/addr"
	"github.com/atvirokodosprendimai/sdev1/internal/core/tx"
)

func testLeaf() addr.LeafID {
	var l addr.LeafID
	l.Prefix[0] = 0x31
	l.Depth = 1
	return l
}

// emittedKinds finds every Kind constant this package declares in source, so the
// vocabulary check compares the registry against what the code actually names
// rather than against a list kept beside it.
func emittedKinds(t *testing.T) []Kind {
	t.Helper()
	sources, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatalf("globbing: %v", err)
	}
	fset := token.NewFileSet()
	var found []Kind
	for _, path := range sources {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		src, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("reading %s: %v", path, err)
		}
		file, err := parser.ParseFile(fset, path, src, 0)
		if err != nil {
			t.Fatalf("parsing %s: %v", path, err)
		}
		ast.Inspect(file, func(n ast.Node) bool {
			spec, ok := n.(*ast.ValueSpec)
			if !ok {
				return true
			}
			ident, ok := spec.Type.(*ast.Ident)
			if !ok || ident.Name != "Kind" {
				return true
			}
			for _, value := range spec.Values {
				lit, ok := value.(*ast.BasicLit)
				if !ok || lit.Kind != token.STRING {
					continue
				}
				found = append(found, Kind(strings.Trim(lit.Value, `"`)))
			}
			return true
		})
	}
	return found
}

// TestEveryEmittedEventIsDeclared is the falsifier ADR-012 names in its
// Enforced-by header.
//
// ⚠ It checks BOTH directions. Every kind the code names must be declared —
// that catches drift. Every declaration must name a reader — that catches the
// long, tidy list nobody looks at, which is the failure that actually
// accumulates.
func TestEveryEmittedEventIsDeclared(t *testing.T) {
	declared := Declared()
	if len(declared) == 0 {
		t.Fatal("no event kinds are declared; this check is passing because it looks at nothing")
	}

	named := emittedKinds(t)
	if len(named) == 0 {
		t.Fatal("no Kind constants were found in this package's source; the scan is looking at " +
			"nothing, which is indistinguishable from a package with a complete vocabulary")
	}

	// Direction one: every kind the code names is declared.
	for _, k := range named {
		if _, ok := DeclarationFor(k); !ok {
			t.Errorf("kind %q is named in the source and not declared — it can be referred to "+
				"and never emitted, and a consumer would meet a shape nobody registered", k)
		}
	}

	// Direction two: every declaration names a reader, and the kind exists.
	for _, d := range declared {
		if d.Reader == "" {
			t.Errorf("kind %q declares no reader; a closed vocabulary whose entries nobody "+
				"consumes is just a long tidy list", d.Kind)
		}
		if !slices.Contains(named, d.Kind) {
			t.Errorf("kind %q is declared and named nowhere in the source — either it was "+
				"renamed or it stopped being emitted, and the declaration still reads as current",
				d.Kind)
		}
		if len(d.Fields) == 0 {
			t.Errorf("kind %q declares no fields; an event with nothing in it answers nothing", d.Kind)
		}
	}
}

// TestUndeclaredKindIsRefused checks emission is where drift fails.
func TestUndeclaredKindIsRefused(t *testing.T) {
	_, err := Emit(Kind("something.nobody.declared"), testLeaf(), tx.TxID{}, nil)
	if !errors.Is(err, ErrUndeclaredKind) {
		t.Fatalf("an undeclared kind: error = %v, want ErrUndeclaredKind — refusing at read "+
			"time would make vocabulary drift a consumer's problem months later", err)
	}

	// A declared kind emits.
	ev, err := Emit(KindRedirect, testLeaf(), tx.TxID{}, map[string]string{"from": "a", "to": "b"})
	if err != nil {
		t.Fatalf("a declared kind was refused: %v", err)
	}
	if ev.Kind != KindRedirect || ev.Leaf != testLeaf() {
		t.Errorf("the event does not carry what it was given: %+v", ev)
	}
}

// TestDeclarationWithoutAReaderIsRefused checks the rule that keeps the
// vocabulary from becoming a list nobody reads.
func TestDeclarationWithoutAReaderIsRefused(t *testing.T) {
	err := Register(Declaration{Kind: "test.no-reader", Fields: []string{"x"}})
	if !errors.Is(err, ErrNoReader) {
		t.Fatalf("a declaration with no reader: error = %v, want ErrNoReader", err)
	}
	if _, ok := DeclarationFor("test.no-reader"); ok {
		t.Error("the refused declaration was registered anyway")
	}

	// With a reader it registers.
	if err := Register(Declaration{
		Kind: "test.with-reader", Reader: "a test", Fields: []string{"x"},
	}); err != nil {
		t.Fatalf("a declaration with a reader was refused: %v", err)
	}

	// A duplicate is refused rather than silently replacing, so two components
	// cannot disagree about what a kind means.
	err = Register(Declaration{Kind: "test.with-reader", Reader: "someone else", Fields: []string{"y"}})
	if !errors.Is(err, ErrDuplicateKind) {
		t.Errorf("a duplicate kind: error = %v, want ErrDuplicateKind", err)
	}
	if d, _ := DeclarationFor("test.with-reader"); d.Reader != "a test" {
		t.Errorf("the duplicate overwrote the original: reader is now %q", d.Reader)
	}

	// A declaration with no kind at all is refused too.
	if err := Register(Declaration{Reader: "someone"}); err == nil {
		t.Error("a declaration with no kind was accepted")
	}
}

// TestEventCarriesTypedFields checks fields are named and checked rather than
// formatted into a message.
func TestEventCarriesTypedFields(t *testing.T) {
	ev, err := Emit(KindWriteRefusedBelowFloor, testLeaf(), tx.TxID{}, map[string]string{
		"surviving":    "2",
		"floor":        "3",
		"domain_level": "rack",
	})
	if err != nil {
		t.Fatalf("Emit: %v", err)
	}
	if got := ev.Fields["surviving"]; got != "2" {
		t.Errorf("field surviving = %q, want 2", got)
	}
	if len(ev.Fields) != 3 {
		t.Errorf("the event carries %d fields, want 3", len(ev.Fields))
	}

	// A field the declaration did not name is refused, so a consumer's field
	// list cannot drift from the producer's.
	_, err = Emit(KindWriteRefusedBelowFloor, testLeaf(), tx.TxID{}, map[string]string{"note": "hello"})
	if !errors.Is(err, ErrUndeclaredField) {
		t.Errorf("an undeclared field: error = %v, want ErrUndeclaredField", err)
	}

	// ⚠ And there is no free-form message field anywhere on the event, because
	// one is how a declared vocabulary becomes a log.
	for _, d := range Declared() {
		for _, f := range d.Fields {
			switch strings.ToLower(f) {
			case "message", "msg", "text", "log", "detail":
				t.Errorf("kind %q declares a free-form field %q — every caller wants one during "+
					"an incident, and once it exists the console is a grep again", d.Kind, f)
			}
		}
	}

	// The event's own map is a copy, so a caller mutating what it passed cannot
	// change a published event.
	fields := map[string]string{"from": "a", "to": "b"}
	ev2, err := Emit(KindRedirect, testLeaf(), tx.TxID{}, fields)
	if err != nil {
		t.Fatalf("Emit: %v", err)
	}
	fields["from"] = "mutated"
	if ev2.Fields["from"] != "a" {
		t.Error("mutating the caller's map changed the emitted event")
	}
}

// TestDeclaredKindsAreStableAndOrdered checks a consumer and a test see the same
// vocabulary each run.
func TestDeclaredKindsAreStableAndOrdered(t *testing.T) {
	first := Declared()
	if len(first) < 2 {
		t.Fatalf("only %d kinds declared; ordering cannot be observed", len(first))
	}

	for i := 1; i < len(first); i++ {
		if first[i-1].Kind >= first[i].Kind {
			t.Fatalf("declarations are not ordered: %q then %q", first[i-1].Kind, first[i].Kind)
		}
	}

	// Repeatedly, because Go randomises map iteration and an unordered answer
	// would differ only sometimes.
	for i := 0; i < 20; i++ {
		again := Declared()
		if len(again) != len(first) {
			t.Fatalf("run %d returned %d declarations, first returned %d", i, len(again), len(first))
		}
		for j := range first {
			if again[j].Kind != first[j].Kind {
				t.Fatalf("run %d diverges at %d: %q vs %q", i, j, again[j].Kind, first[j].Kind)
			}
		}
	}

	// Fields are ordered too, for the same reason.
	for _, d := range first {
		if !slices.IsSorted(d.Fields) {
			t.Errorf("kind %q declares fields in an unstable order: %v", d.Kind, d.Fields)
		}
	}
}
