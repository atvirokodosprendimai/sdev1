package crypt

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"fmt"
)

// Seal encrypts plaintext under k and returns the envelope:
//
//	handle ‖ nonce ‖ ciphertext‖tag
//
// ★ The envelope PREFIXES the ciphertext rather than widening a block header.
// The segment format put a per-block cipher identifier in its header precisely
// so the cipher could be decided later without a format change, so nothing
// already written is reinterpreted and no format version is spent here.
//
// ⚠ A fresh nonce is drawn on every call. Nonce reuse under one key is
// catastrophic for GCM — it loses confidentiality and integrity together — and a
// deterministic nonce derived from a block's position would make safety depend
// on nothing ever re-encoding a block at the same coordinates, which is an
// assumption about code not yet written.
//
// The handle is passed as additional authenticated data, so an envelope whose
// handle was swapped fails instead of being decrypted under some other subject's
// key.
func Seal(id KeyID, k Key, plaintext []byte) ([]byte, error) {
	gcm, err := newGCM(k)
	if err != nil {
		return nil, err
	}

	nonce := make([]byte, NonceSize)
	if _, err := rand.Read(nonce); err != nil {
		return nil, fmt.Errorf("crypt: drawing a nonce: %w", err)
	}

	out := make([]byte, 0, EnvelopePrefixSize+len(plaintext)+TagSize)
	out = append(out, id[:]...)
	out = append(out, nonce...)
	return gcm.Seal(out, nonce, plaintext, id[:]), nil
}

// Open decrypts an envelope, resolving its key through the keystore.
//
// ★ It takes NO key. The envelope says which key is needed; the keystore says
// whether it may still be had. That is what makes a shredded subject fail at the
// same place for every caller, rather than at whichever place each caller
// happened to check.
//
// A destroyed key, a wrong key, a flipped bit and a swapped handle all produce
// an error and never plausible plaintext, because the cipher is authenticated.
func Open(ks Keystore, sealed []byte) ([]byte, error) {
	id, err := KeyIDOf(sealed)
	if err != nil {
		return nil, err
	}

	// The keystore's refusal travels out unwrapped in kind, so a caller can test
	// for a destroyed key rather than parsing a message.
	k, err := ks.Fetch(id)
	if err != nil {
		return nil, err
	}

	gcm, err := newGCM(k)
	if err != nil {
		return nil, err
	}

	nonce := sealed[KeyIDSize:EnvelopePrefixSize]
	body := sealed[EnvelopePrefixSize:]
	plaintext, err := gcm.Open(nil, nonce, body, id[:])
	if err != nil {
		return nil, fmt.Errorf("crypt: opening the envelope: %w", err)
	}
	return plaintext, nil
}

// KeyIDOf reads the handle from an envelope without decrypting it, so a caller
// can find the key it needs before deciding to read.
func KeyIDOf(sealed []byte) (KeyID, error) {
	if len(sealed) < MinEnvelopeSize {
		return KeyID{}, fmt.Errorf("%w: %d bytes, and the smallest envelope is %d",
			ErrNotEncrypted, len(sealed), MinEnvelopeSize)
	}
	var id KeyID
	copy(id[:], sealed[:KeyIDSize])
	return id, nil
}

func newGCM(k Key) (cipher.AEAD, error) {
	block, err := aes.NewCipher(k[:])
	if err != nil {
		return nil, fmt.Errorf("crypt: building the cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("crypt: building GCM: %w", err)
	}
	return gcm, nil
}
