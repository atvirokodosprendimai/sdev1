package subscribe

import (
	"bytes"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/atvirokodosprendimai/sdev1/internal/core/crypt"
	"github.com/atvirokodosprendimai/sdev1/internal/core/tail"
)

// forgetfulSink acknowledges a purge. `refuse` makes it fail to, which is what a
// sink that is registered but unreachable looks like from here.
type forgetfulSink struct {
	name    string
	refuse  bool
	forgot  []string
	touched int
}

func (s *forgetfulSink) Name() string               { return s.name }
func (s *forgetfulSink) Consume(e []tail.Entry) int { return len(e) }
func (s *forgetfulSink) Forget(subject string) error {
	s.touched++
	if s.refuse {
		return errors.New("sink is unreachable")
	}
	s.forgot = append(s.forgot, subject)
	return nil
}

// silentSink is registered and cannot forget anything — a console, say. It can
// never acknowledge, so it can never be counted as having done so.
type silentSink struct{ name string }

func (s *silentSink) Name() string               { return s.name }
func (s *silentSink) Consume(e []tail.Entry) int { return len(e) }

func sealedFor(t *testing.T, ks *crypt.MemoryKeystore, subject string, plaintext []byte) []byte {
	t.Helper()
	id, err := ks.Allocate(subject)
	if err != nil {
		t.Fatalf("Allocate: %v", err)
	}
	k, err := ks.Fetch(id)
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	sealed, err := crypt.Seal(id, k, plaintext)
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}
	return sealed
}

// TestPurgeIsIncompleteWhileASinkHasNotAcknowledged is the falsifier ADR-010
// names in its Enforced-by header.
//
// ⚠ The dangerous sink is registered and NEVER answers, which is what a
// silently-unwired backup looks like from here. A test whose sinks all
// acknowledge proves nothing about the case that actually bites.
func TestPurgeIsIncompleteWhileASinkHasNotAcknowledged(t *testing.T) {
	reg := NewRegistry()
	good := &forgetfulSink{name: "read-model"}
	bad := &forgetfulSink{name: "backup", refuse: true}
	for _, s := range []Sink{good, bad} {
		if _, err := reg.Register(s); err != nil {
			t.Fatalf("Register %s: %v", s.Name(), err)
		}
	}

	res := Mark(reg, "alice@example.com")
	if res.State != PurgeIncomplete {
		t.Fatalf("a purge with an unacknowledged sink reported %s, want incomplete — 'done' is a "+
			"lie that surfaces at the next restore, and 'refused' would suggest nothing happened",
			res.State)
	}
	if !slices.Equal(res.Outstanding, []string{"backup"}) {
		t.Errorf("outstanding = %v, want the one sink that did not acknowledge", res.Outstanding)
	}
	if !slices.Equal(res.Acknowledged, []string{"read-model"}) {
		t.Errorf("acknowledged = %v, want the sink that did", res.Acknowledged)
	}

	// A registered sink that CANNOT forget is outstanding too. It has no way to
	// confirm, so confirming on its behalf would be inventing an acknowledgement.
	quiet := NewRegistry()
	if _, err := quiet.Register(&silentSink{name: "console"}); err != nil {
		t.Fatalf("Register console: %v", err)
	}
	if res := Mark(quiet, "bob@example.com"); res.State != PurgeIncomplete {
		t.Errorf("a sink that cannot forget reported %s, want incomplete", res.State)
	}

	// Once every sink acknowledges, and only then, the purge is done.
	bad.refuse = false
	if res := Mark(reg, "alice@example.com"); res.State != PurgeDone {
		t.Errorf("with every sink acknowledging the purge reported %s, want done", res.State)
	}
}

// TestPurgeNamesWhoHasAcknowledgedAndWhoHasNot checks the result is actionable
// rather than a verdict about everything at once.
func TestPurgeNamesWhoHasAcknowledgedAndWhoHasNot(t *testing.T) {
	reg := NewRegistry()
	sinks := []*forgetfulSink{
		{name: "a"}, {name: "b", refuse: true}, {name: "c"}, {name: "d", refuse: true},
	}
	for _, s := range sinks {
		if _, err := reg.Register(s); err != nil {
			t.Fatalf("Register %s: %v", s.name, err)
		}
	}

	res := Mark(reg, "carol@example.com")
	if !slices.Equal(res.Acknowledged, []string{"a", "c"}) {
		t.Errorf("acknowledged = %v, want [a c]", res.Acknowledged)
	}
	if !slices.Equal(res.Outstanding, []string{"b", "d"}) {
		t.Errorf("outstanding = %v, want [b d]", res.Outstanding)
	}
	if got := len(res.Acknowledged) + len(res.Outstanding); got != len(sinks) {
		t.Errorf("the result accounts for %d sinks, want all %d — a sink in neither list is one "+
			"nobody knows about", got, len(sinks))
	}

	// Every registered sink was actually asked, rather than assumed.
	for _, s := range sinks {
		if s.touched == 0 {
			t.Errorf("sink %q was never asked to forget", s.name)
		}
	}
}

// TestThreeVerbsGiveThreeGuarantees checks the three are observably different.
func TestThreeVerbsGiveThreeGuarantees(t *testing.T) {
	if got := len(PurgeStates()); got != 3 {
		t.Fatalf("there are %d purge states, want exactly 3 — a fourth is where 'we are working "+
			"on it' hides", got)
	}

	ks := crypt.NewMemoryKeystore()
	reg := NewRegistry()
	if _, err := reg.Register(&forgetfulSink{name: "read-model"}); err != nil {
		t.Fatalf("Register: %v", err)
	}

	marked := Mark(reg, "dave@example.com")
	if marked.Verb != "mark" || marked.Erases() {
		t.Errorf("mark: verb %q, erases %v; want mark, false", marked.Verb, marked.Erases())
	}

	swept, err := Sweep(reg, "dave@example.com", Horizon{Nanos: 86400e9})
	if err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	if swept.Verb != "sweep" || swept.Erases() {
		t.Errorf("sweep: verb %q, erases %v; want sweep, false", swept.Verb, swept.Erases())
	}
	// A sweep with no horizon is refused, because a horizon is what bounds it.
	if res, err := Sweep(reg, "dave@example.com", Horizon{}); err == nil || res.State != PurgeRefused {
		t.Errorf("a sweep with no horizon: state %s, error %v; want refused", res.State, err)
	}

	sealedFor(t, ks, "dave@example.com", []byte("some data"))
	shredded, err := Shred(reg, ks, "dave@example.com", "req-1")
	if err != nil {
		t.Fatalf("Shred: %v", err)
	}
	if shredded.Verb != "shred" || !shredded.Erases() {
		t.Errorf("shred: verb %q, erases %v; want shred, true", shredded.Verb, shredded.Erases())
	}

	// Shredding a subject with no key is refused rather than reported done.
	if res, err := Shred(reg, ks, "nobody@example.com", "req-2"); err == nil || res.State != PurgeRefused {
		t.Errorf("shredding an unknown subject: state %s, error %v; want refused", res.State, err)
	}
}

// TestOnlyShreddingMakesASubjectUnreadable is the distinction the whole record
// turns on.
//
// ⚠ Each case reads the data SUCCESSFULLY first, so every assertion is about the
// verb rather than about a fixture that never worked.
func TestOnlyShreddingMakesASubjectUnreadable(t *testing.T) {
	plaintext := []byte("readable until the key is gone")

	for _, c := range []struct {
		verb      string
		apply     func(*testing.T, *Registry, *crypt.MemoryKeystore) PurgeResult
		stillOpen bool
	}{
		{"mark", func(t *testing.T, r *Registry, _ *crypt.MemoryKeystore) PurgeResult {
			return Mark(r, "erin@example.com")
		}, true},
		{"sweep", func(t *testing.T, r *Registry, _ *crypt.MemoryKeystore) PurgeResult {
			res, err := Sweep(r, "erin@example.com", Horizon{Nanos: 1})
			if err != nil {
				t.Fatalf("Sweep: %v", err)
			}
			return res
		}, true},
		{"shred", func(t *testing.T, r *Registry, ks *crypt.MemoryKeystore) PurgeResult {
			res, err := Shred(r, ks, "erin@example.com", "req")
			if err != nil {
				t.Fatalf("Shred: %v", err)
			}
			return res
		}, false},
	} {
		ks := crypt.NewMemoryKeystore()
		reg := NewRegistry()
		if _, err := reg.Register(&forgetfulSink{name: "read-model"}); err != nil {
			t.Fatalf("Register: %v", err)
		}
		sealed := sealedFor(t, ks, "erin@example.com", plaintext)

		// Readable BEFORE, so the assertion afterwards is about the verb.
		got, err := crypt.Open(ks, sealed)
		if err != nil || !bytes.Equal(got, plaintext) {
			t.Fatalf("%s: the fixture was not readable to begin with: %v", c.verb, err)
		}

		res := c.apply(t, reg, ks)

		_, err = crypt.Open(ks, sealed)
		readable := err == nil
		if readable != c.stillOpen {
			t.Errorf("%s: readable afterwards = %v, want %v", c.verb, readable, c.stillOpen)
		}
		if !c.stillOpen && !errors.Is(err, crypt.ErrKeyDestroyed) {
			t.Errorf("%s: error = %v, want ErrKeyDestroyed", c.verb, err)
		}

		// And a caller that DEMANDS erasure is told no by anything but a shred.
		assertErr := res.AssertErased()
		if c.stillOpen && !errors.Is(assertErr, ErrNotErasure) {
			t.Errorf("%s: AssertErased = %v, want ErrNotErasure — reporting a mark or a sweep "+
				"as erasure is the failure this record exists to prevent", c.verb, assertErr)
		}
		if !c.stillOpen && assertErr != nil {
			t.Errorf("shred with every sink acknowledged: AssertErased = %v, want nil", assertErr)
		}
	}

	// A shred with an outstanding sink is NOT erased, because a sink holding
	// plaintext is not reached by destroying a key.
	ks := crypt.NewMemoryKeystore()
	reg := NewRegistry()
	if _, err := reg.Register(&forgetfulSink{name: "backup", refuse: true}); err != nil {
		t.Fatalf("Register: %v", err)
	}
	sealedFor(t, ks, "frank@example.com", plaintext)
	res, err := Shred(reg, ks, "frank@example.com", "req")
	if err != nil {
		t.Fatalf("Shred: %v", err)
	}
	if res.State != PurgeIncomplete {
		t.Fatalf("a shred with an unacknowledged sink reported %s, want incomplete", res.State)
	}
	if !errors.Is(res.AssertErased(), ErrNotErasure) {
		t.Error("a shred with an outstanding sink claimed erasure; a sink holding plaintext is " +
			"not reached by destroying a key")
	}
}

// TestThereIsNoDeleteVerb scans this package's own source.
//
// ⚠ It enumerates the exported functions rather than checking a known list,
// because the failure mode is somebody ADDING a convenience wrapper later — and
// a hand-written list of forbidden names passes when a new one appears.
func TestThereIsNoDeleteVerb(t *testing.T) {
	forbidden := []string{"Delete", "Remove", "Purge", "Erase", "Drop"}

	sources, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatalf("globbing: %v", err)
	}
	fset := token.NewFileSet()
	var exported []string
	// removalVerbs are the exported functions that ACT on the registry — the
	// shape a removal verb necessarily has, since it must reach the sinks.
	var removalVerbs []string
	scanned := 0
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
		scanned++
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Recv != nil || !fn.Name.IsExported() {
				continue
			}
			exported = append(exported, fn.Name.Name)
			if takesRegistry(fn) {
				removalVerbs = append(removalVerbs, fn.Name.Name)
			}
		}
	}
	if scanned == 0 || len(exported) == 0 {
		t.Fatalf("scanned %d files and found %d exported functions; this check is looking at "+
			"nothing", scanned, len(exported))
	}
	if len(removalVerbs) == 0 {
		t.Fatal("no exported function takes a *Registry; this check would pass for a package " +
			"that had lost all three verbs")
	}

	// ⚠ Matching on NAME alone is too blunt — `PurgeStates` and `PurgeResult` are
	// nouns about the mechanism, not verbs that perform it. What distinguishes a
	// removal verb is that it acts on the registry, because it must reach the
	// sinks. So the check is scoped to those, which is both precise and still
	// catches the change that matters: somebody adding `Delete(reg, subject)`.
	for _, name := range removalVerbs {
		for _, bad := range forbidden {
			if name == bad || strings.HasPrefix(name, bad) {
				t.Errorf("this package exports %q, which acts on the registry — one verb meaning "+
					"'remove this' would be answered by a different mechanism depending on "+
					"context, and an operator would not know whether they got invisibility, "+
					"erasure, or a promise about space", name)
			}
		}
	}

	// The three that must exist, do — and each of them is a registry verb, so the
	// scoping above is looking at exactly the right set.
	for _, want := range []string{"Mark", "Shred", "Sweep"} {
		if !slices.Contains(exported, want) {
			t.Errorf("this package does not export %q; the three verbs are the whole point, and "+
				"a check that found none of them would pass vacuously", want)
		}
		if !slices.Contains(removalVerbs, want) {
			t.Errorf("%q does not act on the registry, so a purge through it would reach no "+
				"sinks", want)
		}
	}
}

// takesRegistry reports whether a function's parameters include a *Registry,
// which is the shape any verb that must reach the sinks necessarily has.
func takesRegistry(fn *ast.FuncDecl) bool {
	if fn.Type.Params == nil {
		return false
	}
	for _, field := range fn.Type.Params.List {
		star, ok := field.Type.(*ast.StarExpr)
		if !ok {
			continue
		}
		if ident, ok := star.X.(*ast.Ident); ok && ident.Name == "Registry" {
			return true
		}
	}
	return false
}
