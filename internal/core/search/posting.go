package search

import (
	"encoding/binary"
	"errors"
	"fmt"

	"github.com/atvirokodosprendimai/sdev1/internal/core/crypt"
	"github.com/atvirokodosprendimai/sdev1/internal/core/tx"
)

// ErrNoLimit reports a search asked for without a bound.
//
// ★ A ranked search with no limit is a full scan with extra steps. The limit is
// required rather than defaulted, because a default bound is a number nobody
// wrote down that silently decides how much of the cluster a query touches.
var ErrNoLimit = errors.New("search: a search needs a positive limit")

// ErrMalformedPosting reports bytes that opened but are not a posting.
var ErrMalformedPosting = errors.New("search: the plaintext is not a posting")

// Posting is one subject's occurrence of a term, and the span of transaction
// time over which it held.
//
// ⚠ The range is a field rather than an afterthought. An index that reflected
// only the present would make search the one surface unable to answer "as of",
// in a store whose entire premise is that it can. The price is that postings
// accumulate with history rather than with data.
type Posting struct {
	// Subject is the entity the term was found in.
	Subject string
	// Term is the analysed token. See [Analyze].
	Term Term
	// From is the transaction that asserted it.
	From tx.TxID
	// Until is the transaction that retracted it, or nil while it still holds.
	Until *tx.TxID
}

// Sealed is a posting encrypted with its subject's own key.
//
// ★ This type is why erasure reaches the index. Destroying the subject's key
// makes every one of its sealed postings undecryptable at once — in the live
// index, in every replica, and in every backup of the index — with nothing
// having to go and find them.
type Sealed []byte

// Seal encrypts a posting under its subject's key, allocating the subject a
// handle if it has none.
func Seal(ks crypt.Keystore, p Posting) (Sealed, error) {
	id, err := ks.Allocate(p.Subject)
	if err != nil {
		return nil, fmt.Errorf("search: allocate key for subject: %w", err)
	}
	key, err := ks.Fetch(id)
	if err != nil {
		return nil, fmt.Errorf("search: fetch key: %w", err)
	}
	sealed, err := crypt.Seal(id, key, encodePosting(p))
	if err != nil {
		return nil, fmt.Errorf("search: seal posting: %w", err)
	}
	return Sealed(sealed), nil
}

// Open decrypts one sealed posting.
//
// It returns the keystore's own error unchanged when the key is gone, so a
// caller that genuinely needs to distinguish "erased" from "never issued" can —
// but note that [Visible], the path a SEARCH uses, deliberately cannot.
func Open(ks crypt.Keystore, s Sealed) (Posting, error) {
	plain, err := crypt.Open(ks, []byte(s))
	if err != nil {
		return Posting{}, err
	}
	return decodePosting(plain)
}

// Visible returns the postings a caller can actually see, silently dropping
// every one that does not open.
//
// ⚠ IT RETURNS NO COUNT OF WHAT IT DROPPED, and the signature is the reason it
// cannot. A caller told "3 results withheld" holds an oracle for the existence of
// erased subjects, which is exactly the property crypto-shredding exists to
// remove — and incrementing a counter when a decrypt fails is the natural thing
// to write inside this loop.
//
// ⚠ A dropped posting is also not an error. Returning one would make an erased
// subject observable through the error path instead of the result path, which is
// the same disclosure wearing a different hat.
func Visible(ks crypt.Keystore, sealed []Sealed) []Posting {
	var out []Posting
	for _, s := range sealed {
		p, err := Open(ks, s)
		if err != nil {
			continue
		}
		out = append(out, p)
	}
	return out
}

// Match returns at most limit postings from those the caller can see.
//
// Ranking is not decided here — it needs a corpus to be chosen against — so the
// order is the order given. What this function fixes is that a search is BOUNDED
// at all.
func Match(ks crypt.Keystore, sealed []Sealed, limit int) ([]Posting, error) {
	if limit <= 0 {
		return nil, fmt.Errorf("%w: got %d", ErrNoLimit, limit)
	}
	visible := Visible(ks, sealed)
	if len(visible) > limit {
		visible = visible[:limit]
	}
	return visible, nil
}

// encodePosting writes a posting as bytes.
//
// Deliberately explicit rather than reflective: this is what gets encrypted, and
// a format that changes when a struct field is reordered would silently make
// every existing posting unreadable.
func encodePosting(p Posting) []byte {
	from := p.From.Encode()

	buf := make([]byte, 0, 4+len(p.Subject)+len(p.Term)+2*tx.EncodedSize+1)
	buf = binary.BigEndian.AppendUint16(buf, uint16(len(p.Subject)))
	buf = append(buf, p.Subject...)
	buf = binary.BigEndian.AppendUint16(buf, uint16(len(p.Term)))
	buf = append(buf, p.Term...)
	buf = append(buf, from[:]...)
	if p.Until == nil {
		return append(buf, 0)
	}
	until := p.Until.Encode()
	buf = append(buf, 1)
	return append(buf, until[:]...)
}

// decodePosting reads a posting back, refusing anything short.
func decodePosting(b []byte) (Posting, error) {
	var p Posting

	read := func(n int) ([]byte, bool) {
		if len(b) < n {
			return nil, false
		}
		out := b[:n]
		b = b[n:]
		return out, true
	}

	head, ok := read(2)
	if !ok {
		return p, ErrMalformedPosting
	}
	subject, ok := read(int(binary.BigEndian.Uint16(head)))
	if !ok {
		return p, ErrMalformedPosting
	}
	p.Subject = string(subject)

	head, ok = read(2)
	if !ok {
		return p, ErrMalformedPosting
	}
	term, ok := read(int(binary.BigEndian.Uint16(head)))
	if !ok {
		return p, ErrMalformedPosting
	}
	p.Term = Term(term)

	raw, ok := read(tx.EncodedSize)
	if !ok {
		return p, ErrMalformedPosting
	}
	from, err := tx.DecodeSlice(raw)
	if err != nil {
		return p, fmt.Errorf("%w: %v", ErrMalformedPosting, err)
	}
	p.From = from

	flag, ok := read(1)
	if !ok {
		return p, ErrMalformedPosting
	}
	if flag[0] == 0 {
		return p, nil
	}
	raw, ok = read(tx.EncodedSize)
	if !ok {
		return p, ErrMalformedPosting
	}
	until, err := tx.DecodeSlice(raw)
	if err != nil {
		return p, fmt.Errorf("%w: %v", ErrMalformedPosting, err)
	}
	p.Until = &until
	return p, nil
}
