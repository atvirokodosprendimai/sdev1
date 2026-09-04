package tail

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// readPathFunctions are the functions a reader calls. The rule this package
// exists to hold is that none of them may block, so these are the names the
// guard checks.
//
// ⚠ If a read-path function is added and not named here, the guard silently
// stops covering it. TestReadersTakeNoLock therefore asserts it FOUND each of
// these, so a rename empties the guard loudly rather than quietly.
var readPathFunctions = map[string]bool{
	"Watermark": true,
	"Walk":      true,
	"Read":      true,
	"Snapshot":  true,
}

// blockingCalls are the operations that make a reader wait. A reader that
// performs any of them has stopped being lock-free, however correct its answers
// remain — which is precisely why no behavioural test can catch this.
var blockingCalls = map[string]bool{
	"Lock":    true,
	"Unlock":  true,
	"RLock":   true,
	"RUnlock": true,
	"Wait":    true,
	"Acquire": true,
	"TryLock": true,
}

// offender names one violation the guard found.
type offender struct {
	function string
	call     string
}

// findOffenders parses one file and reports read-path functions that block,
// along with how many read-path functions it saw at all.
//
// It takes source as a string rather than only a path so the positive control
// can feed it a known violation. A guard that can only be pointed at a clean
// tree cannot be shown to work.
func findOffenders(t *testing.T, filename, src string) (found []offender, readPathSeen []string) {
	t.Helper()
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, filename, src, 0)
	if err != nil {
		t.Fatalf("parsing %s: %v", filename, err)
	}

	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Body == nil || !readPathFunctions[fn.Name.Name] {
			continue
		}
		readPathSeen = append(readPathSeen, fn.Name.Name)
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			if blockingCalls[sel.Sel.Name] {
				found = append(found, offender{function: fn.Name.Name, call: sel.Sel.Name})
			}
			return true
		})
	}
	return found, readPathSeen
}

// TestReadersTakeNoLock is the falsifier ADR-017 names in its Enforced-by header.
//
// ★ It reads this package's own SOURCE, because the property is about the code's
// shape and no behavioural test can observe it. A reader that took a lock would
// still return correct answers — just slowly, and while blocking the writer.
// The failure this record exists to prevent is invisible to every test that only
// checks results.
func TestReadersTakeNoLock(t *testing.T) {
	sources, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatalf("globbing package sources: %v", err)
	}

	var scanned int
	var allSeen []string
	for _, path := range sources {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		src, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("reading %s: %v", path, err)
		}
		scanned++
		found, seen := findOffenders(t, path, string(src))
		allSeen = append(allSeen, seen...)
		for _, o := range found {
			t.Errorf("%s: read-path function %s calls %s — a reader that blocks is the one thing "+
				"this package does not permit, and the answers stay correct while it happens",
				path, o.function, o.call)
		}
	}

	// A guard that scanned nothing, or that recognised no read-path function, is
	// passing because it stopped looking. Both are indistinguishable from a clean
	// package unless they are asserted.
	if scanned == 0 {
		t.Fatal("the guard scanned no source files; it is passing because it looked at nothing")
	}
	sort.Strings(allSeen)
	if len(allSeen) == 0 {
		t.Fatalf("the guard recognised no read-path function in %d file(s); the names in "+
			"readPathFunctions no longer match the code, so this guard covers nothing", scanned)
	}
	for name := range readPathFunctions {
		if !contains(allSeen, name) {
			t.Errorf("the guard never saw read-path function %q; it was renamed or removed and "+
				"the guard was not updated with it", name)
		}
	}
}

func contains(xs []string, x string) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}

// TestGuardFlagsAKnownOffender is the positive control.
//
// ⚠ A negative assertion over a clean package passes whether the guard works or
// has stopped working, and those two are identical from the outside. Without
// this, TestReadersTakeNoLock is unfalsifiable: it would go green if the parser
// broke, if the call names drifted, or if the glob matched nothing.
func TestGuardFlagsAKnownOffender(t *testing.T) {
	const offending = `package tail

import "sync"

type fixture struct{ mu sync.RWMutex }

// Walk is a read-path function that blocks, which the guard must flag.
func (f *fixture) Walk(fn func() bool) {
	f.mu.RLock()
	defer f.mu.RUnlock()
	fn()
}

// Watermark is a read-path function that does not block.
func (f *fixture) Watermark() uint64 { return 0 }
`
	found, seen := findOffenders(t, "offending_fixture.go", offending)
	if len(found) == 0 {
		t.Fatal("the guard did not flag a read-path function that takes a lock; " +
			"it is not looking, and TestReadersTakeNoLock proves nothing")
	}
	if len(seen) != 2 {
		t.Errorf("the guard recognised %d read-path functions in the fixture, want 2", len(seen))
	}

	var flagged []string
	for _, o := range found {
		flagged = append(flagged, o.function+"."+o.call)
	}
	sort.Strings(flagged)
	want := []string{"Walk.RLock", "Walk.RUnlock"}
	if len(flagged) != len(want) {
		t.Fatalf("flagged %v, want %v", flagged, want)
	}
	for i := range want {
		if flagged[i] != want[i] {
			t.Errorf("flagged %v, want %v", flagged, want)
			break
		}
	}

	// The clean function in the same fixture must NOT be flagged, or the guard is
	// flagging everything and its silence on the real package means nothing.
	for _, o := range found {
		if o.function == "Watermark" {
			t.Errorf("the guard flagged %s, which does not block; a guard that flags everything "+
				"is as useless as one that flags nothing", o.function)
		}
	}
}
