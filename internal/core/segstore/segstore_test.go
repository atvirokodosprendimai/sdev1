package segstore

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"testing"

	"github.com/atvirokodosprendimai/sdev1/internal/core/addr"
	"github.com/atvirokodosprendimai/sdev1/internal/core/segment"
)

// The tests use a REAL temporary directory rather than a filesystem abstraction.
// The central claim is about when a directory entry appears, and an abstraction
// would be asserting the abstraction rather than the behaviour.

func testLeaf() addr.LeafID { return addr.TenantFromUint(7).TenantSubtree() }

// payload builds deterministic bytes of a given length.
func payload(n int, seed byte) []byte {
	b := make([]byte, n)
	for i := range b {
		b[i] = byte(i*31) ^ seed
	}
	return b
}

// sealSegment writes blocks into a fresh segment and returns its path.
func sealSegment(t *testing.T, blocks map[string][]byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "one.seg")
	w, err := Create(path, testLeaf())
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	defer func() { _ = w.Abort() }()

	for key, raw := range blocks {
		if err := w.Append(key, raw, segment.CodecIdentity); err != nil {
			t.Fatalf("Append(%q): %v", key, err)
		}
	}
	if err := w.Seal(); err != nil {
		t.Fatalf("Seal: %v", err)
	}
	return path
}

// readTrailer parses the trailer of a segment already on disk, so a test can aim
// its corruption at one region rather than hoping.
func readTrailer(t *testing.T, data []byte) trailer {
	t.Helper()
	tr, err := decodeTrailer(data[len(data)-TrailerSize:])
	if err != nil {
		t.Fatalf("decoding the trailer of a segment this test just sealed: %v", err)
	}
	return tr
}

func TestAnUnsealedSegmentDoesNotExistAtItsPath(t *testing.T) {
	dir := t.TempDir()
	dest := filepath.Join(dir, "s.seg")

	w, err := Create(dest, testLeaf())
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	defer func() { _ = w.Abort() }()

	for _, key := range []string{"alpha", "beta", "gamma"} {
		if err := w.Append(key, payload(4096, key[0]), segment.CodecIdentity); err != nil {
			t.Fatalf("Append(%q): %v", key, err)
		}
	}

	if _, err := os.Stat(dest); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("the destination exists before Seal (stat error was %v); a reader listing this "+
			"directory would see a half-written segment, and ADR-017's lock-free read path assumes it cannot", err)
	}

	// ⚠ The second half of the claim. Without it a writer that buffered every
	// block in memory and wrote nothing at all would pass the assertion above.
	ents, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	if len(ents) != 1 {
		t.Fatalf("want exactly one file being written, got %d", len(ents))
	}
	if ents[0].Name() == filepath.Base(dest) {
		t.Fatalf("the one file present is the destination itself")
	}
	info, err := ents[0].Info()
	if err != nil {
		t.Fatalf("Info: %v", err)
	}
	if info.Size() == 0 {
		t.Fatalf("the temporary file %s is empty: nothing has actually been written yet", ents[0].Name())
	}

	if err := w.Seal(); err != nil {
		t.Fatalf("Seal: %v", err)
	}

	if _, err := os.Stat(dest); err != nil {
		t.Fatalf("the destination does not exist after Seal: %v", err)
	}
	ents, err = os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	if len(ents) != 1 || ents[0].Name() != filepath.Base(dest) {
		t.Fatalf("after Seal the directory should hold only %s, got %v", filepath.Base(dest), names(ents))
	}
}

func TestRoundTripsEveryBlock(t *testing.T) {
	blocks := map[string][]byte{
		"zeta":  payload(11, 3),
		"empty": {},
		"alpha": payload(1<<20, 9),
		"mid":   payload(4096, 1),
	}

	// Written through the compressing codec as well, to show the container is
	// ignorant of what a block did to itself — ADR-005 owns that.
	path := filepath.Join(t.TempDir(), "mixed.seg")
	w, err := Create(path, testLeaf())
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	defer func() { _ = w.Abort() }()
	codec := segment.CodecIdentity
	for key, raw := range blocks {
		if err := w.Append(key, raw, codec); err != nil {
			t.Fatalf("Append(%q): %v", key, err)
		}
		codec = segment.CodecZstd
	}
	if err := w.Seal(); err != nil {
		t.Fatalf("Seal: %v", err)
	}

	r, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = r.Close() }()

	leaf, err := r.Leaf()
	if err != nil {
		t.Fatalf("Leaf: %v", err)
	}
	if leaf != testLeaf() {
		t.Errorf("Leaf = %v, want %v", leaf, testLeaf())
	}

	want := make([]string, 0, len(blocks))
	for key := range blocks {
		want = append(want, key)
	}
	sort.Strings(want)
	got, err := r.Keys()
	if err != nil {
		t.Fatalf("Keys: %v", err)
	}
	if !equalStrings(got, want) {
		t.Errorf("Keys = %v, want %v", got, want)
	}

	for key, raw := range blocks {
		block, err := r.Get(key)
		if err != nil {
			t.Errorf("Get(%q): %v", key, err)
			continue
		}
		if !bytes.Equal(block, raw) {
			t.Errorf("Get(%q) returned %d bytes, want %d identical", key, len(block), len(raw))
		}
	}
}

func TestACorruptIndexIsRefused(t *testing.T) {
	path := sealSegment(t, map[string][]byte{
		"alpha": payload(64, 1),
		"beta":  payload(64, 2),
		"gamma": payload(64, 3),
	})

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	tr := readTrailer(t, data)
	entries, err := decodeIndex(data[tr.IndexOff : tr.IndexOff+tr.IndexLen])
	if err != nil {
		t.Fatalf("decoding the index of a segment this test just sealed: %v", err)
	}
	if len(entries) != 3 {
		t.Fatalf("want 3 index entries, got %d", len(entries))
	}

	// ⚠ Aimed INSIDE the index, and at a NAMED FIELD rather than at a byte in the
	// middle. A flipped offset is caught by the bounds check and a flipped key by
	// the order check — either would make this test pass while leaving the
	// checksum, which is what it claims to prove, entirely unexercised.
	//
	// The block written FIRST is the one to aim at: index order is by key and file
	// order is by append, so only for that one is a span a byte longer certain to
	// stay inside the block region.
	target := 0
	for i := range entries {
		if entries[i].Offset < entries[target].Offset {
			target = i
		}
	}
	at := 4
	for i := 0; i < target; i++ {
		at += 2 + len(entries[i].Key) + 8 + 4
	}
	at += 2 + len(entries[target].Key) + 8 // into this entry's span
	spanLowByte := at + 3

	// So establish that this corruption really does survive every OTHER check.
	// If it stops doing so, this test starts passing for the wrong reason, and
	// that is what these two assertions refuse to let happen quietly.
	probe := append([]byte(nil), data[tr.IndexOff:tr.IndexOff+tr.IndexLen]...)
	probe[spanLowByte] ^= 0x01
	survivors, err := decodeIndex(probe)
	if err != nil {
		t.Fatalf("the corruption must survive decoding so that only the checksum can refuse it: %v", err)
	}
	if err := checkEntries(survivors, tr.IndexOff); err != nil {
		t.Fatalf("the corruption must survive the bounds and order checks so that only the checksum can refuse it: %v", err)
	}

	data[int(tr.IndexOff)+spanLowByte] ^= 0x01
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	r, err := Open(path)
	if err == nil {
		_ = r.Close()
		t.Fatalf("Open accepted a segment whose index failed its checksum")
	}
	if !errors.Is(err, ErrIndexCorrupt) {
		t.Fatalf("Open error = %v, want ErrIndexCorrupt", err)
	}
}

func TestATruncatedFileIsNotASegment(t *testing.T) {
	dir := t.TempDir()

	// The shape a crash leaves at each stage: nothing written, a few bytes
	// written, and everything but the trailer written.
	empty := filepath.Join(dir, "empty.seg")
	if err := os.WriteFile(empty, nil, 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	short := filepath.Join(dir, "short.seg")
	if err := os.WriteFile(short, []byte("SDEV1"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	full := sealSegment(t, map[string][]byte{"alpha": payload(256, 1), "beta": payload(256, 2)})
	data, err := os.ReadFile(full)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	headless := filepath.Join(dir, "notrailer.seg")
	if err := os.WriteFile(headless, data[:len(data)-TrailerSize-1], 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	for _, path := range []string{empty, short, headless} {
		r, err := Open(path)
		if err == nil {
			_ = r.Close()
			t.Errorf("Open(%s) accepted a file that is not a segment", filepath.Base(path))
			continue
		}
		if !errors.Is(err, ErrNotASegment) {
			t.Errorf("Open(%s) error = %v, want ErrNotASegment", filepath.Base(path), err)
		}
	}
}

func TestAMissingKeyIsNamed(t *testing.T) {
	// ⚠ An EMPTY block is written on purpose. Without one, "absent" and "present
	// but empty" are never actually told apart — only one of them is tested.
	path := sealSegment(t, map[string][]byte{
		"present-and-empty": {},
		"present":           payload(32, 4),
	})

	r, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = r.Close() }()

	block, err := r.Get("present-and-empty")
	if err != nil {
		t.Fatalf("Get on a block that exists and is empty: %v", err)
	}
	if len(block) != 0 {
		t.Errorf("an empty block came back as %d bytes", len(block))
	}

	if _, err := r.Get("absent"); !errors.Is(err, ErrNoSuchBlock) {
		t.Fatalf("Get on a key never written = %v, want ErrNoSuchBlock", err)
	}
}

func TestACorruptBlockIsRefusedOnRead(t *testing.T) {
	path := sealSegment(t, map[string][]byte{"alpha": payload(512, 7)})

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	tr := readTrailer(t, data)
	entries, err := decodeIndex(data[tr.IndexOff : tr.IndexOff+tr.IndexLen])
	if err != nil {
		t.Fatalf("decoding the index of a segment this test just sealed: %v", err)
	}

	// Aimed at the block's STORED bytes, leaving the index and its checksum
	// untouched: the index being right is exactly what must not make the data
	// trusted.
	data[int(entries[0].Offset)+segment.BlockHeaderSize] ^= 0xff
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	r, err := Open(path)
	if err != nil {
		t.Fatalf("Open should still succeed — only a block was corrupted: %v", err)
	}
	defer func() { _ = r.Close() }()

	if _, err := r.Get("alpha"); !errors.Is(err, segment.ErrCorruptBlock) {
		t.Fatalf("Get on a corrupted block = %v, want segment.ErrCorruptBlock", err)
	}
}

func TestAbortLeavesNothingBehind(t *testing.T) {
	dir := t.TempDir()
	dest := filepath.Join(dir, "s.seg")

	w, err := Create(dest, testLeaf())
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := w.Append("alpha", payload(1024, 1), segment.CodecIdentity); err != nil {
		t.Fatalf("Append: %v", err)
	}
	if err := w.Abort(); err != nil {
		t.Fatalf("Abort: %v", err)
	}

	ents, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	if len(ents) != 0 {
		t.Fatalf("Abort left %v behind", names(ents))
	}
}

func TestADuplicateKeyIsRefused(t *testing.T) {
	w, err := Create(filepath.Join(t.TempDir(), "s.seg"), testLeaf())
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	defer func() { _ = w.Abort() }()

	if err := w.Append("alpha", payload(16, 1), segment.CodecIdentity); err != nil {
		t.Fatalf("Append: %v", err)
	}
	// A second entry under one key is a block that is written, paid for, and
	// unreachable through a binary search.
	if err := w.Append("alpha", payload(16, 2), segment.CodecIdentity); !errors.Is(err, ErrDuplicateKey) {
		t.Fatalf("second Append under the same key = %v, want ErrDuplicateKey", err)
	}
}

func TestAppendAfterSealIsRefused(t *testing.T) {
	w, err := Create(filepath.Join(t.TempDir(), "s.seg"), testLeaf())
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := w.Append("alpha", payload(16, 1), segment.CodecIdentity); err != nil {
		t.Fatalf("Append: %v", err)
	}
	if err := w.Seal(); err != nil {
		t.Fatalf("Seal: %v", err)
	}

	if err := w.Append("beta", payload(16, 2), segment.CodecIdentity); !errors.Is(err, ErrSealed) {
		t.Errorf("Append after Seal = %v, want ErrSealed", err)
	}
	if err := w.Seal(); !errors.Is(err, ErrSealed) {
		t.Errorf("second Seal = %v, want ErrSealed", err)
	}
	// Abort after a successful Seal is a no-op, so `defer w.Abort()` beside a
	// `w.Seal()` is the safe shape rather than a guaranteed second error.
	if err := w.Abort(); err != nil {
		t.Errorf("Abort after Seal = %v, want nil", err)
	}
}

func TestABlockOutlivesTheReaderThatReturnedIt(t *testing.T) {
	// ⚠ Large enough to span several pages, so a returned view into the mapping
	// certainly touches unmapped memory rather than possibly.
	want := payload(256<<10, 5)
	path := sealSegment(t, map[string][]byte{"alpha": want})

	r, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	got, err := r.Get("alpha")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if err := r.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// ⚠ Read AFTER the close, which is the only order that can see the defect.
	// If Get had returned a view into the mapping, this line faults — the test
	// fails as a signal rather than as an assertion, which is itself the point:
	// the bug is invisible to every test that reads before closing.
	if !bytes.Equal(got, want) {
		t.Fatalf("a block read before Close is %d bytes afterwards, want %d identical", len(got), len(want))
	}
}

func TestGetAfterCloseIsRefused(t *testing.T) {
	path := sealSegment(t, map[string][]byte{"alpha": payload(64, 1)})
	r, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := r.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	if _, err := r.Get("alpha"); !errors.Is(err, ErrClosed) {
		t.Errorf("Get after Close = %v, want ErrClosed", err)
	}
	if _, err := r.Keys(); !errors.Is(err, ErrClosed) {
		t.Errorf("Keys after Close = %v, want ErrClosed", err)
	}
	if _, err := r.Leaf(); !errors.Is(err, ErrClosed) {
		t.Errorf("Leaf after Close = %v, want ErrClosed", err)
	}
	if err := r.Close(); err != nil {
		t.Errorf("second Close = %v, want nil", err)
	}
}

func TestAReadDoesNotRaceAClose(t *testing.T) {
	want := payload(64<<10, 2)
	path := sealSegment(t, map[string][]byte{"alpha": want})

	r, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	const readers = 32
	problems := make(chan error, readers+1)
	var wg sync.WaitGroup

	for i := 0; i < readers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			block, err := r.Get("alpha")
			switch {
			case err == nil:
				if !bytes.Equal(block, want) {
					problems <- fmt.Errorf("a concurrent read returned %d bytes, want %d identical", len(block), len(want))
				}
			case errors.Is(err, ErrClosed):
				// The only other legitimate outcome.
			default:
				problems <- fmt.Errorf("a concurrent read failed: %w", err)
			}
		}()
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		if err := r.Close(); err != nil {
			problems <- fmt.Errorf("Close: %w", err)
		}
	}()

	wg.Wait()
	close(problems)
	for err := range problems {
		t.Error(err)
	}
}

func names(ents []os.DirEntry) []string {
	out := make([]string, len(ents))
	for i, e := range ents {
		out[i] = e.Name()
	}
	return out
}

func equalStrings(a, b []string) bool {
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
