package vfs

// Mode is a node's permission bits, as a filesystem reports them.
type Mode uint32

const (
	// ModeFile is an attribute file: readable by everyone, writable by nobody.
	ModeFile Mode = 0o444
	// ModeDir is a directory: readable and traversable, writable by nobody.
	ModeDir Mode = 0o555
	// ModeWriteBits are the bits no node in this projection ever carries.
	//
	// ★ Read-only is visible in metadata and not only at [Open], so a caller that
	// checks permissions before trying learns the same thing as one that tries.
	ModeWriteBits Mode = 0o222
)

// ModeOf returns the mode of a node kind.
func ModeOf(k Kind) Mode {
	if k == KindAttributeFile {
		return ModeFile
	}
	return ModeDir
}

// OpenFlags is what a caller intends by opening a node.
type OpenFlags uint32

const (
	// OpenRead is the only intent this projection serves.
	OpenRead OpenFlags = 0
	// OpenWrite is O_WRONLY or O_RDWR.
	OpenWrite OpenFlags = 1 << 0
	// OpenCreate is O_CREAT.
	OpenCreate OpenFlags = 1 << 1
	// OpenTruncate is O_TRUNC.
	OpenTruncate OpenFlags = 1 << 2
	// OpenAppend is O_APPEND.
	OpenAppend OpenFlags = 1 << 3
)

// writeIntents is every flag meaning the caller means to change something.
const writeIntents = OpenWrite | OpenCreate | OpenTruncate | OpenAppend

// Open reports what the kernel is told when a caller opens a node.
//
// ⚠ Write intent is refused FIRST, before the node kind is considered. Two
// reasons, and both are about what the caller learns:
//
// The refusal happens at OPEN rather than at write or close. A program that
// opens for writing, buffers, and fails at close(2) has already lost the data,
// and a great many programs do not check close.
//
// And a directory opened for writing is [EROFS] rather than [EISDIR]. EISDIR
// would tell the caller that opening a FILE for writing would have worked, which
// is exactly the wrong thing to learn about a read-only projection.
func Open(p Path, flags OpenFlags) Errno {
	if flags&writeIntents != 0 {
		return EROFS
	}
	switch p.Kind {
	case KindAttributeFile:
		return OK
	case KindRoot, KindEntityDir:
		return EISDIR
	default:
		return EINVAL
	}
}

// Presence is what the store can say about a node.
type Presence int

const (
	// PresencePresent: the datom is readable.
	PresencePresent Presence = iota
	// PresenceAbsent: no such fact was ever asserted.
	PresenceAbsent
	// PresenceShredded: the fact existed and its key was destroyed.
	PresenceShredded
)

// Stat reports what the kernel is told about a node's existence.
//
// ⚠ [PresenceAbsent] and [PresenceShredded] return the SAME errno, and that is
// the whole function. Distinguishing them — with EACCES, or with an empty file —
// makes a stat an oracle for whether a subject ever existed, answerable by anyone
// who can guess a name. Crypto-shredding is worth nothing if the filesystem
// confirms who was erased.
func Stat(have Presence) Errno {
	switch have {
	case PresencePresent:
		return OK
	case PresenceAbsent, PresenceShredded:
		return ENOENT
	default:
		return EINVAL
	}
}

// Datom is what the store hands back for one attribute file.
type Datom struct {
	// Value is the attribute's value.
	Value string
	// TxTime is when the assertion was RECORDED — the transaction axis.
	TxTime int64
	// ReadAt is when this read happened, which is a file's atime.
	ReadAt int64
}

// Attr is what stat(2) reports about a node.
type Attr struct {
	Mode Mode
	Size int64
	// ModTime is mtime: when the fact was ASSERTED.
	ModTime int64
	// AccessTime is atime: when it was last read.
	AccessTime int64
}

// StatAttr renders a datom as a file's attributes.
//
// ⚠ ModTime is the datom's transaction time, NEVER the read. Callers read stat
// far more than they read contents — make compares mtimes, rsync compares mtime
// and size, a backup agent skips a file whose mtime has not moved. An mtime taken
// from the clock makes every file look modified on every pass, so every
// incremental tool over this mount copies everything, every time.
//
// The failure is not visible in a single read, because any value equals itself.
// It shows up as a mount that appears to work and quietly makes every backup a
// full one.
func StatAttr(p Path, d Datom) Attr {
	return Attr{
		Mode:       ModeOf(p.Kind),
		Size:       int64(len(d.Value)),
		ModTime:    d.TxTime,
		AccessTime: d.ReadAt,
	}
}
