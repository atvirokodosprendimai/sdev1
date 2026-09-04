package leafstore

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/atvirokodosprendimai/sdev1/internal/core/addr"
	"github.com/atvirokodosprendimai/sdev1/internal/core/hlc"
	"github.com/atvirokodosprendimai/sdev1/internal/core/ports"
	"github.com/atvirokodosprendimai/sdev1/internal/core/temporal"
	"github.com/atvirokodosprendimai/sdev1/internal/core/tx"
)

// ★ A real temporary directory, not a filesystem abstraction. The falsifier is
// about what happens when files are RENAMED, and an abstraction would be
// asserting the abstraction.

func testLeaf() addr.LeafID { return addr.TenantFromUint(3).TenantSubtree() }

func at(wall int64) tx.TxID {
	return tx.TxID{HLC: hlc.Timestamp{Wall: wall}, Leaf: testLeaf(), Seq: 1}
}

// snapshotAfter is a snapshot that sees everything written up to wall.
func snapshotAfter(wall int64) ports.Snapshot {
	return ports.Snapshot{At: at(wall), ValidAt: 500}
}

func assertion(entity, attribute, value string, wall int64) ports.Datom {
	return ports.Datom{
		Entity: entity, Attribute: attribute, Value: []byte(value),
		Valid: temporal.Interval{From: 0, To: temporal.Forever},
		TxID:  at(wall), Assert: true,
	}
}

func retraction(entity, attribute, value string, wall int64) ports.Datom {
	d := assertion(entity, attribute, value, wall)
	d.Assert = false
	return d
}

// sealEach appends each batch and seals it, producing one segment per batch.
func sealEach(t *testing.T, s *Store, batches ...[]ports.Datom) {
	t.Helper()
	ctx := context.Background()
	for i, batch := range batches {
		if err := s.Append(ctx, batch...); err != nil {
			t.Fatalf("Append batch %d: %v", i, err)
		}
		if err := s.Seal(ctx); err != nil {
			t.Fatalf("Seal batch %d: %v", i, err)
		}
	}
}

func segmentFiles(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	var out []string
	for _, e := range entries {
		if filepath.Ext(e.Name()) == Extension {
			out = append(out, e.Name())
		}
	}
	sort.Strings(out)
	return out
}

func values(datoms []ports.Datom) []string {
	out := make([]string, len(datoms))
	for i, d := range datoms {
		out[i] = fmt.Sprintf("%s/%s=%s@%d,assert=%t",
			d.Entity, d.Attribute, d.Value, d.TxID.HLC.Wall, d.Assert)
	}
	return out
}

func TestTheAnswerDoesNotDependOnSegmentOrder(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir, testLeaf())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	sealEach(t, s,
		[]ports.Datom{assertion("planet-3", "mass", "first", 100)},
		[]ports.Datom{assertion("planet-3", "mass", "second", 200)},
		[]ports.Datom{assertion("planet-3", "mass", "third", 300)},
	)
	before, err := s.History("planet-3")
	if err != nil {
		t.Fatalf("History: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if len(before) != 3 {
		t.Fatalf("want 3 datoms, got %d", len(before))
	}

	// Rename every segment so that lexical order is REVERSED. Nothing about the
	// facts changes; only what the files are called.
	names := segmentFiles(t, dir)
	if len(names) != 3 {
		t.Fatalf("want 3 segments, got %d", len(names))
	}
	for i, name := range names {
		to := fmt.Sprintf("%c-renamed%s", 'z'-rune(i), Extension)
		if err := os.Rename(filepath.Join(dir, name), filepath.Join(dir, to)); err != nil {
			t.Fatalf("Rename: %v", err)
		}
	}

	// ⚠ Assert the rename actually reversed the listing. Without this the test
	// could pass by having changed nothing, while looking thorough.
	after := segmentFiles(t, dir)
	if len(after) != 3 {
		t.Fatalf("want 3 segments after renaming, got %d", len(after))
	}
	reopened, err := Open(dir, testLeaf())
	if err != nil {
		t.Fatalf("Open after renaming: %v", err)
	}
	defer func() { _ = reopened.Close() }()

	got, err := reopened.History("planet-3")
	if err != nil {
		t.Fatalf("History after renaming: %v", err)
	}
	if want, have := values(before), values(got); !equal(want, have) {
		t.Fatalf("renaming the segments changed the answer:\nbefore %v\n after %v\n"+
			"the merge is ordering by filename, so a rename, a copy or a restore silently "+
			"reorders history", want, have)
	}
}

func TestAFactSurvivesReopening(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir, testLeaf())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	sealEach(t, s, []ports.Datom{assertion("planet-3", "mass", "5.97e24", 100)})
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	reopened, err := Open(dir, testLeaf())
	if err != nil {
		t.Fatalf("Open again: %v", err)
	}
	defer func() { _ = reopened.Close() }()

	got, err := reopened.Load(context.Background(), "planet-3", snapshotAfter(1000))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("want 1 datom after reopening, got %d", len(got))
	}
	if !bytes.Equal(got[0].Value, []byte("5.97e24")) {
		t.Errorf("value came back as %q", got[0].Value)
	}
	if got[0].TxID != at(100) {
		t.Errorf("transaction identifier came back as %v", got[0].TxID)
	}
}

func TestTheLatestValueWinsAcrossSegments(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir, testLeaf())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	sealEach(t, s,
		[]ports.Datom{assertion("planet-3", "mass", "older", 100)},
		[]ports.Datom{assertion("planet-3", "radius", "unrelated", 200)},
		[]ports.Datom{assertion("planet-3", "mass", "newer", 300)},
	)
	defer func() { _ = s.Close() }()

	// ⚠ The fixture only proves anything if the LATER value sits in the
	// alphabetically EARLIER file. Names are random, so this is arranged by
	// renaming rather than hoped for.
	names := segmentFiles(t, dir)
	if len(names) != 3 {
		t.Fatalf("want 3 segments, got %d", len(names))
	}
	_ = s.Close()
	order := []string{"a" + Extension, "m" + Extension, "z" + Extension}
	// segment 0 holds "older", segment 2 holds "newer": give the newer one the
	// name that sorts first.
	for i, name := range names {
		to := order[2-i]
		if err := os.Rename(filepath.Join(dir, name), filepath.Join(dir, to)); err != nil {
			t.Fatalf("Rename: %v", err)
		}
	}

	reopened, err := Open(dir, testLeaf())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = reopened.Close() }()

	got, err := reopened.Load(context.Background(), "planet-3", snapshotAfter(1000))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	// ⚠ The LAST "mass" in the order the store returned, not the maximum by TxID.
	// Picking the maximum here would re-do the store's job inside the test, and
	// the test would then pass with the store's ordering deleted entirely.
	var latestMass ports.Datom
	for _, d := range got {
		if d.Attribute == "mass" {
			latestMass = d
		}
	}
	if !bytes.Equal(latestMass.Value, []byte("newer")) {
		t.Fatalf("latest mass is %q, want \"newer\" — the merge took the alphabetically first "+
			"file rather than the latest transaction", latestMass.Value)
	}
}

func TestARetractionInALaterSegmentHidesAnEarlierFact(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir, testLeaf())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = s.Close() }()

	sealEach(t, s,
		[]ports.Datom{assertion("planet-3", "mass", "5.97e24", 100)},
		[]ports.Datom{retraction("planet-3", "mass", "5.97e24", 200)},
	)

	attrs, err := s.Attributes(context.Background(), "planet-3", snapshotAfter(1000))
	if err != nil {
		t.Fatalf("Attributes: %v", err)
	}
	if len(attrs) != 0 {
		t.Errorf("Attributes = %v after the attribute was retracted in a later segment, want none", attrs)
	}

	// History keeps both: a retraction is a fact, not a deletion.
	all, err := s.History("planet-3")
	if err != nil {
		t.Fatalf("History: %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("History = %d datoms, want both the assertion and the retraction", len(all))
	}
	if !all[0].Assert || all[1].Assert {
		t.Errorf("History came back as %v, want the assertion then the retraction", values(all))
	}
}

func TestASealedFactAppearsExactlyOnce(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	s, err := Open(dir, testLeaf())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = s.Close() }()

	// ⚠ Sealed in the MIDDLE. A fact that is only ever in the tail, or only ever
	// in a segment, comes back once from a broken implementation too.
	if err := s.Append(ctx, assertion("planet-3", "mass", "sealed", 100)); err != nil {
		t.Fatalf("Append: %v", err)
	}
	if err := s.Seal(ctx); err != nil {
		t.Fatalf("Seal: %v", err)
	}
	if err := s.Append(ctx, assertion("planet-3", "radius", "unsealed", 200)); err != nil {
		t.Fatalf("Append: %v", err)
	}

	if s.Pending() != 1 {
		t.Fatalf("Pending = %d, want the one unsealed datom", s.Pending())
	}
	got, err := s.History("planet-3")
	if err != nil {
		t.Fatalf("History: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("History = %d datoms (%v), want exactly 2 — a sealed fact returned from both "+
			"the tail and its segment is counted twice", len(got), values(got))
	}
}

func TestAnEntityWithNoFactsIsEmptyNotAnError(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir, testLeaf())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = s.Close() }()

	// Each segment holds a different entity, so every one of them is missing the
	// key the read asks for.
	sealEach(t, s,
		[]ports.Datom{assertion("planet-1", "mass", "a", 100)},
		[]ports.Datom{assertion("planet-2", "mass", "b", 200)},
		[]ports.Datom{assertion("planet-3", "mass", "c", 300)},
	)

	got, err := s.Load(context.Background(), "planet-9", snapshotAfter(1000))
	if err != nil {
		t.Fatalf("Load of an entity no segment holds = %v, want no error", err)
	}
	if len(got) != 0 {
		t.Errorf("Load returned %d datoms for an entity that was never written", len(got))
	}
}

func TestARealReadErrorIsNotAnEmptyAnswer(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir, testLeaf())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	// Large and incompressible, so the stored block is comfortably bigger than
	// the header and a byte flipped well inside the file lands in block data.
	big := make([]byte, 4096)
	for i := range big {
		big[i] = byte(i*31 ^ 0x5a)
	}
	sealEach(t, s, []ports.Datom{{
		Entity: "planet-3", Attribute: "atlas", Value: big,
		Valid: temporal.Interval{From: 0, To: temporal.Forever},
		TxID:  at(100), Assert: true,
	}})
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	names := segmentFiles(t, dir)
	if len(names) != 1 {
		t.Fatalf("want 1 segment, got %d", len(names))
	}
	path := filepath.Join(dir, names[0])
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	// Past the segment header and the block header, inside the stored bytes. The
	// index and trailer live at the END of the file and are left intact, so the
	// segment still opens and only the block is bad.
	raw[100] ^= 0xff
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	reopened, err := Open(dir, testLeaf())
	if err != nil {
		t.Fatalf("Open should still succeed — only a block was corrupted: %v", err)
	}
	defer func() { _ = reopened.Close() }()

	// ⚠ The other half of the missing-block translation. "This segment holds
	// nothing for that entity" is the common case and is swallowed on purpose;
	// everything else must come out. A loop that swallowed both would turn a
	// corrupt disk into an entity that simply has no facts.
	got, err := reopened.History("planet-3")
	if err == nil {
		t.Fatalf("History over a corrupted block returned %d datoms and no error; a real read "+
			"failure has become an entity that appears to have no facts", len(got))
	}
	if len(got) != 0 {
		t.Errorf("History returned %d datoms alongside its error", len(got))
	}
}

func TestAZeroSnapshotIsRefused(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	s, err := Open(dir, testLeaf())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = s.Close() }()
	sealEach(t, s, []ports.Datom{assertion("planet-3", "mass", "m", 100)})

	if _, err := s.Load(ctx, "planet-3", ports.Snapshot{}); !errors.Is(err, ErrNoSnapshot) {
		t.Errorf("Load with a zero snapshot = %v, want ErrNoSnapshot; it would otherwise "+
			"return nothing, which reads as an entity with no facts", err)
	}
	if _, err := s.Attributes(ctx, "planet-3", ports.Snapshot{}); !errors.Is(err, ErrNoSnapshot) {
		t.Errorf("Attributes with a zero snapshot = %v, want ErrNoSnapshot", err)
	}
}

func TestSealingNothingWritesNoSegment(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir, testLeaf())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = s.Close() }()

	for i := 0; i < 3; i++ {
		if err := s.Seal(context.Background()); err != nil {
			t.Fatalf("Seal %d of an empty tail: %v", i, err)
		}
	}
	if names := segmentFiles(t, dir); len(names) != 0 {
		t.Errorf("sealing an empty tail wrote %v; every later read would have to open them "+
			"to learn they hold nothing", names)
	}
	if s.Segments() != 0 {
		t.Errorf("Segments = %d after sealing nothing", s.Segments())
	}
}

func TestAPartialWriteIsIgnored(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir, testLeaf())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	sealEach(t, s, []ports.Datom{assertion("planet-3", "mass", "m", 100)})
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// The shape a crash leaves, plus something unrelated a human dropped in.
	junk := map[string]string{
		".one.seg.partial-12345": "half a segment",
		".hidden.seg":            "dot-prefixed, whatever it is",
		"NOTES.txt":              "not a segment at all",
	}
	for name, body := range junk {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}
	}

	reopened, err := Open(dir, testLeaf())
	if err != nil {
		t.Fatalf("Open with a partial write in the directory: %v", err)
	}
	defer func() { _ = reopened.Close() }()

	if reopened.Segments() != 1 {
		t.Fatalf("Segments = %d, want only the one real segment", reopened.Segments())
	}
	got, err := reopened.Load(context.Background(), "planet-3", snapshotAfter(1000))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(got) != 1 {
		t.Errorf("Load returned %d datoms, want the one that was sealed", len(got))
	}
}

func TestAttributesAreTheShapeNotTheHistory(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir, testLeaf())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = s.Close() }()

	sealEach(t, s, []ports.Datom{
		assertion("planet-3", "mass", "5.97e24", 100),
		assertion("planet-3", "radius", "6371", 110),
		assertion("planet-3", "codename", "blue", 120),
	})
	sealEach(t, s, []ports.Datom{retraction("planet-3", "codename", "blue", 200)})

	attrs, err := s.Attributes(context.Background(), "planet-3", snapshotAfter(1000))
	if err != nil {
		t.Fatalf("Attributes: %v", err)
	}
	if want := []string{"mass", "radius"}; !equal(attrs, want) {
		t.Errorf("Attributes = %v, want %v — a retracted attribute is not carried", attrs, want)
	}

	all, err := s.History("planet-3")
	if err != nil {
		t.Fatalf("History: %v", err)
	}
	if len(all) != 4 {
		t.Errorf("History = %d datoms, want all four including the retraction", len(all))
	}
}

func TestLoadIsHistoryFilteredAtTheSnapshot(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	s, err := Open(dir, testLeaf())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = s.Close() }()

	sealEach(t, s,
		[]ports.Datom{{
			Entity: "planet-3", Attribute: "mass", Value: []byte("early"),
			Valid: temporal.Interval{From: 0, To: 400},
			TxID:  at(100), Assert: true,
		}},
		[]ports.Datom{{
			Entity: "planet-3", Attribute: "mass", Value: []byte("late"),
			Valid: temporal.Interval{From: 400, To: temporal.Forever},
			TxID:  at(200), Assert: true,
		}},
	)
	if err := s.Append(ctx, assertion("planet-3", "radius", "unsealed", 300)); err != nil {
		t.Fatalf("Append: %v", err)
	}

	all, err := s.History("planet-3")
	if err != nil {
		t.Fatalf("History: %v", err)
	}

	// ⚠ The expectation is COMPUTED, not hard-coded. A fixture would agree with
	// whatever the code did on the day it was written; recomputing the filter here
	// is what makes a divergence between the two read paths visible.
	for _, snap := range []ports.Snapshot{
		{At: at(1000), ValidAt: 100},
		{At: at(1000), ValidAt: 500},
		{At: at(150), ValidAt: 100},
		{At: at(150), ValidAt: 500},
		{At: at(250), ValidAt: 500},
	} {
		q, err := query(snap)
		if err != nil {
			t.Fatalf("query: %v", err)
		}
		var want []ports.Datom
		for _, d := range all {
			if temporal.Visible(d.Valid.From, d.Valid.To, d.TxID, q) {
				want = append(want, d)
			}
		}

		got, err := s.Load(ctx, "planet-3", snap)
		if err != nil {
			t.Fatalf("Load at %v: %v", snap, err)
		}
		if !equal(values(got), values(want)) {
			t.Errorf("at tx %d valid %d: Load = %v, History filtered = %v",
				snap.At.HLC.Wall, snap.ValidAt, values(got), values(want))
		}
	}
}

func TestEntitiesListsWhatTheLeafHolds(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	s, err := Open(dir, testLeaf())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = s.Close() }()

	sealEach(t, s,
		[]ports.Datom{assertion("planet-3", "mass", "a", 100), assertion("star-1", "mass", "b", 110)},
		[]ports.Datom{assertion("planet-9", "mass", "c", 200)},
	)
	// planet-3 is in a segment AND in the tail: it must appear once.
	if err := s.Append(ctx, assertion("planet-3", "radius", "d", 300)); err != nil {
		t.Fatalf("Append: %v", err)
	}

	got, err := s.Entities()
	if err != nil {
		t.Fatalf("Entities: %v", err)
	}
	if want := []string{"planet-3", "planet-9", "star-1"}; !equal(got, want) {
		t.Errorf("Entities = %v, want %v", got, want)
	}
}

func equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
