package vfs

import (
	"strconv"
	"strings"

	"github.com/atvirokodosprendimai/sdev1/internal/core/ql"
)

// EntityRoot is the single directory every entity lives under.
const EntityRoot = "e"

// SnapshotRoot is the path prefix that makes a read historical.
//
// ★ A path under it is an ORDINARY path, which is what lets any program that
// walks a directory tree read this store at an instant without knowing it exists.
const SnapshotRoot = ".at"

// Kind is what a path names. There are three, and there is no fourth.
//
// ⚠ A fourth kind is how a control file enters — a node that changes behaviour
// when written is a write surface behind [Open]'s refusal, and it makes a path's
// meaning depend on hidden state, so two processes reading the same path get
// different answers.
type Kind int

const (
	// KindUnset is the zero value and names nothing.
	KindUnset Kind = iota
	// KindRoot is a directory naming no datom: "/" and "/e".
	KindRoot
	// KindEntityDir is "/e/<entity>" — every attribute of one entity.
	KindEntityDir
	// KindAttributeFile is "/e/<entity>/<attribute>" — one value.
	KindAttributeFile
)

func (k Kind) String() string {
	switch k {
	case KindRoot:
		return "root"
	case KindEntityDir:
		return "entity-dir"
	case KindAttributeFile:
		return "attribute-file"
	default:
		return "unset"
	}
}

// Kinds returns every node kind a path can name.
//
// ★ It exists so a test can walk the closed set rather than a list written beside
// it, which is the only shape that notices a fourth kind being added.
func Kinds() []Kind { return []Kind{KindRoot, KindEntityDir, KindAttributeFile} }

// Errno is what the kernel is told.
type Errno int

const (
	// OK is not an error.
	OK Errno = iota
	// ENOENT: no such node. Also what an ERASED subject looks like — see [Stat].
	ENOENT
	// ENOTDIR: something below an attribute file was addressed as a directory.
	ENOTDIR
	// EISDIR: a directory was opened as a file.
	EISDIR
	// EROFS: the caller intends to write, and nothing here is writable.
	EROFS
	// EINVAL: the path is not one this grammar accepts — a dot segment, or a
	// snapshot prefix with no readable instant.
	EINVAL
	// EACCES is declared and DELIBERATELY never returned.
	//
	// ⚠ It is the tempting answer for a shredded subject and it is the wrong
	// one: a permission error confirms the entity exists, which turns stat into
	// an oracle anyone can query by guessing a name. It is named here so the
	// refusal is visible rather than merely absent.
	EACCES
)

func (e Errno) String() string {
	switch e {
	case OK:
		return "OK"
	case ENOENT:
		return "ENOENT"
	case ENOTDIR:
		return "ENOTDIR"
	case EISDIR:
		return "EISDIR"
	case EROFS:
		return "EROFS"
	case EINVAL:
		return "EINVAL"
	case EACCES:
		return "EACCES"
	default:
		return "unknown"
	}
}

// Path is a parsed path.
type Path struct {
	Kind      Kind
	Entity    string
	Attribute string
	// At is the instant a [SnapshotRoot] prefix asked for, or nil for now.
	At *int64
}

// ParsePath reads a path and says what it names, or why it is refused.
//
// ⚠ It does not clean the path. "." and ".." are REFUSED with [EINVAL], because
// resolving ".." inside a snapshot prefix climbs out of the snapshot and answers
// a historical question from live data — a silent wrong answer rather than a
// visible failure.
func ParsePath(raw string) (Path, Errno) {
	segments := make([]string, 0, 4)
	for _, segment := range strings.Split(raw, "/") {
		if segment == "" {
			continue
		}
		if segment == "." || segment == ".." {
			return Path{}, EINVAL
		}
		segments = append(segments, segment)
	}

	var at *int64
	if len(segments) > 0 && segments[0] == SnapshotRoot {
		if len(segments) < 2 {
			return Path{}, EINVAL
		}
		instant, err := strconv.ParseInt(segments[1], 10, 64)
		if err != nil {
			return Path{}, EINVAL
		}
		at = &instant
		segments = segments[2:]
	}

	// Everything that names data lives under one directory. A dot-prefixed name
	// that is not the snapshot root falls through to ENOENT here rather than
	// becoming a node of its own.
	if len(segments) > 0 && segments[0] != EntityRoot {
		return Path{}, ENOENT
	}

	switch len(segments) {
	case 0, 1:
		return Path{Kind: KindRoot, At: at}, OK
	case 2:
		return Path{Kind: KindEntityDir, Entity: segments[1], At: at}, OK
	case 3:
		return Path{Kind: KindAttributeFile, Entity: segments[1], Attribute: segments[2], At: at}, OK
	default:
		// An attribute file is not a directory.
		return Path{}, ENOTDIR
	}
}

// Compile returns the query this path means.
//
// The second return is false for a path naming no datom — the root, which would
// need an enumeration the language does not express. ★A mount that answered it by
// inventing entries would be indistinguishable from a real listing to every
// caller, including a backup that would record it as truth.
func (p Path) Compile() (ql.Statement, bool) {
	// The instant is carried AS WRITTEN. Resolving it here would be a second
	// implementation of the defaults table, and two drift invisibly until a query
	// returns the wrong history.
	var when ql.TimeClause
	if p.At != nil {
		instant := *p.At
		when.ValidAt = &instant
	}

	switch p.Kind {
	case KindAttributeFile:
		return &ql.Read{Entity: p.Entity, Attributes: []string{p.Attribute}, Time: when}, true
	case KindEntityDir:
		// Reading a directory is reading every attribute of the entity, which is
		// what an empty projection means.
		return &ql.Read{Entity: p.Entity, Time: when}, true
	default:
		return nil, false
	}
}
