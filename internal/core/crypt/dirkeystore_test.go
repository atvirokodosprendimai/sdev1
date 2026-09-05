package crypt

import (
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func openStore(t *testing.T, dir string) *DirKeystore {
	t.Helper()
	ks, err := OpenDirKeystore(dir)
	if err != nil {
		t.Fatalf("OpenDirKeystore: %v", err)
	}
	return ks
}

func TestADestroyedKeyIsGoneFromTheCacheAndTheDisk(t *testing.T) {
	dir := t.TempDir()
	ks := openStore(t, dir)

	id, err := ks.Allocate("planet-3")
	if err != nil {
		t.Fatalf("Allocate: %v", err)
	}

	// ⚠ Fetched BEFORE the destroy, so the key is certainly in the cache. A test
	// that only reopened the store would never exercise the eviction.
	if _, err := ks.Fetch(id); err != nil {
		t.Fatalf("Fetch: %v", err)
	}

	if err := ks.Destroy(id); err != nil {
		t.Fatalf("Destroy: %v", err)
	}

	// Half one: the instance that was serving it a moment ago.
	if _, err := ks.Fetch(id); !errors.Is(err, ErrKeyDestroyed) {
		t.Errorf("the destroying instance still fetches the key: %v — a cached key that outlives "+
			"its destruction leaves a shredded subject readable wherever it was cached", err)
	}

	// Half two: the disk. An implementation can get either half alone right.
	reopened := openStore(t, dir)
	if _, err := reopened.Fetch(id); !errors.Is(err, ErrKeyDestroyed) {
		t.Errorf("a keystore reopened on the same directory still fetches the key: %v", err)
	}
}

func TestAKeyOutlivesTheProcess(t *testing.T) {
	dir := t.TempDir()

	first := openStore(t, dir)
	id, err := first.Allocate("planet-3")
	if err != nil {
		t.Fatalf("Allocate: %v", err)
	}
	want, err := first.Fetch(id)
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}

	// A different keystore on the same directory: the thing MemoryKeystore
	// cannot do, and the reason erasure was previously safe in the wrong
	// direction — every key lost on restart erases everything.
	second := openStore(t, dir)
	got, err := second.Fetch(id)
	if err != nil {
		t.Fatalf("Fetch after reopening: %v", err)
	}
	if got != want {
		t.Errorf("the key came back different after a reopen")
	}
	if resolved, held := second.Resolve("planet-3"); !held || resolved != id {
		t.Errorf("Resolve after reopening = %x, %t; want the original handle", resolved[:], held)
	}
}

func TestTheMappingDiesWithTheKey(t *testing.T) {
	dir := t.TempDir()
	ks := openStore(t, dir)

	id, err := ks.Allocate("planet-3")
	if err != nil {
		t.Fatalf("Allocate: %v", err)
	}
	if _, held := ks.Resolve("planet-3"); !held {
		t.Fatal("the subject is not resolvable before the destroy")
	}

	if err := ks.Destroy(id); err != nil {
		t.Fatalf("Destroy: %v", err)
	}

	if _, held := ks.Resolve("planet-3"); held {
		t.Error("the subject is still resolvable after its key was destroyed; nothing durable " +
			"may still bind the identity to the ciphertext")
	}
	if _, held := openStore(t, dir).Resolve("planet-3"); held {
		t.Error("the subject is resolvable by a keystore reopened on the same directory")
	}
}

func TestADanglingIndexEntryIsNotAReadableKey(t *testing.T) {
	dir := t.TempDir()
	ks := openStore(t, dir)

	id, err := ks.Allocate("planet-3")
	if err != nil {
		t.Fatalf("Allocate: %v", err)
	}

	// ⚠ The state a crash BETWEEN the two removals leaves, built by deleting the
	// key file directly rather than by a helper that pretends to crash. The point
	// is that the real surviving state is safe.
	if err := os.Remove(filepath.Join(dir, hex.EncodeToString(id[:])+".key")); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "subjects")); err != nil {
		t.Fatalf("the subject index is missing entirely: %v", err)
	}

	reopened := openStore(t, dir)
	if _, held := reopened.Resolve("planet-3"); held {
		t.Error("a dangling index entry resolved to a live subject; the litter a crash leaves " +
			"must not read as a subject that still exists")
	}
	if _, err := reopened.Fetch(id); !errors.Is(err, ErrKeyDestroyed) {
		t.Errorf("Fetch on the dangling handle = %v, want ErrKeyDestroyed", err)
	}
}

func TestAnUnwritableDirectoryIsRefused(t *testing.T) {
	// A path whose parent is a FILE cannot be created, so the keystore cannot
	// write and — more to the point — cannot ever delete.
	parent := t.TempDir()
	blocker := filepath.Join(parent, "not-a-directory")
	if err := os.WriteFile(blocker, []byte("x"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	ks, err := OpenDirKeystore(filepath.Join(blocker, "keys"))
	if !errors.Is(err, ErrNotDeletable) {
		t.Fatalf("OpenDirKeystore on an unusable path = %v, want ErrNotDeletable", err)
	}
	if ks != nil {
		t.Error("a keystore was returned alongside the refusal")
	}

	// ⚠ Refused at OPEN. A keystore that discovers it cannot delete at the moment
	// somebody exercises their erasure right has already failed.

	// And an existing directory that cannot be WRITTEN to: the probe's other
	// half, and the one a wrong mount actually produces.
	if os.Geteuid() == 0 {
		t.Skip("running as root, which bypasses directory permissions")
	}
	readonly := t.TempDir()
	if err := os.Chmod(readonly, 0o500); err != nil {
		t.Fatalf("Chmod: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(readonly, 0o700) })

	if _, err := OpenDirKeystore(filepath.Join(readonly, "keys")); !errors.Is(err, ErrNotDeletable) {
		t.Errorf("OpenDirKeystore on a read-only directory = %v, want ErrNotDeletable", err)
	}
}

func TestDirKeystoreShredsRecordNoSubject(t *testing.T) {
	dir := t.TempDir()
	ks := openStore(t, dir)

	id, err := ks.Allocate("planet-3")
	if err != nil {
		t.Fatalf("Allocate: %v", err)
	}
	record, err := ks.Shred(id, "request-42")
	if err != nil {
		t.Fatalf("Shred: %v", err)
	}
	if record.KeyID != id || record.Request != "request-42" || record.At == 0 {
		t.Errorf("the shred record is %+v", record)
	}
	if got := ks.Shreds(); len(got) != 1 || got[0].KeyID != id {
		t.Errorf("Shreds = %+v, want the one destruction", got)
	}
	if _, err := ks.Fetch(id); !errors.Is(err, ErrKeyDestroyed) {
		t.Errorf("the key survived a shred: %v", err)
	}
}
