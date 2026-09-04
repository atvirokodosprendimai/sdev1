package leafstore

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/atvirokodosprendimai/sdev1/internal/core/datom"
	"github.com/atvirokodosprendimai/sdev1/internal/core/ports"
	"github.com/atvirokodosprendimai/sdev1/internal/core/segment"
	"github.com/atvirokodosprendimai/sdev1/internal/core/segstore"
)

// mixedSubjects reports the keys of any block whose datoms name an entity other
// than the key itself.
//
// It is the whole check, factored out so the test can prove it is capable of
// FAILING before trusting it to pass.
func mixedSubjects(t *testing.T, path string) []string {
	t.Helper()
	r, err := segstore.Open(path)
	if err != nil {
		t.Fatalf("segstore.Open(%s): %v", filepath.Base(path), err)
	}
	defer func() { _ = r.Close() }()

	keys, err := r.Keys()
	if err != nil {
		t.Fatalf("Keys: %v", err)
	}
	if len(keys) == 0 {
		t.Fatalf("%s holds no blocks; a checker that reads nothing passes everything",
			filepath.Base(path))
	}

	var offenders []string
	for _, key := range keys {
		raw, err := r.Get(key)
		if err != nil {
			t.Fatalf("Get(%q): %v", key, err)
		}
		run, err := datom.Decode(raw)
		if err != nil {
			t.Fatalf("Decode(%q): %v", key, err)
		}
		if len(run) == 0 {
			t.Fatalf("block %q decoded to no datoms", key)
		}
		for _, d := range run {
			if d.Entity != key {
				offenders = append(offenders, key)
				break
			}
		}
	}
	return offenders
}

func TestNoBlockMixesSubjects(t *testing.T) {
	ctx := context.Background()

	// ⚠ FIRST: prove the checker can fail. The production code already satisfies
	// this rule, so a checker with a bug — reading no blocks, comparing nothing —
	// would pass exactly as loudly as a correct one. A segment that deliberately
	// packs two subjects into one block is built by hand, and the checker must
	// reject it.
	mixedDir := t.TempDir()
	mixedPath := filepath.Join(mixedDir, "mixed.seg")
	w, err := segstore.Create(mixedPath, testLeaf())
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	packed, err := datom.Encode([]ports.Datom{
		assertion("planet-3", "mass", "5", 100),
		assertion("star-1", "mass", "9", 110),
	})
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	if err := w.Append("planet-3", packed, segment.CodecIdentity); err != nil {
		t.Fatalf("Append: %v", err)
	}
	if err := w.Seal(); err != nil {
		t.Fatalf("Seal: %v", err)
	}
	if got := mixedSubjects(t, mixedPath); len(got) != 1 {
		t.Fatalf("the checker accepted a block holding two subjects (offenders: %v); it cannot "+
			"be trusted to pass on a real segment until it is shown to fail on this one", got)
	}

	// THEN: the real thing, written through the ordinary path.
	dir := t.TempDir()
	s, err := Open(dir, testLeaf())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = s.Close() }()

	sealEach(t, s,
		[]ports.Datom{
			assertion("planet-3", "mass", "first", 100),
			assertion("star-1", "codename", "yellow", 110),
			assertion("planet-9", "mass", "1.9e27", 120),
		},
		[]ports.Datom{
			assertion("planet-3", "mass", "second", 200),
			assertion("star-1", "radius", "696340", 210),
		},
	)

	// ⚠ Read back from the FILE, not from the grouping that produced it. The
	// rule is about what the format contains.
	for _, name := range segmentFiles(t, dir) {
		if got := mixedSubjects(t, filepath.Join(dir, name)); len(got) != 0 {
			t.Errorf("after sealing, blocks %v hold datoms of more than one subject — a shared "+
				"block makes one subject's data a probe for another's, makes shredding a rewrite, "+
				"and makes reclaim impossible", got)
		}
	}

	// ⚠ And after a COMPACTION, which rewrites every block at once and is the
	// natural place for somebody to improve packing.
	if err := s.Compact(ctx); err != nil {
		t.Fatalf("Compact: %v", err)
	}
	after := segmentFiles(t, dir)
	if len(after) != 1 {
		t.Fatalf("compaction left %d segments, want 1", len(after))
	}
	if got := mixedSubjects(t, filepath.Join(dir, after[0])); len(got) != 0 {
		t.Errorf("after compaction, blocks %v hold datoms of more than one subject", got)
	}
}
