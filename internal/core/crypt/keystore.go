package crypt

import (
	"errors"
	"fmt"
	"sync"
	"time"
)

var (
	// ErrKeyDestroyed reports that the subject was erased. The ciphertext still
	// exists and no longer means anything.
	ErrKeyDestroyed = errors.New("crypt: the key was destroyed; the subject was erased")

	// ErrUnknownKey reports a handle that was never issued.
	//
	// ⚠ It is deliberately distinct from [ErrKeyDestroyed]. An operator proving
	// compliance needs to tell "erased" from "never existed", and the handle
	// names nobody — but the distinction IS information, and it confirms that a
	// handle once existed. Any external surface must weigh that before exposing
	// it.
	ErrUnknownKey = errors.New("crypt: no key was ever issued for that handle")
)

// ShredRecord is the audit trail of one destruction.
//
// ⚠ It has three fields and must keep exactly three. There is no subject here,
// and none may be added: the point of the whole record is that nothing durable
// binds an identity to the permanent ciphertext, and an audit trail is durable.
// Proving "this person was erased" is done through the requester's own
// reference, held outside this system.
type ShredRecord struct {
	// KeyID is the handle that was destroyed. It names nobody.
	KeyID KeyID
	// At is when, in Unix nanoseconds.
	At int64
	// Request is the erasure request's reference, as supplied by the caller.
	// It is an opaque string this package never interprets.
	Request string
}

// MemoryKeystore holds keys in memory.
//
// ⚠ Its NAME is the warning, and the name is part of the decision. In memory
// means every key is lost on restart, which erases everything — safe in the
// wrong direction and a catastrophe in production. Where a keystore really
// lives, how it is made durable, and how it is rotated are open questions
// deliberately larger than this type.
type MemoryKeystore struct {
	mu        sync.RWMutex
	keys      map[KeyID]Key
	bySubject map[string]KeyID
	destroyed map[KeyID]bool
	shreds    []ShredRecord

	// now is injected so a test can assert on the recorded instant without
	// racing a real clock.
	now func() int64
}

// NewMemoryKeystore returns an empty keystore.
func NewMemoryKeystore() *MemoryKeystore {
	return &MemoryKeystore{
		keys:      map[KeyID]Key{},
		bySubject: map[string]KeyID{},
		destroyed: map[KeyID]bool{},
		now:       func() int64 { return time.Now().UnixNano() },
	}
}

// Allocate returns the subject's handle, creating one if it has none.
//
// ★ It is STABLE: one subject keeps one handle for as long as it is not
// destroyed, so every block that subject writes shares a key and they are all
// erased together. A fresh handle per block would make erasure a sweep over
// everything the subject ever touched, which is the enumeration problem
// crypto-shredding exists to avoid.
//
// After a destruction the subject is unknown, so allocating again issues a NEW
// handle — which cannot read anything written under the old one.
func (m *MemoryKeystore) Allocate(subject string) (KeyID, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if id, ok := m.bySubject[subject]; ok {
		return id, nil
	}

	id, err := NewKeyID()
	if err != nil {
		return KeyID{}, err
	}
	k, err := NewKey()
	if err != nil {
		return KeyID{}, err
	}
	m.keys[id] = k
	m.bySubject[subject] = id
	return id, nil
}

// Fetch returns the key for a handle.
func (m *MemoryKeystore) Fetch(id KeyID) (Key, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if m.destroyed[id] {
		return Key{}, fmt.Errorf("%w: %x", ErrKeyDestroyed, id)
	}
	k, ok := m.keys[id]
	if !ok {
		return Key{}, fmt.Errorf("%w: %x", ErrUnknownKey, id)
	}
	return k, nil
}

// Resolve maps a subject to its handle.
//
// After destruction the subject is not known, because the mapping is destroyed
// with the key.
func (m *MemoryKeystore) Resolve(subject string) (KeyID, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	id, ok := m.bySubject[subject]
	return id, ok
}

// Destroy irreversibly removes the key and the subject mapping.
func (m *MemoryKeystore) Destroy(id KeyID) error {
	_, err := m.Shred(id, "")
	return err
}

// Shred destroys a key and records the destruction against a request reference.
//
// ★ It removes the SUBJECT MAPPING as well as the key, in the same operation.
// Removing only the key would leave a durable record binding a handle to a
// subject — and the handle sits beside ciphertext that lasts forever, so that
// record would be the un-erasable identifier this whole design exists to
// prevent.
//
// It is idempotent and final. A second call succeeds and nothing resurrects a
// key, because a destruction that could be undone would not be an erasure.
func (m *MemoryKeystore) Shred(id KeyID, request string) (ShredRecord, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	rec := ShredRecord{KeyID: id, At: m.now(), Request: request}

	if m.destroyed[id] {
		// Already gone. Recording the second request is honest — somebody asked
		// — and it changes nothing about the key.
		m.shreds = append(m.shreds, rec)
		return rec, nil
	}

	delete(m.keys, id)
	for subject, held := range m.bySubject {
		if held == id {
			delete(m.bySubject, subject)
		}
	}
	m.destroyed[id] = true
	m.shreds = append(m.shreds, rec)
	return rec, nil
}

// Shreds returns the audit trail, oldest first.
func (m *MemoryKeystore) Shreds() []ShredRecord {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]ShredRecord, len(m.shreds))
	copy(out, m.shreds)
	return out
}
