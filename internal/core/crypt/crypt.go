package crypt

import (
	"crypto/rand"
	"errors"
	"fmt"

	"github.com/atvirokodosprendimai/sdev1/internal/core/segment"
)

const (
	// KeyIDSize is the width of a key handle.
	KeyIDSize = 16
	// KeySize is 256 bits, for AES-256.
	KeySize = 32
	// NonceSize is GCM's standard nonce width.
	NonceSize = 12
	// TagSize is GCM's authentication tag width.
	TagSize = 16
)

// EnvelopePrefixSize is the fixed width of what precedes the ciphertext: the key
// handle, then the nonce.
const EnvelopePrefixSize = KeyIDSize + NonceSize

// MinEnvelopeSize is the smallest possible sealed value — prefix plus the
// authentication tag over an empty plaintext.
const MinEnvelopeSize = EnvelopePrefixSize + TagSize

// CipherAES256GCM is the [segment.CipherID] that claims this envelope layout.
//
// ★ It is defined here rather than in the segment package on purpose. The
// segment format RECORDS which cipher a block used; it does not need to know
// what any of them mean. Putting the constant here keeps the format ignorant of
// cryptography, which is what let this record land without spending a format
// version.
const CipherAES256GCM = segment.CipherID(1)

// KeyID is an opaque handle for a key.
//
// ⚠ It is ALLOCATED, never derived. Nothing about a subject can be recovered
// from it, because the ciphertext it sits beside is permanent and anything
// readable there is permanent too. A handle computed from an entity identifier
// would be an un-erasable label for that identifier — and confirmable by anyone
// who could guess it.
type KeyID [KeyIDSize]byte

// Key is a 256-bit encryption key. It is never persisted beside a ciphertext.
type Key [KeySize]byte

// NewKeyID draws a fresh handle.
//
// ★ It takes NO arguments, and that is the design rather than an omission: a
// function that cannot see the subject cannot derive from it.
func NewKeyID() (KeyID, error) {
	var id KeyID
	if _, err := rand.Read(id[:]); err != nil {
		return KeyID{}, fmt.Errorf("crypt: drawing a key handle: %w", err)
	}
	return id, nil
}

// NewKey draws a fresh key.
func NewKey() (Key, error) {
	var k Key
	if _, err := rand.Read(k[:]); err != nil {
		return Key{}, fmt.Errorf("crypt: drawing a key: %w", err)
	}
	return k, nil
}

// ErrNotEncrypted reports bytes that do not carry an envelope.
var ErrNotEncrypted = errors.New("crypt: the bytes carry no envelope")

// Keystore holds keys and the subject-to-handle mapping, and is the only
// authority on whether a subject is still readable.
//
// ★ It is declared here, where it is CONSUMED by [Open], and implemented
// elsewhere. That is why [Open] takes a keystore rather than a key: a caller
// that could supply a key would be a second authority on whether a shredded
// subject can be read, and there would be as many authorities as callers.
type Keystore interface {
	// Allocate returns the subject's handle, creating one if it has none.
	// It is stable: one subject has one handle for as long as it is not
	// destroyed, so a subject's blocks share a key and are erased together.
	Allocate(subject string) (KeyID, error)

	// Fetch returns the key for a handle, or reports that it was destroyed.
	Fetch(id KeyID) (Key, error)

	// Resolve maps a subject to its handle. After destruction the subject is
	// not known, because the mapping is destroyed with the key.
	Resolve(subject string) (KeyID, bool)

	// Destroy irreversibly removes the key AND the subject mapping.
	Destroy(id KeyID) error
}
