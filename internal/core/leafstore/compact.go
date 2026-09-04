package leafstore

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/atvirokodosprendimai/sdev1/internal/core/datom"
	"github.com/atvirokodosprendimai/sdev1/internal/core/ports"
	"github.com/atvirokodosprendimai/sdev1/internal/core/segment"
	"github.com/atvirokodosprendimai/sdev1/internal/core/segstore"
	"github.com/atvirokodosprendimai/sdev1/internal/core/tx"
)

// identity is what makes two datoms the same datom.
//
// ⚠ It is FULL equality, the transaction identifier included — not a key somebody
// chose. ADR-026 rejected deduplication because "it needs a key that says two
// datoms are the same fact, and this store does not get to invent one — two
// identical assertions from two transactions are two facts". That stands: two
// transactions differ here, so they can never be conflated.
//
// The value is compared separately, below, because copying it into a map key
// would allocate on every datom of every read for a case that is almost always
// absent.
type identity struct {
	entity    string
	attribute string
	id        tx.TxID
	from      int64
	to        int64
	assert    bool
	reference bool
}

func identify(d ports.Datom) identity {
	return identity{
		entity:    d.Entity,
		attribute: d.Attribute,
		id:        d.TxID,
		from:      d.Valid.From,
		to:        d.Valid.To,
		assert:    d.Assert,
		reference: d.IsReference,
	}
}

// deduplicate returns the datoms with exact repeats removed, preserving order.
//
// ★ This is what makes an interrupted compaction harmless. A compaction publishes
// its output before removing its inputs, so a crash between the two leaves BOTH on
// disk — permanently. Without this, every later read of that leaf would count each
// datom twice.
//
// ⚠ Two datoms that agree on everything above and DISAGREE about their value are
// not repeats. They are a malformed write, and hiding one of them would be this
// store repairing data it does not understand.
func deduplicate(datoms []ports.Datom) []ports.Datom {
	if len(datoms) < 2 {
		return datoms
	}
	seen := make(map[identity][]int, len(datoms))
	out := datoms[:0:0]
	for _, d := range datoms {
		key := identify(d)
		duplicate := false
		for _, at := range seen[key] {
			if string(out[at].Value) == string(d.Value) {
				duplicate = true
				break
			}
		}
		if duplicate {
			continue
		}
		seen[key] = append(seen[key], len(out))
		out = append(out, d)
	}
	return out
}

// ShouldCompact reports whether a compaction is due under a policy.
//
// ⚠ It DECIDES and does not compact, for the reason ADR-028 gives about sealing:
// who runs it and how often is a deployment decision, and a package that started a
// goroutine would take that decision silently.
//
// The bound is a SEGMENT COUNT rather than a size, because the cost being paid is
// one block lookup per segment per read.
func (s *Store) ShouldCompact(p Policy) bool {
	if p.MaxSegments <= 0 {
		return false
	}
	return s.Segments() >= p.MaxSegments
}

// Compact merges every current segment into one.
//
// ⚠ It drops NOTHING. Discarding superseded datoms while rewriting them anyway is
// what compaction usually means, and here it would change the answer to every
// question about the past — which is what a bitemporal store is for. Removal is
// ADR-010's purge, which has a horizon and an acknowledgement protocol this does
// not.
//
// ⚠ The merged segment is published BEFORE the inputs are removed. The reverse
// loses data: a reader between a removal and a publish sees neither copy, and a
// crash there destroys what was durable a moment earlier. This direction's worst
// case is the harmless overlap [deduplicate] absorbs.
func (s *Store) Compact(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return ErrClosed
	}
	if len(s.segments) < 2 {
		// Nothing to merge. Compacting one segment would rewrite it for no gain
		// and briefly double the leaf's size on disk.
		return nil
	}

	entities, err := s.entitiesLocked()
	if err != nil {
		return err
	}

	merged := make(map[string][]ports.Datom, len(entities))
	for _, entity := range entities {
		history, err := s.sealedHistoryLocked(entity)
		if err != nil {
			return err
		}
		if len(history) > 0 {
			merged[entity] = history
		}
	}

	name, err := segmentName()
	if err != nil {
		return err
	}
	path := filepath.Join(s.dir, name)

	w, err := segstore.Create(path, s.leaf)
	if err != nil {
		return fmt.Errorf("leafstore: compacting: %w", err)
	}
	defer func() { _ = w.Abort() }()

	for _, entity := range entities {
		if len(merged[entity]) == 0 {
			continue
		}
		raw, err := datom.Encode(merged[entity])
		if err != nil {
			return fmt.Errorf("leafstore: compacting %q: %w", entity, err)
		}
		if err := w.Append(entity, raw, segment.CodecZstd); err != nil {
			return fmt.Errorf("leafstore: compacting %q: %w", entity, err)
		}
	}

	// ⚠ The publication. Everything after this point is cleanup, and every state
	// it can be interrupted in is one [deduplicate] already handles.
	if err := w.Seal(); err != nil {
		return fmt.Errorf("leafstore: publishing a compaction: %w", err)
	}

	compacted, err := segstore.Open(path)
	if err != nil {
		return fmt.Errorf("leafstore: reopening the segment just compacted: %w", err)
	}

	// The inputs, now redundant. Closing them releases their mappings; removing
	// the files is what actually reclaims the space.
	inputs := s.segments
	s.segments = []*segstore.Reader{compacted}

	var first error
	for i, seg := range inputs {
		if err := seg.Close(); err != nil && first == nil {
			first = err
		}
		if err := os.Remove(filepath.Join(s.dir, s.segmentFiles[i])); err != nil && first == nil {
			first = fmt.Errorf("leafstore: removing a compacted input: %w", err)
		}
	}
	s.segmentFiles = []string{name}
	return first
}

// sealedHistoryLocked gathers one entity's datoms from the SEGMENTS only.
//
// ⚠ The tail is excluded on purpose. Compaction rewrites what is already durable;
// folding the tail in would seal it as a side effect of a layout operation, and
// ADR-028 put that decision in a policy rather than here.
func (s *Store) sealedHistoryLocked(entity string) ([]ports.Datom, error) {
	var out []ports.Datom
	for _, seg := range s.segments {
		raw, err := seg.Get(entity)
		if err != nil {
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
	sortByTransaction(out)
	return deduplicate(out), nil
}
