package crypt

import (
	"bytes"
	"errors"
	"reflect"
	"sort"
	"testing"
)

// sealFor allocates a subject's key and seals plaintext under it, the way a
// storage engine would.
func sealFor(t *testing.T, ks *MemoryKeystore, subject string, plaintext []byte) ([]byte, KeyID) {
	t.Helper()
	id, err := ks.Allocate(subject)
	if err != nil {
		t.Fatalf("Allocate(%q): %v", subject, err)
	}
	k, err := ks.Fetch(id)
	if err != nil {
		t.Fatalf("Fetch after Allocate: %v", err)
	}
	sealed, err := Seal(id, k, plaintext)
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}
	return sealed, id
}

// TestShreddedSubjectIsUnreadable is the falsifier ADR-007 names in its
// Enforced-by header.
//
// ⚠ It asserts the blocks WERE readable first. A test that only checks
// unreadability afterwards passes when the fixture never worked, which would make
// the central claim of this record rest on nothing.
func TestShreddedSubjectIsUnreadable(t *testing.T) {
	ks := NewMemoryKeystore()
	const subject = "alice@example.com"

	// Several blocks, as a subject would accumulate over time.
	plaintexts := [][]byte{
		[]byte("the first thing alice wrote"),
		[]byte("the second, some time later"),
		bytes.Repeat([]byte("and a larger one "), 500),
		nil,
	}
	sealed := make([][]byte, len(plaintexts))
	var id KeyID
	for i, p := range plaintexts {
		sealed[i], id = sealFor(t, ks, subject, p)
	}

	// Readable now. This is the half a careless test omits.
	for i, s := range sealed {
		got, err := Open(ks, s)
		if err != nil {
			t.Fatalf("block %d was not readable before the shred: %v", i, err)
		}
		if !bytes.Equal(got, plaintexts[i]) {
			t.Fatalf("block %d did not round trip before the shred", i)
		}
	}

	// One key. One act.
	if _, err := ks.Shred(id, "erasure-request-2026-09-04-001"); err != nil {
		t.Fatalf("Shred: %v", err)
	}

	// Every block, at once, without any of them being visited.
	for i, s := range sealed {
		got, err := Open(ks, s)
		if !errors.Is(err, ErrKeyDestroyed) {
			t.Errorf("block %d after the shred: error = %v, want ErrKeyDestroyed", i, err)
		}
		if got != nil {
			t.Errorf("block %d returned %d bytes of plaintext after the shred", i, len(got))
		}
	}

	// The ciphertext still exists, and that is the point: nothing was rewritten.
	for i, s := range sealed {
		if len(s) < MinEnvelopeSize {
			t.Errorf("block %d was altered by the shred; erasure must rewrite nothing", i)
		}
	}
}

// TestShreddingForgetsTheSubjectMapping checks the mapping dies with the key.
//
// Removing only the key would leave a durable record binding a handle to a
// subject — beside ciphertext that lasts forever. That record is exactly the
// un-erasable identifier this design exists to prevent.
func TestShreddingForgetsTheSubjectMapping(t *testing.T) {
	ks := NewMemoryKeystore()
	const subject = "bob@example.com"

	_, id := sealFor(t, ks, subject, []byte("something"))

	if got, ok := ks.Resolve(subject); !ok || got != id {
		t.Fatalf("Resolve before the shred: got %x, %v; want %x, true", got, ok, id)
	}

	if _, err := ks.Shred(id, "req-2"); err != nil {
		t.Fatalf("Shred: %v", err)
	}

	if got, ok := ks.Resolve(subject); ok {
		t.Errorf("the subject still resolves to %x after the shred; a record binding an "+
			"identity to permanent ciphertext survived the erasure", got)
	}
}

// TestShredRecordNamesNoSubject is a reflective check over the struct.
//
// ⚠ It enumerates the fields rather than checking known ones. A hand-written
// list of "fields that must not exist" passes when a field is ADDED, which is
// the exact change that breaks the guarantee — somebody adding `Subject string`
// "just for debugging" is the failure mode, and it must fail here.
func TestShredRecordNamesNoSubject(t *testing.T) {
	want := map[string]string{
		"KeyID":   "crypt.KeyID",
		"At":      "int64",
		"Request": "string",
	}

	typ := reflect.TypeOf(ShredRecord{})
	var got []string
	for i := 0; i < typ.NumField(); i++ {
		f := typ.Field(i)
		got = append(got, f.Name)
		expected, ok := want[f.Name]
		if !ok {
			t.Errorf("ShredRecord has an unexpected field %q of type %s — the audit trail is "+
				"durable, so any field that could carry a subject makes the erasure incomplete",
				f.Name, f.Type)
			continue
		}
		if f.Type.String() != expected {
			t.Errorf("field %q is %s, want %s", f.Name, f.Type, expected)
		}
	}
	sort.Strings(got)
	if len(got) != len(want) {
		t.Fatalf("ShredRecord has %d fields %v, want exactly %d", len(got), got, len(want))
	}

	// The recorded values carry the handle and the request, and the handle names
	// nobody.
	ks := NewMemoryKeystore()
	ks.now = func() int64 { return 1757000000000000000 }
	_, id := sealFor(t, ks, "carol@example.com", []byte("x"))
	rec, err := ks.Shred(id, "req-3")
	if err != nil {
		t.Fatalf("Shred: %v", err)
	}
	if rec.KeyID != id {
		t.Errorf("the record names handle %x, want %x", rec.KeyID, id)
	}
	if rec.At != 1757000000000000000 {
		t.Errorf("the record's instant is %d, want the injected clock's value", rec.At)
	}
	if rec.Request != "req-3" {
		t.Errorf("the record's request is %q, want %q", rec.Request, "req-3")
	}
	if bytes.Contains(rec.KeyID[:], []byte("carol")) {
		t.Error("the recorded handle contains the subject")
	}

	trail := ks.Shreds()
	if len(trail) != 1 || trail[0] != rec {
		t.Errorf("the audit trail holds %d entries, want the one just written", len(trail))
	}
}

// TestDestroyIsIdempotentAndFinal checks destruction cannot be undone, and that
// re-allocating a subject does not resurrect its data.
func TestDestroyIsIdempotentAndFinal(t *testing.T) {
	ks := NewMemoryKeystore()
	const subject = "dave@example.com"
	plaintext := []byte("written before the erasure")

	sealed, id := sealFor(t, ks, subject, plaintext)
	if _, err := Open(ks, sealed); err != nil {
		t.Fatalf("not readable before the shred: %v", err)
	}

	if err := ks.Destroy(id); err != nil {
		t.Fatalf("Destroy: %v", err)
	}
	// Idempotent: a second destruction succeeds rather than erroring, because an
	// erasure request arriving twice is ordinary.
	if err := ks.Destroy(id); err != nil {
		t.Errorf("a second Destroy: %v, want success", err)
	}

	// Destroyed is not the same fact as never-issued.
	if _, err := ks.Fetch(id); !errors.Is(err, ErrKeyDestroyed) {
		t.Errorf("a destroyed handle: error = %v, want ErrKeyDestroyed", err)
	}
	var stranger KeyID
	copy(stranger[:], bytes.Repeat([]byte{0x5A}, KeyIDSize))
	if _, err := ks.Fetch(stranger); !errors.Is(err, ErrUnknownKey) {
		t.Errorf("a handle never issued: error = %v, want ErrUnknownKey", err)
	}
	if errors.Is(errors.New("x"), ErrKeyDestroyed) {
		t.Error("the sentinels are not distinct")
	}

	// Re-allocating the subject issues a NEW handle, which cannot read the old
	// blocks. A subject can come back; its erased data cannot.
	again, err := ks.Allocate(subject)
	if err != nil {
		t.Fatalf("Allocate after the shred: %v", err)
	}
	if again == id {
		t.Fatal("re-allocating the subject returned the destroyed handle; the erasure was undone")
	}
	if _, err := Open(ks, sealed); !errors.Is(err, ErrKeyDestroyed) {
		t.Errorf("after re-allocation the old block: error = %v, want ErrKeyDestroyed", err)
	}

	// New data under the new handle is readable, so the subject is usable again.
	fresh, _ := sealFor(t, ks, subject, []byte("written after"))
	if got, err := Open(ks, fresh); err != nil || !bytes.Equal(got, []byte("written after")) {
		t.Errorf("data written after re-allocation is not readable: %v", err)
	}
}

// TestAllocateIsStableForOneSubject checks a subject keeps one handle, so its
// blocks share a key and are erased together rather than one at a time.
func TestAllocateIsStableForOneSubject(t *testing.T) {
	ks := NewMemoryKeystore()
	const subject = "erin@example.com"

	first, err := ks.Allocate(subject)
	if err != nil {
		t.Fatalf("Allocate: %v", err)
	}
	for i := 0; i < 50; i++ {
		again, err := ks.Allocate(subject)
		if err != nil {
			t.Fatalf("Allocate %d: %v", i, err)
		}
		if again != first {
			t.Fatalf("allocation %d returned a different handle; a subject's blocks would "+
				"then need erasing one at a time, which is the sweep this design avoids", i)
		}
	}

	// Different subjects get different handles, or one erasure would take out
	// several people.
	other, err := ks.Allocate("frank@example.com")
	if err != nil {
		t.Fatalf("Allocate other: %v", err)
	}
	if other == first {
		t.Fatal("two subjects share a handle; erasing one would erase the other")
	}

	// And the mapping is what makes that stable, so it resolves both ways.
	if got, ok := ks.Resolve(subject); !ok || got != first {
		t.Errorf("Resolve(%q) = %x, %v; want %x, true", subject, got, ok, first)
	}
}
