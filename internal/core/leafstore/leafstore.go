package leafstore

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/atvirokodosprendimai/sdev1/internal/core/addr"
	"github.com/atvirokodosprendimai/sdev1/internal/core/datom"
	"github.com/atvirokodosprendimai/sdev1/internal/core/ports"
	"github.com/atvirokodosprendimai/sdev1/internal/core/segment"
	"github.com/atvirokodosprendimai/sdev1/internal/core/segstore"
	"github.com/atvirokodosprendimai/sdev1/internal/core/temporal"
	"github.com/atvirokodosprendimai/sdev1/internal/core/tx"
)

// Extension is the suffix a sealed segment carries.
//
// ⚠ It is the only thing a segment's name says. The rest is random — see [Store].
const Extension = ".seg"

var (
	// ErrNoSnapshot reports a read with a zero [ports.Snapshot].
	//
	// ⚠ A zero transaction identifier bounds the system axis at before-anything,
	// so every fact is invisible and the read returns nothing at all — which is
	// indistinguishable from an entity that has no facts. "You asked as of the
	// beginning of time" and "there is nothing here" are different answers, and
	// the first is always a bug.
	ErrNoSnapshot = errors.New("leafstore: read has no snapshot")

	// ErrClosed reports use after [Store.Close].
	ErrClosed = errors.New("leafstore: store is closed")
)

// Store is one leaf: a directory of sealed segments plus an unsealed tail.
//
// It implements [ports.Store]. Safe for concurrent use.
//
// ★ A segment's name is random and means NOTHING. Nothing here reads it, sorts by
// it, or derives anything from it — a name that sorted would be a name something
// could come to depend on, and the merge would then be true only until somebody
// wrote the loop that broke it.
type Store struct {
	dir  string
	leaf addr.LeafID

	// mu guards the tail and the open segments together.
	//
	// ⚠ Together is the point: [Store.Seal] publishes a segment and clears the
	// tail, and a read that fell between them would see the same fact in both
	// places. A duplicated datom is not an obvious error — it is a fact that looks
	// asserted twice.
	mu       sync.RWMutex
	segments []*segstore.Reader
	// segmentFiles are the file names of segments, positionally matching
	// segments. ⚠ Kept only so a compaction can REMOVE the inputs it merged —
	// nothing reads a name to decide anything, which is rule 2's whole point.
	segmentFiles []string
	tail         []ports.Datom
	closed       bool
}

// compile-time proof that a leaf is exactly what a read model may be handed.
var _ ports.Store = (*Store)(nil)

// Open reads a leaf directory, creating it if it is not there.
//
// Every file ending in [Extension] is opened as a segment. ⚠ Dot-prefixed names
// are skipped: that is what ADR-024 calls a partial write, and opening one turns a
// crash that was safely survivable into a failure at start-up.
func Open(dir string, leaf addr.LeafID) (*Store, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("leafstore: opening %s: %w", dir, err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("leafstore: listing %s: %w", dir, err)
	}

	s := &Store{dir: dir, leaf: leaf}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || strings.HasPrefix(name, ".") || !strings.HasSuffix(name, Extension) {
			continue
		}
		r, err := segstore.Open(filepath.Join(dir, name))
		if err != nil {
			_ = s.Close()
			return nil, fmt.Errorf("leafstore: opening segment %s: %w", name, err)
		}
		s.segments = append(s.segments, r)
		s.segmentFiles = append(s.segmentFiles, name)
	}
	return s, nil
}

// Append adds datoms to the tail.
//
// ⚠ It touches no disk. ADR-020 fixed the commit point at N memory replicas in
// distinct failure domains, so a segment is a durability tier and NOT the commit
// path — flushing here would move the commit point and the latency with it, as a
// side effect of a storage decision.
func (s *Store) Append(_ context.Context, datoms ...ports.Datom) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return ErrClosed
	}
	s.tail = append(s.tail, datoms...)
	return nil
}

// Seal writes the tail into one new segment and empties it.
//
// Sealing an empty tail writes nothing: an empty segment is a file every later
// read has to open in order to learn it holds nothing.
//
// ⚠ Publishing and clearing happen under one exclusive hold. Between a rename and
// a separate clear, a read sees each sealed fact twice.
func (s *Store) Seal(_ context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return ErrClosed
	}
	if len(s.tail) == 0 {
		return nil
	}

	// One block per entity, which is what makes a read fetch one block per segment
	// rather than scan it.
	byEntity := make(map[string][]ports.Datom)
	for _, d := range s.tail {
		byEntity[d.Entity] = append(byEntity[d.Entity], d)
	}
	keys := make([]string, 0, len(byEntity))
	for k := range byEntity {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	name, err := segmentName()
	if err != nil {
		return err
	}
	path := filepath.Join(s.dir, name)

	w, err := segstore.Create(path, s.leaf)
	if err != nil {
		return fmt.Errorf("leafstore: sealing: %w", err)
	}
	defer func() { _ = w.Abort() }()

	for _, entity := range keys {
		raw, err := datom.Encode(byEntity[entity])
		if err != nil {
			return fmt.Errorf("leafstore: encoding %q: %w", entity, err)
		}
		if err := w.Append(entity, raw, segment.CodecZstd); err != nil {
			return fmt.Errorf("leafstore: writing %q: %w", entity, err)
		}
	}
	if err := w.Seal(); err != nil {
		return fmt.Errorf("leafstore: sealing: %w", err)
	}

	r, err := segstore.Open(path)
	if err != nil {
		return fmt.Errorf("leafstore: reopening the segment just sealed: %w", err)
	}
	s.segments = append(s.segments, r)
	s.segmentFiles = append(s.segmentFiles, name)
	s.tail = nil
	return nil
}

// segmentName returns a name that says nothing.
//
// ★ Deliberately unordered and unparseable. `BACKLOG.md` §12 wrote the trap down
// before there was anything to trap: whatever names a segment file must not encode
// anything a reader needs in order to interpret it. A name that cannot be ordered
// cannot be depended on for order.
func segmentName() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("leafstore: naming a segment: %w", err)
	}
	return hex.EncodeToString(b[:]) + Extension, nil
}

// History returns every datom the leaf holds for one entity, ordered by
// transaction.
//
// ⚠ Ordered by the datoms' own identifiers, NEVER by the order segments were
// opened. That order is a property of the filenames, and the filenames mean
// nothing on purpose.
//
// It is the primitive [Load] filters. A caller rebuilding state wants this rather
// than Load: no snapshot returns all of history, because an instant on the
// business axis selects the facts true AT it.
func (s *Store) History(entity string) ([]ports.Datom, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.closed {
		return nil, ErrClosed
	}
	return s.historyLocked(entity)
}

func (s *Store) historyLocked(entity string) ([]ports.Datom, error) {
	var out []ports.Datom
	for _, seg := range s.segments {
		raw, err := seg.Get(entity)
		if err != nil {
			// ⚠ The ONE place this refusal is translated. At the block layer a
			// missing key is exceptional and rightly named; here it is the common
			// case, because most segments hold most entities not at all. Every
			// other error propagates — a refusal swallowed in a loop is how a real
			// read failure becomes an empty answer.
			if isMissingBlock(err) {
				continue
			}
			return nil, fmt.Errorf("leafstore: reading %q: %w", entity, err)
		}
		run, err := datom.Decode(raw)
		if err != nil {
			return nil, fmt.Errorf("leafstore: decoding %q: %w", entity, err)
		}
		out = append(out, run...)
	}
	for _, d := range s.tail {
		if d.Entity == entity {
			out = append(out, d)
		}
	}

	sortByTransaction(out)
	// ⚠ A datom can legitimately appear in more than one segment: a compaction
	// publishes its output before removing its inputs, and a crash between the two
	// leaves the overlap on disk permanently. Without this every later read of
	// that leaf would count each datom twice. See ADR-029.
	return deduplicate(out), nil
}

// isMissingBlock reports the one error a leaf translates rather than propagates.
//
// ⚠ At the block layer a missing key is exceptional and rightly named; here it is
// the common case, because most segments hold most entities not at all. Every
// other error must come out — a refusal swallowed in a loop is how a real read
// failure becomes an empty answer.
func isMissingBlock(err error) bool { return errors.Is(err, segstore.ErrNoSuchBlock) }

// sortByTransaction orders datoms by their own identifiers.
//
// ⚠ By transaction, NEVER by the order segments were opened. That order is a
// property of the filenames, and the filenames mean nothing on purpose.
func sortByTransaction(datoms []ports.Datom) {
	sort.SliceStable(datoms, func(i, j int) bool {
		return datoms[i].TxID.Compare(datoms[j].TxID) < 0
	})
}

// Load returns the datoms of one entity visible at a snapshot.
//
// It is [History] filtered by ADR-002's visibility predicate — one gathering and
// one filter, so the snapshot cannot be applied on one read path and forgotten on
// another.
func (s *Store) Load(_ context.Context, entity string, at ports.Snapshot) ([]ports.Datom, error) {
	q, err := query(at)
	if err != nil {
		return nil, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.closed {
		return nil, ErrClosed
	}
	all, err := s.historyLocked(entity)
	if err != nil {
		return nil, err
	}

	out := make([]ports.Datom, 0, len(all))
	for _, d := range all {
		if temporal.Visible(d.Valid.From, d.Valid.To, d.TxID, q) {
			out = append(out, d)
		}
	}
	return out, nil
}

// Attributes returns the attribute names an entity CARRIES at a snapshot.
//
// ⚠ The present shape, not the history: an attribute whose latest visible datom
// is a retraction is absent. It is derived from [Load], so the two cannot disagree
// about what an entity has.
func (s *Store) Attributes(ctx context.Context, entity string, at ports.Snapshot) ([]string, error) {
	visible, err := s.Load(ctx, entity, at)
	if err != nil {
		return nil, err
	}

	// ★ [ports.Carried] rather than a fourth copy of "latest per attribute,
	// retractions suppressed" — the evaluator and search's confirmation need the
	// same reduction, and a copy that dropped the retraction half would report an
	// attribute the entity no longer has.
	carried := ports.Carried(visible)
	out := make([]string, 0, len(carried))
	for name := range carried {
		out = append(out, name)
	}
	sort.Strings(out)
	return out, nil
}

// Entities returns the entities this leaf holds, sorted and without duplicates.
//
// ★ It is a directory listing of one leaf, not the enumeration `BACKLOG.md` §20
// defers. That one is `READ` over entities nobody named, across leaves, and it
// needs a planner and a routing decision. The two look alike from the outside,
// which is exactly why the difference is written down.
func (s *Store) Entities() ([]string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.closed {
		return nil, ErrClosed
	}
	return s.entitiesLocked()
}

func (s *Store) entitiesLocked() ([]string, error) {
	seen := make(map[string]struct{})
	for _, seg := range s.segments {
		keys, err := seg.Keys()
		if err != nil {
			return nil, fmt.Errorf("leafstore: listing a segment: %w", err)
		}
		for _, k := range keys {
			seen[k] = struct{}{}
		}
	}
	for _, d := range s.tail {
		seen[d.Entity] = struct{}{}
	}

	out := make([]string, 0, len(seen))
	for k := range seen {
		out = append(out, k)
	}
	sort.Strings(out)
	return out, nil
}

// Pending is how many datoms are in the tail, unsealed.
func (s *Store) Pending() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.tail)
}

// Segments is how many sealed segments the leaf holds.
func (s *Store) Segments() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.segments)
}

// Close releases every segment's mapping. It is idempotent.
//
// ⚠ It does NOT seal. An unsealed tail is lost, which is what ADR-020 means by
// acknowledging in memory — closing quietly to a disk would make the commit point
// depend on how a process exited.
func (s *Store) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true

	var first error
	for _, seg := range s.segments {
		if err := seg.Close(); err != nil && first == nil {
			first = err
		}
	}
	s.segments = nil
	s.segmentFiles = nil
	s.tail = nil
	return first
}

// query turns a snapshot into the bound form ADR-002's predicate takes, refusing
// a zero one.
//
// ⚠ The assembly is [ports.Snapshot.Query], not a struct literal here. ADR-002
// concentrates naming both time axes in one package so that passing one instant
// into two parameters is reviewable in one file, and a storage engine writing its
// own literal is exactly the second site that guarantee cannot survive.
func query(at ports.Snapshot) (temporal.Query, error) {
	if at.At == (tx.TxID{}) {
		return temporal.Query{}, fmt.Errorf("%w: a zero transaction identifier hides every fact, "+
			"which is indistinguishable from an entity that has none", ErrNoSnapshot)
	}
	return at.Query(), nil
}
