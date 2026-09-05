package placement

import (
	"errors"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/atvirokodosprendimai/sdev1/internal/core/addr"
	"github.com/atvirokodosprendimai/sdev1/internal/core/topology"
)

func loadFixture(t *testing.T) topology.Map {
	t.Helper()
	f, err := os.Open(filepath.Join("..", "..", "..", "testdata", "topology", "minimal.json"))
	if err != nil {
		t.Fatalf("open fixture: %v", err)
	}
	defer f.Close()
	m, err := topology.Load(f)
	if err != nil {
		t.Fatalf("Load fixture: %v", err)
	}
	return m
}

func leafFor(t *testing.T, entity string, depth uint8) addr.LeafID {
	t.Helper()
	l, err := addr.Descend(addr.KeyOf(addr.TenantFromUint(1), entity), depth)
	if err != nil {
		t.Fatalf("Descend(%q, %d): %v", entity, depth, err)
	}
	return l
}

// TestResolveOrderIsPinnedAcrossProcesses is the regression guard for the one
// failure the rest of this file structurally cannot see.
//
// ⚠ Every other determinism check here calls Resolve repeatedly IN ONE PROCESS,
// and a Go test binary is one process. A per-process random seed is therefore
// constant for the whole suite, so scoring that differs on every machine in the
// cluster passes all of them. That is not hypothetical: placement was seeded
// with `maphash.MakeSeed()` until 2026-09-04, and it was caught by running
// cmd/sdev1-addr twice by hand while writing documentation — not by the tests.
//
// ★ A cross-process invariant can only be held by a check on VALUES. These are
// golden: if the ordering function changes at all, this fails and the change has
// to be a deliberate one, because it re-places every leaf in an existing cluster.
func TestResolveOrderIsPinnedAcrossProcesses(t *testing.T) {
	m := loadFixture(t)

	// Recorded 2026-09-04 against testdata/topology/minimal.json with FNV-1a
	// scoring. Changing them silently is the failure; changing them deliberately
	// means every stored leaf moves.
	//
	// ⚠ The tenants differ so the leaves differ. At this fixture's depth of 1 the
	// leaf is the key's FIRST byte, which is the tenant's high byte — so two
	// entities under one tenant share a leaf and would pin the same order twice,
	// discriminating nothing. That was the first version of this table.
	golden := []struct {
		tenant uint16
		leaf   string
		want   []string
	}{
		{tenant: 1, leaf: "1:00", want: []string{"srv-3-d0", "srv-1-d0", "srv-1-d1", "srv-2-d0"}},
		{tenant: 256, leaf: "1:01", want: []string{"srv-2-d0", "srv-1-d0", "srv-1-d1", "srv-3-d0"}},
	}

	for _, tc := range golden {
		leaf, err := addr.Descend(addr.KeyOf(addr.TenantFromUint(tc.tenant), "pinned"), m.Depth)
		if err != nil {
			t.Fatalf("Descend(tenant %d): %v", tc.tenant, err)
		}
		if got := leaf.String(); got != tc.leaf {
			t.Fatalf("tenant %d resolved to leaf %s, want %s — the fixture or the address layout moved",
				tc.tenant, got, tc.leaf)
		}
		got, err := Resolve(leaf, m)
		if err != nil {
			t.Fatalf("Resolve(%s): %v", tc.leaf, err)
		}
		if !reflect.DeepEqual(got, tc.want) {
			t.Fatalf("Resolve(%s) = %v, want %v\n"+
				"If this changed because the scoring function was reseeded or replaced: a seeded hash "+
				"makes every process disagree, so one client writes a leaf where another will never look. "+
				"If it changed deliberately, every leaf in every existing cluster has just been re-placed.",
				tc.leaf, got, tc.want)
		}
	}
}

// TestScoringSpreadsAcrossTargets is the other half of what scoring owes, and
// the half that had no check at all until 2026-09-04.
//
// ⚠ Determinism and distribution are separate requirements, and it is easy to
// satisfy the first while badly failing the second — every determinism assertion
// still passes, so nothing says a word. FNV-1a was briefly used here and its
// avalanche is weak enough that the ranking tracked the target's NAME: over 256
// leaves one target won 107 times and another 37, against a fair share of 64.
// That is a 2.9× spread, and it means placement systematically favours whichever
// servers happen to sort a certain way.
//
// ★ Measured 2026-09-04 against testdata/topology/minimal.json: SHA-256 gives
// 61 / 60 / 65 / 70. The band below passes that comfortably and rejects FNV-1a
// at both ends. The check is fully deterministic — there is no sampling and no
// randomness — so it cannot flake.
func TestScoringSpreadsAcrossTargets(t *testing.T) {
	m := loadFixture(t)

	// At this fixture's depth of 1 the leaf is the key's first byte, which is the
	// tenant's high byte — so stepping the high byte walks every distinct leaf
	// the fixture can address, and there are exactly 256 of them.
	const leaves = 256
	wins := map[string]int{}
	var targets []string
	for i := 0; i < leaves; i++ {
		leaf, err := addr.Descend(addr.KeyOf(addr.TenantFromUint(uint16(i)<<8), "spread"), m.Depth)
		if err != nil {
			t.Fatalf("Descend(%d): %v", i, err)
		}
		got, err := Resolve(leaf, m)
		if err != nil {
			t.Fatalf("Resolve(%s): %v", leaf, err)
		}
		if targets == nil {
			targets = got
		}
		wins[got[0]]++
	}

	fair := float64(leaves) / float64(len(targets))
	low, high := fair*0.6, fair*1.6
	for _, name := range targets {
		got := float64(wins[name])
		if got < low || got > high {
			t.Errorf("%s wins %d of %d leaves; fair share is %.0f and the accepted band is %.0f–%.0f.\n"+
				"Scoring is deterministic but badly distributed, so placement favours some targets and "+
				"starves others. Every determinism test still passes — that is why this check exists.",
				name, wins[name], leaves, fair, low, high)
		}
	}
	if t.Failed() {
		t.Logf("wins: %v", wins)
	}
}

// TestScoringUsesNoPerProcessSeed asserts the property directly, so it fails at
// the source rather than through a golden order that could also move for an
// innocent reason.
//
// ⚠ It inspects this package's IMPORTS rather than its text. The first version
// grepped the source for "maphash.MakeSeed" and immediately flagged the comment
// explaining why that call must not be used — a guard that fires on prose gets
// switched off, and then it protects nothing. An import list is the narrowest
// thing that still catches the defect.
func TestScoringUsesNoPerProcessSeed(t *testing.T) {
	fset := token.NewFileSet()
	pkgFiles, err := parser.ParseDir(fset, ".", func(fi os.FileInfo) bool {
		return !strings.HasSuffix(fi.Name(), "_test.go")
	}, parser.ImportsOnly)
	if err != nil {
		t.Fatalf("parse package: %v", err)
	}

	// Every one of these can only produce a value that differs between processes
	// or between runs, which is the single thing placement scoring may not do.
	banned := map[string]string{
		"hash/maphash": "maphash.MakeSeed returns a NEW RANDOM seed per process, so every binary would score differently",
		"math/rand":    "a random ordering is not an ordering two clients can both compute",
		"math/rand/v2": "a random ordering is not an ordering two clients can both compute",
		"crypto/rand":  "a random ordering is not an ordering two clients can both compute",
		"time":         "a placement that depends on when it was computed cannot be recomputed later",
	}

	for _, pkg := range pkgFiles {
		for name, file := range pkg.Files {
			for _, spec := range file.Imports {
				path := strings.Trim(spec.Path.Value, `"`)
				if why, isBanned := banned[path]; isBanned {
					t.Errorf("%s imports %q: %s.\nPlacement must be a pure function of the leaf and the "+
						"target name. A per-process value here makes two clients place the same leaf on "+
						"different servers, and NO in-process test can detect it.", name, path, why)
				}
			}
		}
	}
}

// TestResolveIsDeterministic checks the same leaf and map yield the same order
// every time, so two clients agree without coordinating.
//
// ⚠ Within ONE process only — see TestResolveOrderIsPinnedAcrossProcesses for
// the half this cannot reach.
func TestResolveIsDeterministic(t *testing.T) {
	m := loadFixture(t)
	leaf := leafFor(t, "determinism", m.Depth)
	first, err := Resolve(leaf, m)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	for i := 0; i < 64; i++ {
		got, err := Resolve(leaf, m)
		if err != nil {
			t.Fatalf("Resolve iteration %d: %v", i, err)
		}
		if !reflect.DeepEqual(got, first) {
			t.Fatalf("iteration %d: Resolve = %v, want %v", i, got, first)
		}
	}
}

// TestResolveTakesNoCallerIdentity is the structural guard on the invariant
// locality is most likely to break: Resolve's answer must not be able to vary by
// who asks. A function that cannot see the caller cannot depend on them, so this
// asserts the signature rather than a behaviour.
//
// It fails the moment someone adds a "from" or "client" parameter to make reads
// prefer near replicas — which is what Nearest is for.
func TestResolveTakesNoCallerIdentity(t *testing.T) {
	ft := reflect.TypeOf(Resolve)
	if ft.NumIn() != 2 {
		t.Fatalf("Resolve takes %d parameters, want exactly 2 (leaf, map): "+
			"a third parameter is almost certainly caller identity, which must go to Nearest", ft.NumIn())
	}
	if got, want := ft.In(0), reflect.TypeOf(addr.LeafID{}); got != want {
		t.Errorf("Resolve parameter 0 is %v, want %v", got, want)
	}
	if got, want := ft.In(1), reflect.TypeOf(topology.Map{}); got != want {
		t.Errorf("Resolve parameter 1 is %v, want %v", got, want)
	}
	if ft.IsVariadic() {
		t.Error("Resolve is variadic; its inputs must be exactly the leaf and the map")
	}
}

// TestResolveIsStableUnderUnrelatedTopologyChange checks rendezvous hashing's
// defining property: adding a target elsewhere inserts it at its own position
// and leaves the RELATIVE order of the existing targets untouched. Without this
// a topology change reshuffles the whole cluster.
func TestResolveIsStableUnderUnrelatedTopologyChange(t *testing.T) {
	m := loadFixture(t)
	leaf := leafFor(t, "stability", m.Depth)
	before, err := Resolve(leaf, m)
	if err != nil {
		t.Fatalf("Resolve before: %v", err)
	}

	grown := growFixture(t)
	leafGrown := leafFor(t, "stability", grown.Depth)
	after, err := Resolve(leafGrown, grown)
	if err != nil {
		t.Fatalf("Resolve after: %v", err)
	}

	// Every target present before must still appear, in the same relative order.
	var filtered []string
	present := make(map[string]bool, len(before))
	for _, n := range before {
		present[n] = true
	}
	for _, n := range after {
		if present[n] {
			filtered = append(filtered, n)
		}
	}
	if !reflect.DeepEqual(filtered, before) {
		t.Errorf("adding an unrelated target reordered the existing ones:\n before %v\n after  %v",
			before, filtered)
	}
}

// growFixture returns the fixture plus one extra disk in a third rack, so that
// "unrelated change" means genuinely unrelated.
func growFixture(t *testing.T) topology.Map {
	t.Helper()
	const src = `{
	  "version":1,
	  "generation":"000000003b9aca000000000000010000000000000000000000000000000000000000000000000000000000000200000002",
	  "depth":1,
	  "levels":["universe","planet","datacenter","rack","server","disk"],
	  "root":{"level":"universe","name":"u","children":[
	    {"level":"planet","name":"earth","children":[
	      {"level":"datacenter","name":"dc-1","children":[
	        {"level":"rack","name":"rack-a","children":[
	          {"level":"server","name":"srv-1","weight":100,"children":[
	            {"level":"disk","name":"srv-1-d0","weight":100},
	            {"level":"disk","name":"srv-1-d1","weight":100}]},
	          {"level":"server","name":"srv-2","weight":100,"children":[
	            {"level":"disk","name":"srv-2-d0","weight":100}]}]},
	        {"level":"rack","name":"rack-b","children":[
	          {"level":"server","name":"srv-3","weight":100,"children":[
	            {"level":"disk","name":"srv-3-d0","weight":100}]}]},
	        {"level":"rack","name":"rack-c","children":[
	          {"level":"server","name":"srv-4","weight":100,"children":[
	            {"level":"disk","name":"srv-4-d0","weight":100}]}]}]}]}]}
	}`
	m, err := topology.Load(strings.NewReader(src))
	if err != nil {
		t.Fatalf("Load grown fixture: %v", err)
	}
	return m
}

// TestResolveRefusesDepthMismatch checks a leaf recorded at one depth is refused
// against a map declaring another, rather than silently re-placed.
func TestResolveRefusesDepthMismatch(t *testing.T) {
	m := loadFixture(t)
	wrong := leafFor(t, "mismatch", m.Depth+1)
	if _, err := Resolve(wrong, m); !errors.Is(err, ErrDepthMismatch) {
		t.Fatalf("Resolve(depth %d against map depth %d) error = %v, want ErrDepthMismatch",
			wrong.Depth, m.Depth, err)
	}
}

// TestSpreadPrefersDistinctDomains checks that consuming a prefix of a spread
// order yields distinct failure domains wherever the map offers them — the
// property a durability rule depends on.
func TestSpreadPrefersDistinctDomains(t *testing.T) {
	m := growFixture(t)
	leaf := leafFor(t, "spread", m.Depth)
	order, err := Resolve(leaf, m)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	rack := m.LevelIndex("rack")
	spread := Spread(order, m, rack)

	// The fixture offers three racks, so the first three entries must be in
	// three distinct racks.
	seen := make(map[string]bool)
	for i := 0; i < 3; i++ {
		anc, err := m.AncestorAtLevel(spread[i], rack)
		if err != nil {
			t.Fatalf("AncestorAtLevel(%q, rack): %v", spread[i], err)
		}
		if seen[anc.Name] {
			t.Fatalf("first three of %v are not in distinct racks: %q repeats at position %d",
				spread, anc.Name, i)
		}
		seen[anc.Name] = true
	}
}

// TestSpreadIsAPermutation checks Spread reorders and never changes membership.
// Read preference and domain diversity may both change the ORDER a policy
// consumes targets in; neither may change which targets exist.
func TestSpreadIsAPermutation(t *testing.T) {
	m := growFixture(t)
	leaf := leafFor(t, "permutation", m.Depth)
	order, err := Resolve(leaf, m)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	got := Spread(order, m, m.LevelIndex("rack"))
	assertPermutation(t, "Spread", order, got)
}

// TestNearestPrefersSameRackThenSameDatacenter checks a caller's own order runs
// same-rack first, then same-datacenter, then the rest.
func TestNearestPrefersSameRackThenSameDatacenter(t *testing.T) {
	m := growFixture(t)
	leaf := leafFor(t, "nearest", m.Depth)
	order, err := Resolve(leaf, m)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	got := Nearest(order, "srv-1", m)

	last := -1
	for _, name := range got {
		d, err := m.Distance("srv-1", name)
		if err != nil {
			t.Fatalf("Distance(srv-1, %q): %v", name, err)
		}
		if d < last {
			t.Fatalf("Nearest(%v) is not ordered by distance: %q at %d follows %d",
				got, name, d, last)
		}
		last = d
	}
	// srv-1's own disks are in its own rack, so one of them must come first.
	if !strings.HasPrefix(got[0], "srv-1-") && !strings.HasPrefix(got[0], "srv-2-") {
		t.Errorf("Nearest from srv-1 starts with %q, want a disk in rack-a", got[0])
	}
}

// TestNearestIsAPermutationOfResolve checks read preference cannot silently
// change replica membership.
func TestNearestIsAPermutationOfResolve(t *testing.T) {
	m := growFixture(t)
	leaf := leafFor(t, "nearest-permutation", m.Depth)
	order, err := Resolve(leaf, m)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	for _, from := range []string{"srv-1", "srv-3", "srv-4"} {
		assertPermutation(t, "Nearest from "+from, order, Nearest(order, from, m))
	}
}

// TestNearestKeepsUnknownTargets checks an unresolvable target sorts last rather
// than disappearing — dropping it would make read preference change membership.
func TestNearestKeepsUnknownTargets(t *testing.T) {
	m := loadFixture(t)
	set := []string{"srv-1-d0", "not-in-this-map", "srv-3-d0"}
	got := Nearest(set, "srv-1", m)
	assertPermutation(t, "Nearest with an unknown target", set, got)
	if got[len(got)-1] != "not-in-this-map" {
		t.Errorf("Nearest = %v, want the unknown target last", got)
	}
}

func assertPermutation(t *testing.T, what string, want, got []string) {
	t.Helper()
	if len(want) != len(got) {
		t.Fatalf("%s changed the set size: %d -> %d (%v -> %v)", what, len(want), len(got), want, got)
	}
	counts := make(map[string]int, len(want))
	for _, n := range want {
		counts[n]++
	}
	for _, n := range got {
		counts[n]--
	}
	for n, c := range counts {
		if c != 0 {
			t.Errorf("%s is not a permutation: %q has count delta %d (%v -> %v)", what, n, c, want, got)
		}
	}
}
