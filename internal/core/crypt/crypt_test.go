package crypt

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"fmt"
	"math/rand"
	"testing"
)

// stubKeystore is the smallest thing that satisfies the interface, so T1's tests
// do not depend on T2's implementation. It also lets a test refuse a fetch, which
// is how "the keystore is the authority" is observed.
type stubKeystore struct {
	keys    map[KeyID]Key
	fetched []KeyID
	refuse  error
}

func newStub() *stubKeystore { return &stubKeystore{keys: map[KeyID]Key{}} }

func (s *stubKeystore) put(t *testing.T) (KeyID, Key) {
	t.Helper()
	id, err := NewKeyID()
	if err != nil {
		t.Fatalf("NewKeyID: %v", err)
	}
	k, err := NewKey()
	if err != nil {
		t.Fatalf("NewKey: %v", err)
	}
	s.keys[id] = k
	return id, k
}

func (s *stubKeystore) Allocate(string) (KeyID, error) { return NewKeyID() }
func (s *stubKeystore) Resolve(string) (KeyID, bool)   { return KeyID{}, false }
func (s *stubKeystore) Destroy(id KeyID) error         { delete(s.keys, id); return nil }

func (s *stubKeystore) Fetch(id KeyID) (Key, error) {
	s.fetched = append(s.fetched, id)
	if s.refuse != nil {
		return Key{}, s.refuse
	}
	k, ok := s.keys[id]
	if !ok {
		return Key{}, fmt.Errorf("stub: no key for %x", id)
	}
	return k, nil
}

// TestKeyHandleNamesNobody is the rule the whole record turns on.
//
// The ciphertext is permanent, so anything readable beside it is permanent too.
// A handle that could be computed from a subject would be an un-erasable
// identifier for that subject — and confirmable by anyone who could guess it.
func TestKeyHandleNamesNobody(t *testing.T) {
	subjects := []string{
		"alice@example.com", "bob@example.com", "user-1", "user-2",
		"", "a", "entity/42", "Ω-unicode-subject",
	}

	// Two handles drawn for the same subject differ. A stable mapping is the
	// keystore's job; the HANDLE itself must carry no subject at all.
	seen := map[KeyID]bool{}
	const draws = 20000
	for i := 0; i < draws; i++ {
		id, err := NewKeyID()
		if err != nil {
			t.Fatalf("NewKeyID: %v", err)
		}
		if seen[id] {
			t.Fatalf("draw %d repeated a handle; handles are not drawn from a "+
				"cryptographic source", i)
		}
		seen[id] = true
		if id == (KeyID{}) {
			t.Fatal("a handle came out as all zeroes")
		}
	}

	// No handle is any obvious function of any subject. This is the check that
	// would catch somebody "simplifying" allocation into a hash.
	forbidden := map[[KeyIDSize]byte]string{}
	for _, s := range subjects {
		sum := sha256.Sum256([]byte(s))
		var truncated [KeyIDSize]byte
		copy(truncated[:], sum[:KeyIDSize])
		forbidden[truncated] = "sha256(subject)"

		var padded [KeyIDSize]byte
		copy(padded[:], s)
		forbidden[padded] = "the subject's own bytes"
	}
	for id := range seen {
		if how, bad := forbidden[id]; bad {
			t.Fatalf("a drawn handle equals %s; the handle is derived from the subject "+
				"and survives the erasure that was meant to remove it", how)
		}
	}

	// And no handle contains a subject's bytes as a substring.
	for id := range seen {
		for _, s := range subjects {
			if len(s) >= 4 && bytes.Contains(id[:], []byte(s)) {
				t.Fatalf("a handle contains the subject %q", s)
			}
		}
	}
}

// TestEverySealDrawsAFreshNonce checks nonce reuse cannot occur through
// repetition.
//
// ⚠ It seals IDENTICAL plaintext under ONE key. That is the only shape that
// isolates the nonce: with differing plaintexts the ciphertexts would differ
// even under a fixed nonce, and the test would pass while the guarantee was
// gone.
func TestEverySealDrawsAFreshNonce(t *testing.T) {
	s := newStub()
	id, k := s.put(t)
	plaintext := []byte("the same bytes, sealed again and again and again")

	const seals = 5000
	nonces := map[string]bool{}
	ciphertexts := map[string]bool{}
	for i := 0; i < seals; i++ {
		sealed, err := Seal(id, k, plaintext)
		if err != nil {
			t.Fatalf("Seal %d: %v", i, err)
		}
		nonce := string(sealed[KeyIDSize:EnvelopePrefixSize])
		if nonces[nonce] {
			t.Fatalf("seal %d reused a nonce; under one key that is catastrophic for GCM, "+
				"losing confidentiality and integrity together", i)
		}
		nonces[nonce] = true

		body := string(sealed[EnvelopePrefixSize:])
		if ciphertexts[body] {
			t.Fatalf("seal %d produced an identical ciphertext for identical plaintext; "+
				"the nonce is not varying", i)
		}
		ciphertexts[body] = true
	}
}

// TestSealOpenRoundTrips is the property: seal then open returns the original
// bytes, across generated sizes including the empty one.
func TestSealOpenRoundTrips(t *testing.T) {
	s := newStub()
	id, k := s.put(t)
	rng := rand.New(rand.NewSource(7))

	sizes := []int{0, 1, 15, 16, 17, 1024, 4095, 4096}
	for _, n := range sizes {
		plaintext := make([]byte, n)
		rng.Read(plaintext)

		sealed, err := Seal(id, k, plaintext)
		if err != nil {
			t.Fatalf("Seal(%d bytes): %v", n, err)
		}
		if got, want := len(sealed), EnvelopePrefixSize+n+TagSize; got != want {
			t.Errorf("sealing %d bytes produced %d, want %d", n, got, want)
		}

		got, err := Open(s, sealed)
		if err != nil {
			t.Fatalf("Open(%d bytes): %v", n, err)
		}
		if !bytes.Equal(got, plaintext) {
			t.Fatalf("round trip changed %d bytes", n)
		}
	}

	for i := 0; i < 300; i++ {
		plaintext := make([]byte, rng.Intn(8000))
		rng.Read(plaintext)
		sealed, err := Seal(id, k, plaintext)
		if err != nil {
			t.Fatalf("case %d: Seal: %v", i, err)
		}
		got, err := Open(s, sealed)
		if err != nil {
			t.Fatalf("case %d: Open: %v", i, err)
		}
		if !bytes.Equal(got, plaintext) {
			t.Fatalf("case %d: round trip changed %d bytes", i, len(plaintext))
		}
	}
}

// TestOpenResolvesItsKeyThroughTheKeystore checks the keystore is the authority.
//
// Open takes no key argument, so a caller cannot supply one — and when the
// keystore refuses, the read fails even though the key exists and the test holds
// it. That is what makes a shredded subject fail at the same place for everyone.
func TestOpenResolvesItsKeyThroughTheKeystore(t *testing.T) {
	s := newStub()
	id, k := s.put(t)
	plaintext := []byte("readable, until the keystore says otherwise")

	sealed, err := Seal(id, k, plaintext)
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}

	if _, err := Open(s, sealed); err != nil {
		t.Fatalf("Open with the key present: %v", err)
	}
	if len(s.fetched) != 1 {
		t.Fatalf("Open fetched %d times, want 1", len(s.fetched))
	}
	if s.fetched[0] != id {
		t.Errorf("Open fetched %x, want the handle from the envelope, %x", s.fetched[0], id)
	}

	// The keystore refuses. The key still exists and this test still holds it,
	// and the read must fail anyway.
	refusal := errors.New("the keystore says no")
	s.refuse = refusal
	got, err := Open(s, sealed)
	if !errors.Is(err, refusal) {
		t.Fatalf("a refusing keystore: error = %v, want the keystore's own refusal; "+
			"a caller must not be able to read past it", err)
	}
	if got != nil {
		t.Error("plaintext was returned alongside the keystore's refusal")
	}
	s.refuse = nil

	// A handle the keystore does not know fails too, rather than falling back.
	var stranger KeyID
	copy(stranger[:], bytes.Repeat([]byte{0xAB}, KeyIDSize))
	foreign := append([]byte(nil), sealed...)
	copy(foreign[:KeyIDSize], stranger[:])
	if _, err := Open(s, foreign); err == nil {
		t.Error("an envelope naming an unknown handle was opened")
	}
}

// TestUnencryptedBytesAreRefused checks bytes that carry no envelope are refused
// by name rather than read past.
func TestUnencryptedBytesAreRefused(t *testing.T) {
	for _, n := range []int{0, 1, KeyIDSize, EnvelopePrefixSize, MinEnvelopeSize - 1} {
		if _, err := KeyIDOf(make([]byte, n)); !errors.Is(err, ErrNotEncrypted) {
			t.Errorf("%d bytes: error = %v, want ErrNotEncrypted", n, err)
		}
		if _, err := Open(newStub(), make([]byte, n)); !errors.Is(err, ErrNotEncrypted) {
			t.Errorf("Open with %d bytes: error = %v, want ErrNotEncrypted", n, err)
		}
	}

	// The smallest real envelope IS accepted, so the boundary is where it is
	// claimed to be rather than one byte off.
	s := newStub()
	id, k := s.put(t)
	sealed, err := Seal(id, k, nil)
	if err != nil {
		t.Fatalf("Seal(empty): %v", err)
	}
	if len(sealed) != MinEnvelopeSize {
		t.Fatalf("an empty plaintext sealed to %d bytes, want MinEnvelopeSize=%d",
			len(sealed), MinEnvelopeSize)
	}
	if _, err := KeyIDOf(sealed); err != nil {
		t.Errorf("the smallest valid envelope was refused: %v", err)
	}
}

// TestTamperedCiphertextIsRefused checks failure is CLOSED: a flipped bit
// anywhere yields an error, never plausible plaintext.
//
// This matters more here than in most places, because "unreadable" is the
// guarantee this package makes.
func TestTamperedCiphertextIsRefused(t *testing.T) {
	s := newStub()
	id, k := s.put(t)
	plaintext := []byte("authenticated, so tampering is detected rather than decoded")

	sealed, err := Seal(id, k, plaintext)
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}
	if _, err := Open(s, sealed); err != nil {
		t.Fatalf("the intact envelope was refused: %v", err)
	}

	for i := range sealed {
		rotten := append([]byte(nil), sealed...)
		rotten[i] ^= 0x01
		got, err := Open(s, rotten)
		if err == nil {
			t.Fatalf("a flipped bit at byte %d of %d was not detected", i, len(sealed))
		}
		if got != nil {
			t.Fatalf("byte %d: plaintext was returned alongside the error", i)
		}
	}

	// A truncated envelope, and one whose tag is dropped entirely.
	if _, err := Open(s, sealed[:len(sealed)-1]); err == nil {
		t.Error("a truncated envelope was opened")
	}
	if _, err := Open(s, sealed[:MinEnvelopeSize]); err == nil {
		t.Error("an envelope with its body removed was opened")
	}

	// A different key over the same envelope fails, rather than producing
	// plausible bytes.
	wrongKey, err := NewKey()
	if err != nil {
		t.Fatalf("NewKey: %v", err)
	}
	other := newStub()
	other.keys[id] = wrongKey // the envelope's own handle, a different key behind it
	if _, err := Open(other, sealed); err == nil {
		t.Error("an envelope opened under the wrong key")
	}
}
