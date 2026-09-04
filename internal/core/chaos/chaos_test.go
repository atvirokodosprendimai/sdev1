package chaos

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// cataloguePath is the document this package is accountable to.
const cataloguePath = "../../../docs/adr/FAILURES.md"

// checkedRow matches a row of the checked catalogue: a backticked fault name, the
// record it tests, and its disposition.
var checkedRow = regexp.MustCompile(`^\|\s*` + "`" + `([a-z0-9-]+)` + "`" + `\s*\|\s*(ADR-\d+)\s*\|\s*\**([a-z ]+?)\**\s*\|`)

type catalogueEntry struct {
	record      string
	disposition string
}

// readCatalogue parses the checked catalogue out of FAILURES.md.
//
// It reads ONLY the section under "## Checked catalogue", because the document
// deliberately also lists faults nothing can inject yet. Parsing the whole file
// would make those look like missing registrations.
func readCatalogue(t *testing.T) map[string]catalogueEntry {
	t.Helper()
	raw, err := os.ReadFile(filepath.Clean(cataloguePath))
	if err != nil {
		t.Fatalf("reading the catalogue at %s: %v — it is the deliverable of this package, "+
			"so its absence is a failure rather than a skip", cataloguePath, err)
	}

	lines := strings.Split(string(raw), "\n")
	entries := map[string]catalogueEntry{}
	inSection := false
	for _, line := range lines {
		if strings.HasPrefix(line, "## ") {
			inSection = strings.TrimSpace(line) == "## Checked catalogue"
			continue
		}
		if !inSection {
			continue
		}
		m := checkedRow.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		entries[m[1]] = catalogueEntry{record: m[2], disposition: strings.TrimSpace(m[3])}
	}

	if len(entries) == 0 {
		t.Fatalf("the checked catalogue in %s parsed to zero entries; either the section heading "+
			"changed or the table format did, and this check now covers nothing", cataloguePath)
	}
	return entries
}

// TestEveryInjectedFaultIsCatalogued is the falsifier ADR-019 names in its
// Enforced-by header.
//
// ⚠ It checks BOTH directions. Every fault needs an entry — that catches a new
// fault nobody wrote up. Every entry needs a fault — that catches a fault which
// quietly stopped being injected while its entry still reads as current, which is
// the direction that rots.
func TestEveryInjectedFaultIsCatalogued(t *testing.T) {
	entries := readCatalogue(t)
	faults := Registered()

	if len(faults) == 0 {
		t.Fatal("no faults are registered; this suite is green because it breaks nothing")
	}

	for _, f := range faults {
		entry, ok := entries[f.Name]
		if !ok {
			t.Errorf("fault %q is registered and has no entry in %s — it runs, and what it does "+
				"is written down nowhere", f.Name, cataloguePath)
			continue
		}
		if entry.record != f.Record {
			t.Errorf("fault %q tests %s but its catalogue entry names %s; the entry points at the "+
				"wrong record's promise", f.Name, f.Record, entry.record)
		}
		if entry.disposition != f.Expected.String() {
			t.Errorf("fault %q expects %q but its catalogue entry says %q",
				f.Name, f.Expected, entry.disposition)
		}
	}

	for name := range entries {
		if _, ok := Lookup(name); !ok {
			t.Errorf("%s has an entry for %q and no such fault is registered — either it was "+
				"renamed or it quietly stopped being injected, and the entry still reads as current",
				cataloguePath, name)
		}
	}
}

// TestScheduleIsReproducibleFromItsSeed checks a run can be replayed.
//
// An unreproducible failure is a report rather than a bug: confirming it costs
// somebody a day, so it gets muted, and the muted one is usually the real one.
func TestScheduleIsReproducibleFromItsSeed(t *testing.T) {
	const seed, n = 424242, 64

	first := NewSchedule(seed, n)
	if len(first.Faults) != n {
		t.Fatalf("a schedule of %d drew %d faults", n, len(first.Faults))
	}

	// Repeatedly, because the registry is a map and Go randomises map iteration
	// deliberately — drawing straight from it would reproduce nothing, and the
	// bug would appear only sometimes.
	for i := 0; i < 20; i++ {
		again := NewSchedule(seed, n)
		if len(again.Faults) != len(first.Faults) {
			t.Fatalf("replay %d drew %d faults, first drew %d", i, len(again.Faults), len(first.Faults))
		}
		for j := range first.Faults {
			if again.Faults[j] != first.Faults[j] {
				t.Fatalf("replay %d diverges at step %d: %q vs %q — the schedule is not a pure "+
					"function of its seed, so no failure found here can be reproduced",
					i, j, again.Faults[j], first.Faults[j])
			}
		}
	}

	// A different seed must actually differ, or "reproducible" is being achieved
	// by drawing the same thing always.
	other := NewSchedule(seed+1, n)
	same := true
	for j := range first.Faults {
		if other.Faults[j] != first.Faults[j] {
			same = false
			break
		}
	}
	if same && len(Registered()) > 1 {
		t.Error("two different seeds drew identical schedules; the seed is not being used")
	}

	// The per-step generator is seeded from the schedule too, so an injection's
	// own randomness replays as well.
	if a, b := first.Rand(3).Int63(), NewSchedule(seed, n).Rand(3).Int63(); a != b {
		t.Errorf("the step generator is not reproducible: %d then %d", a, b)
	}
}

// runFault injects one fault and returns its outcome, failing the test if the
// injection itself could not be performed.
func runFault(t *testing.T, name string, step int) Outcome {
	t.Helper()
	f, ok := Lookup(name)
	if !ok {
		t.Fatalf("fault %q is not registered", name)
	}
	s := NewSchedule(20260904, 8)
	out, err := f.Inject(s.Rand(step))
	if err != nil {
		t.Fatalf("injecting %q (seed %d, step %d): %v — this is an injection failure, "+
			"not a finding about the system", name, s.Seed, step, err)
	}
	if out.Disposition == DispositionUnset {
		t.Fatalf("fault %q reported no disposition", name)
	}
	if out.Detail == "" {
		t.Errorf("fault %q reported no detail; the catalogue needs to say what was observed", name)
	}
	return out
}

func assertDisposition(t *testing.T, name string, step int, want Disposition) {
	t.Helper()
	out := runFault(t, name, step)
	if out.Disposition != want {
		t.Errorf("fault %q: %s\n  observed:  %s\n  expected:  %s\n  replay:    seed 20260904, step %d",
			name, out.Detail, out.Disposition, want, step)
		return
	}
	t.Logf("%s (%s): %s", name, out.Disposition, out.Detail)
}

// TestFragmentLossWithinToleranceRecovers is ADR-006's central promise under an
// actual fault rather than in a unit test's fixture.
func TestFragmentLossWithinToleranceRecovers(t *testing.T) {
	for step := 0; step < 8; step++ {
		assertDisposition(t, "fragment-loss-within-tolerance", step, Recovers)
	}
}

// TestFragmentLossBeyondToleranceIsUnrecoverableByDesign checks the refusal is
// the behaviour, and that it is catalogued as intended rather than as a bug.
func TestFragmentLossBeyondToleranceIsUnrecoverableByDesign(t *testing.T) {
	for step := 0; step < 8; step++ {
		assertDisposition(t, "fragment-loss-beyond-tolerance", step, UnrecoverableByDesign)
	}
}

// TestCorruptFragmentRecovers is the difference between an erasure and an error,
// under a real injection: the fragment is present and lying, and only its
// checksum can say so.
func TestCorruptFragmentRecovers(t *testing.T) {
	for step := 0; step < 8; step++ {
		assertDisposition(t, "fragment-corruption", step, Recovers)
	}
	for step := 0; step < 4; step++ {
		assertDisposition(t, "block-checksum-mismatch", step, Recovers)
	}
	for step := 0; step < 2; step++ {
		assertDisposition(t, "durability-floor-breached", step, Recovers)
	}
}

// TestWriterStoppedMidAppendLosesNothingPublished is ADR-017's claim under a
// fault, and its companion open finding.
func TestWriterStoppedMidAppendLosesNothingPublished(t *testing.T) {
	for step := 0; step < 6; step++ {
		assertDisposition(t, "writer-stopped-mid-append", step, Recovers)
	}

	// ⚠ This one is EXPECTED to be unrecoverable. Asserting the expectation is
	// what stops the finding being quietly fixed-by-accident and forgotten, and
	// what makes the test fail loudly when ADR-009 finally closes it — at which
	// point the catalogue entry has to be re-dispositioned rather than left
	// claiming something that stopped being true.
	assertDisposition(t, "writer-process-lost", 0, UnrecoverableAndOpen)
}

// TestCatalogueDistinguishesOpenFromByDesign checks the open entries stay
// countable.
func TestCatalogueDistinguishesOpenFromByDesign(t *testing.T) {
	if got := len(Dispositions()); got != 3 {
		t.Fatalf("there are %d dispositions, want exactly 3 — a fourth is how "+
			"'we are looking into it' enters a catalogue", got)
	}
	seen := map[string]bool{}
	for _, d := range Dispositions() {
		if d == DispositionUnset {
			t.Error("the unset zero value is offered as a valid disposition")
		}
		if seen[d.String()] {
			t.Errorf("two dispositions render as %q, so the catalogue cannot distinguish them", d)
		}
		seen[d.String()] = true
	}

	entries := readCatalogue(t)
	counts := map[string]int{}
	for name, e := range entries {
		if !seen[e.disposition] {
			t.Errorf("catalogue entry %q has disposition %q, which is not one of the three",
				name, e.disposition)
		}
		counts[e.disposition]++
	}

	// Each of the three must actually occur, or the distinction is untested: a
	// catalogue where everything recovers proves nothing about the vocabulary.
	for _, d := range Dispositions() {
		if counts[d.String()] == 0 {
			t.Errorf("no catalogue entry is %q; the distinction between intended and open "+
				"losses is not exercised by anything", d)
		}
	}

	// A fault registered as open must be open in the catalogue too, so the count
	// an operator reads is the count the code produces.
	for _, f := range Registered() {
		if f.Expected != UnrecoverableAndOpen {
			continue
		}
		if e, ok := entries[f.Name]; ok && e.disposition != UnrecoverableAndOpen.String() {
			t.Errorf("fault %q is registered as open but the catalogue calls it %q", f.Name, e.disposition)
		}
	}
}
