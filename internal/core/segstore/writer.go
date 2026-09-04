package segstore

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/atvirokodosprendimai/sdev1/internal/core/addr"
	"github.com/atvirokodosprendimai/sdev1/internal/core/segment"
)

// Writer builds one segment. It is not safe for concurrent use — a segment is
// written by whoever decided to seal it, and sharing one would mean agreeing on
// block order, which nothing needs.
type Writer struct {
	dest  string
	tmp   string
	f     *os.File
	leaf  addr.LeafID
	off   uint64
	index []indexEntry
	keys  map[string]struct{}
	done  bool
}

// Create begins a segment for leaf, to be published at path.
//
// ⚠ Nothing appears at path until [Writer.Seal] succeeds. The bytes go to a
// temporary file in the SAME directory, because that is what makes the rename in
// Seal atomic — rename(2) guarantees nothing across filesystems, and a temporary
// directory elsewhere would quietly lose the property this package is built on.
func Create(path string, leaf addr.LeafID) (*Writer, error) {
	dir := filepath.Dir(path)
	// The leading dot and the ".partial" infix are for a human reading the
	// directory during an incident: a leftover has no valid trailer and can be
	// deleted without inspection, and the name says so.
	f, err := os.CreateTemp(dir, "."+filepath.Base(path)+".partial-*")
	if err != nil {
		return nil, fmt.Errorf("segstore: creating a temporary file beside %s: %w", path, err)
	}

	w := &Writer{
		dest: path,
		tmp:  f.Name(),
		f:    f,
		leaf: leaf,
		keys: make(map[string]struct{}),
	}

	// The header is written now so block offsets are right, and written AGAIN in
	// Seal because the block count is not known until then. That second write is
	// to the temporary file, never to a published one — publication is the rename
	// at the end of Seal, and nothing has observed these bytes before it.
	hdr := segment.Header{Version: segment.FormatVersion, Leaf: leaf}
	b := hdr.Encode()
	if _, err := f.Write(b[:]); err != nil {
		f.Close()
		os.Remove(w.tmp)
		return nil, fmt.Errorf("segstore: writing the segment header: %w", err)
	}
	w.off = segment.HeaderSize
	return w, nil
}

// Append encodes raw through ADR-005's block pipeline and stores it under key.
//
// The codec is per block rather than per segment because ADR-005's header is
// per block: a segment may mix them, and a reader is told by each block rather
// than by configuration.
//
// ⚠ Blocks go straight to the file rather than through a buffer. A block is
// deliberately large — batching small writes is what a block IS — so a buffer
// would copy megabytes for nothing, and it would leave the temporary file empty
// while the writer claimed to be making progress.
func (w *Writer) Append(key string, raw []byte, codec segment.CodecID) error {
	if w.done {
		return ErrSealed
	}
	if len(key) > maxKeyLen {
		return fmt.Errorf("segstore: key of %d bytes exceeds the %d the index can encode", len(key), maxKeyLen)
	}
	if _, dup := w.keys[key]; dup {
		return fmt.Errorf("%w: %q", ErrDuplicateKey, key)
	}

	h, stored, err := segment.EncodeBlock(raw, codec)
	if err != nil {
		return err
	}
	hb := h.Encode()
	if _, err := w.f.Write(hb[:]); err != nil {
		return fmt.Errorf("segstore: writing the header of block %q: %w", key, err)
	}
	if _, err := w.f.Write(stored); err != nil {
		return fmt.Errorf("segstore: writing block %q: %w", key, err)
	}

	span := segment.BlockHeaderSize + len(stored)
	w.index = append(w.index, indexEntry{Key: key, Offset: w.off, Span: uint32(span)})
	w.keys[key] = struct{}{}
	w.off += uint64(span)
	return nil
}

// Seal writes the index and the trailer, flushes them to the disk, and publishes
// the segment by renaming it into place.
//
// ⚠ The rename is LAST and it is the publication. Everything before it is
// invisible to a reader, which is what lets a reader trust that any segment it
// can name is complete — and therefore immutable, and therefore readable without
// coordinating with anyone.
func (w *Writer) Seal() error {
	if w.done {
		return ErrSealed
	}

	// Sorted here rather than kept sorted, because Append is called far more often
	// than Seal and a binary search is only needed on the read side.
	sort.Slice(w.index, func(i, j int) bool { return w.index[i].Key < w.index[j].Key })

	idx := encodeIndex(w.index)
	indexOff := w.off
	if _, err := w.f.Write(idx); err != nil {
		return fmt.Errorf("segstore: writing the index: %w", err)
	}

	t := trailer{
		Version:  FormatVersion,
		IndexOff: indexOff,
		IndexLen: uint64(len(idx)),
		IndexSum: segment.Checksum(idx),
	}
	tb := t.encode()
	if _, err := w.f.Write(tb[:]); err != nil {
		return fmt.Errorf("segstore: writing the trailer: %w", err)
	}

	hdr := segment.Header{Version: segment.FormatVersion, Leaf: w.leaf, Blocks: uint32(len(w.index))}
	hb := hdr.Encode()
	if _, err := w.f.WriteAt(hb[:], 0); err != nil {
		return fmt.Errorf("segstore: recording the block count: %w", err)
	}

	// ⚠ Before the rename, not after. A rename that reaches the directory ahead of
	// the contents publishes a segment whose bytes are not yet on the disk.
	if err := w.f.Sync(); err != nil {
		return fmt.Errorf("segstore: flushing %s: %w", w.tmp, err)
	}
	if err := w.f.Close(); err != nil {
		return fmt.Errorf("segstore: closing %s: %w", w.tmp, err)
	}
	w.done = true

	if err := os.Rename(w.tmp, w.dest); err != nil {
		return fmt.Errorf("segstore: publishing %s: %w", w.dest, err)
	}
	return syncDir(filepath.Dir(w.dest))
}

// Abort discards a segment that will not be sealed.
//
// It is a no-op after a successful Seal, so `defer w.Abort()` beside a `w.Seal()`
// is the safe shape rather than a double failure.
func (w *Writer) Abort() error {
	if w.done {
		return nil
	}
	w.done = true
	// The close error is discarded on purpose: the file is being removed, and
	// reporting a failure to close something that is about to not exist would
	// hide the only error that matters.
	w.f.Close()
	if err := os.Remove(w.tmp); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("segstore: discarding %s: %w", w.tmp, err)
	}
	return nil
}

// syncDir flushes a directory entry.
//
// ⚠ rename(2) is atomic but not durable. Without this a crash can leave the file
// safely on the disk and the directory entry naming it lost — which is precisely
// the half-published state the rename exists to prevent.
func syncDir(dir string) error {
	d, err := os.Open(dir)
	if err != nil {
		return fmt.Errorf("segstore: opening %s to flush it: %w", dir, err)
	}
	defer d.Close()
	if err := d.Sync(); err != nil {
		return fmt.Errorf("segstore: flushing the directory %s: %w", dir, err)
	}
	return nil
}
