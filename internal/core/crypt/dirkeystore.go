package crypt

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// ErrNotDeletable reports a directory a keystore cannot write to or remove from.
//
// ⚠ It is refused at OPEN rather than discovered at destroy. A keystore that
// finds out it cannot delete at the moment somebody exercises their erasure right
// has already failed.
var ErrNotDeletable = errors.New("crypt: the keystore directory cannot be written to or removed from")

// keyFileSize is a key file's fixed prefix: the key, then the subject's length.
const keyFileSize = KeySize + 2

// DirKeystore holds keys in a directory, one file per key.
//
// ★ A key file holds the key AND its subject. ADR-007 requires the subject
// mapping to be destroyed with the key, and this makes that a property of the
// filesystem: one unlink removes both, so there is no state in which a key exists
// without its mapping.
//
// ⚠ THIS MUST NOT LIVE ON THIS SYSTEM'S OWN STORAGE. A sealed segment is
// immutable (ADR-024) and compaction copies every fact forward (ADR-029), so a
// key written to a leaf could never be destroyed — and every test in this
// repository would still pass.
//
// ⚠ AND IT MUST NEVER SHARE A BACKUP, SNAPSHOT OR REPLICA WITH THE DATA.
// Restoring one that holds both puts the key back beside its ciphertext and
// undoes every erasure it contains. That is a retention decision no code here can
// hold, and it is the single easiest way to get crypto-shredding wrong.
//
// ⚠ Removing a file does not destroy the bytes on the medium. A journal, a
// copy-on-write snapshot or a flash translation layer may keep them; media-level
// destruction needs full-disk encryption or an overwriting store.
type DirKeystore struct {
	dir string

	mu sync.RWMutex
	// cache holds keys already read.
	//
	// ⚠ It is evicted INSIDE Destroy, under this same lock, before the destroy
	// returns. A cached key that outlived its destruction would leave a shredded
	// subject readable wherever it was cached, and "eventually" is not good
	// enough for an erasure.
	cache  map[KeyID]Key
	shreds []ShredRecord
}

var _ Keystore = (*DirKeystore)(nil)

// OpenDirKeystore opens or creates a keystore in dir.
//
// It refuses a directory it cannot write to or remove from, because a keystore
// that cannot delete is not a keystore — it is a place erasure silently fails.
func OpenDirKeystore(dir string) (*DirKeystore, error) {
	if err := os.MkdirAll(filepath.Join(dir, "subjects"), 0o700); err != nil {
		return nil, fmt.Errorf("%w: %s: %v", ErrNotDeletable, dir, err)
	}

	// Prove BOTH capabilities now. Writing is not enough: a directory can be
	// writable and immutable-append, and the failure would then surface at the
	// worst possible moment.
	probe := filepath.Join(dir, ".deletable-probe")
	if err := os.WriteFile(probe, []byte("probe"), 0o600); err != nil {
		return nil, fmt.Errorf("%w: %s cannot be written: %v", ErrNotDeletable, dir, err)
	}
	if err := os.Remove(probe); err != nil {
		return nil, fmt.Errorf("%w: %s cannot be removed from: %v", ErrNotDeletable, dir, err)
	}

	return &DirKeystore{dir: dir, cache: make(map[KeyID]Key)}, nil
}

func (d *DirKeystore) keyPath(id KeyID) string {
	return filepath.Join(d.dir, hex.EncodeToString(id[:])+".key")
}

// subjectPath names the index entry for a subject.
//
// The name is a hash rather than the subject itself: a subject may contain any
// byte, and a filename may not.
func (d *DirKeystore) subjectPath(subject string) string {
	sum := sha256.Sum256([]byte(subject))
	return filepath.Join(d.dir, "subjects", hex.EncodeToString(sum[:]))
}

// Allocate returns the subject's handle, creating one if it has none.
func (d *DirKeystore) Allocate(subject string) (KeyID, error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	if id, held, err := d.resolveLocked(subject); err != nil {
		return KeyID{}, err
	} else if held {
		return id, nil
	}

	id, err := NewKeyID()
	if err != nil {
		return KeyID{}, err
	}
	key, err := NewKey()
	if err != nil {
		return KeyID{}, err
	}

	// The key file first: it holds the mapping, so it is the thing that must
	// exist for the index entry to mean anything.
	body := make([]byte, 0, keyFileSize+len(subject))
	body = append(body, key[:]...)
	body = append(body, byte(len(subject)>>8), byte(len(subject)))
	body = append(body, subject...)
	if err := os.WriteFile(d.keyPath(id), body, 0o600); err != nil {
		return KeyID{}, fmt.Errorf("crypt: writing a key: %w", err)
	}
	if err := os.WriteFile(d.subjectPath(subject), id[:], 0o600); err != nil {
		return KeyID{}, fmt.Errorf("crypt: writing a subject index entry: %w", err)
	}

	d.cache[id] = key
	return id, nil
}

// Fetch returns the key for a handle, or reports that it was destroyed.
func (d *DirKeystore) Fetch(id KeyID) (Key, error) {
	d.mu.RLock()
	if key, cached := d.cache[id]; cached {
		d.mu.RUnlock()
		return key, nil
	}
	d.mu.RUnlock()

	key, _, err := d.readKey(id)
	if err != nil {
		return Key{}, err
	}
	d.mu.Lock()
	d.cache[id] = key
	d.mu.Unlock()
	return key, nil
}

// readKey reads a key file, returning the key and the subject it belongs to.
//
// ⚠ A missing file is [ErrKeyDestroyed] rather than a filesystem error: a handle
// that was issued and whose file is gone IS an erased subject, and that is what a
// caller needs to be told.
func (d *DirKeystore) readKey(id KeyID) (Key, string, error) {
	body, err := os.ReadFile(d.keyPath(id))
	if errors.Is(err, os.ErrNotExist) {
		return Key{}, "", ErrKeyDestroyed
	}
	if err != nil {
		return Key{}, "", fmt.Errorf("crypt: reading a key: %w", err)
	}
	if len(body) < keyFileSize {
		return Key{}, "", fmt.Errorf("crypt: key file for %x is %d bytes, too few",
			id[:], len(body))
	}

	var key Key
	copy(key[:], body[:KeySize])
	length := int(body[KeySize])<<8 | int(body[KeySize+1])
	if len(body) != keyFileSize+length {
		return Key{}, "", fmt.Errorf("crypt: key file for %x holds %d bytes for a %d-byte subject",
			id[:], len(body)-keyFileSize, length)
	}
	return key, string(body[keyFileSize:]), nil
}

// Resolve maps a subject to its handle.
//
// ⚠ An index entry whose key file is gone reports UNKNOWN. That is the litter a
// crash mid-destroy leaves, and treating it as a live subject would make an
// erased subject look present.
func (d *DirKeystore) Resolve(subject string) (KeyID, bool) {
	d.mu.RLock()
	defer d.mu.RUnlock()
	id, held, err := d.resolveLocked(subject)
	if err != nil {
		return KeyID{}, false
	}
	return id, held
}

func (d *DirKeystore) resolveLocked(subject string) (KeyID, bool, error) {
	raw, err := os.ReadFile(d.subjectPath(subject))
	if errors.Is(err, os.ErrNotExist) {
		return KeyID{}, false, nil
	}
	if err != nil {
		return KeyID{}, false, fmt.Errorf("crypt: reading a subject index entry: %w", err)
	}
	if len(raw) != KeyIDSize {
		return KeyID{}, false, nil
	}
	var id KeyID
	copy(id[:], raw)

	// The key file is the authority. An entry pointing at a destroyed handle is
	// litter, not a subject.
	if _, err := os.Stat(d.keyPath(id)); err != nil {
		return KeyID{}, false, nil
	}
	return id, true, nil
}

// Destroy irreversibly removes the key AND the subject mapping.
//
// ⚠ The KEY FILE goes first and the index entry second, and the orders are not
// symmetric. A crash after the key is gone leaves an entry pointing at a
// destroyed handle — Fetch fails, the subject is erased, and the entry is litter.
// A crash the other way leaves a READABLE key that nothing resolves to, which is
// not erased at all while looking erased.
//
// ⚠ The cache is evicted before this returns, under the same lock. An erasure
// that is true eventually is not an erasure.
func (d *DirKeystore) Destroy(id KeyID) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	// Read only for the SUBJECT: the key file is where the mapping lives, so the
	// index entry to remove cannot be known without it.
	_, subject, err := d.readKey(id)
	if errors.Is(err, ErrKeyDestroyed) {
		// Already gone. Still evict, in case a cache outlived a destroy done by
		// somebody else on the same directory.
		delete(d.cache, id)
		return ErrKeyDestroyed
	}
	if err != nil {
		return err
	}

	if err := os.Remove(d.keyPath(id)); err != nil {
		return fmt.Errorf("crypt: destroying a key: %w", err)
	}
	// ⚠ Evicted here, between the two removals rather than after both, so no
	// path through this function returns with the key still cached.
	delete(d.cache, id)

	if err := os.Remove(d.subjectPath(subject)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("crypt: destroying a subject index entry: %w", err)
	}
	return nil
}

// Shred destroys a key and records that it happened.
//
// The record names the handle and never the subject: nothing durable may bind an
// identity to the permanent ciphertext.
func (d *DirKeystore) Shred(id KeyID, request string) (ShredRecord, error) {
	if err := d.Destroy(id); err != nil {
		return ShredRecord{}, err
	}
	record := ShredRecord{KeyID: id, At: time.Now().UnixNano(), Request: request}

	d.mu.Lock()
	d.shreds = append(d.shreds, record)
	d.mu.Unlock()
	return record, nil
}

// Shreds returns the destructions this keystore performed, in order.
//
// ⚠ In THIS PROCESS. The audit trail is not durable, deliberately: a durable
// trail is durable state binding a handle to a moment, and where that may live is
// the same question as where the keystore may be backed up to.
func (d *DirKeystore) Shreds() []ShredRecord {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return append([]ShredRecord(nil), d.shreds...)
}
