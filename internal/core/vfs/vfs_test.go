package vfs

import (
	"testing"

	"github.com/atvirokodosprendimai/sdev1/internal/core/ql"
)

// mustParse parses a path the test expects to be accepted.
func mustParse(t *testing.T, raw string) Path {
	t.Helper()
	p, errno := ParsePath(raw)
	if errno != OK {
		t.Fatalf("ParsePath(%q) = %s, want OK", raw, errno)
	}
	return p
}

// TestAPathCompilesToAQuery is ADR-014's falsifier.
//
// ⚠ Reading a datom directly is shorter than building a statement and handing it
// somewhere, so this is the mistake a reasonable implementation makes. A path
// that answers without a query is a second query surface with its own time
// semantics, and it diverges exactly on historical reads.
func TestAPathCompilesToAQuery(t *testing.T) {
	cases := []struct {
		path       string
		entity     string
		attributes []string
	}{
		{path: "/e/planet-7/mass", entity: "planet-7", attributes: []string{"mass"}},
		{path: "/e/planet-7", entity: "planet-7", attributes: nil},
		{path: "/.at/1700000000/e/planet-7/mass", entity: "planet-7", attributes: []string{"mass"}},
	}

	for _, tc := range cases {
		t.Run(tc.path, func(t *testing.T) {
			statement, ok := mustParse(t, tc.path).Compile()
			if !ok {
				t.Fatalf("%q names data and compiled to nothing", tc.path)
			}
			if statement == nil {
				t.Fatalf("%q compiled to a nil statement, so it is answered by reaching past the language rather than through it", tc.path)
			}
			selection, isRead := statement.(*ql.Read)
			if !isRead {
				t.Fatalf("%q compiled to %T, want *ql.Read", tc.path, statement)
			}
			if selection.Entity != tc.entity {
				t.Fatalf("%q selects entity %q, want %q", tc.path, selection.Entity, tc.entity)
			}
			if len(selection.Attributes) != len(tc.attributes) {
				t.Fatalf("%q selects attributes %v, want %v", tc.path, selection.Attributes, tc.attributes)
			}
			for i, want := range tc.attributes {
				if selection.Attributes[i] != want {
					t.Fatalf("%q selects attribute %q at %d, want %q", tc.path, selection.Attributes[i], i, want)
				}
			}
		})
	}

	// The root names no datom, and the language cannot enumerate. Saying so is
	// the honest answer; inventing entries would be indistinguishable from a real
	// listing to every caller, including a backup that recorded it as truth.
	if statement, ok := mustParse(t, "/").Compile(); ok || statement != nil {
		t.Fatalf("the root compiled to %v, but the language cannot enumerate entities", statement)
	}
}

// TestASnapshotPathIsAnOrdinaryPath checks the property the whole projection is
// worth building for.
func TestASnapshotPathIsAnOrdinaryPath(t *testing.T) {
	const instant = int64(1700000000)

	bare := mustParse(t, "/e/planet-7/mass")
	snapshot := mustParse(t, "/.at/1700000000/e/planet-7/mass")

	if snapshot.Kind != bare.Kind || snapshot.Entity != bare.Entity || snapshot.Attribute != bare.Attribute {
		t.Fatalf("a snapshot path named %+v, want the same node as %+v — anything else means a tool walking the tree needs to know about snapshots", snapshot, bare)
	}
	if bare.At != nil {
		t.Fatalf("a bare path carried an instant: %d", *bare.At)
	}
	if snapshot.At == nil || *snapshot.At != instant {
		t.Fatalf("a snapshot path did not carry its instant")
	}

	statement, ok := snapshot.Compile()
	if !ok {
		t.Fatal("a snapshot path compiled to nothing")
	}
	when := statement.(*ql.Read).Time
	if when.ValidAt == nil || *when.ValidAt != instant {
		t.Fatal("the instant did not reach the statement")
	}
	// Carried AS WRITTEN: resolving here would be a second implementation of the
	// defaults table, and two drift invisibly until a query returns the wrong
	// history.
	if bareStatement, _ := bare.Compile(); bareStatement.(*ql.Read).Time.ValidAt != nil {
		t.Fatal("a path with no snapshot prefix resolved a default instant itself")
	}

	// A prefix that names no readable instant is refused rather than treated as
	// an entity called ".at".
	for _, raw := range []string{"/.at", "/.at/", "/.at/yesterday/e/planet-7", "/.at/e/planet-7"} {
		if _, errno := ParsePath(raw); errno != EINVAL {
			t.Fatalf("ParsePath(%q) = %s, want EINVAL", raw, errno)
		}
	}
}

// TestAWriteIsRefusedAtOpen opens every node kind for writing.
//
// ⚠ A test that only opened FILES would prove nothing about directories, where
// EISDIR is the tempting answer — and EISDIR tells a caller that opening a file
// for writing would have worked.
func TestAWriteIsRefusedAtOpen(t *testing.T) {
	nodes := map[string]Path{
		"root":           mustParse(t, "/"),
		"entity dir":     mustParse(t, "/e/planet-7"),
		"attribute file": mustParse(t, "/e/planet-7/mass"),
		"snapshot file":  mustParse(t, "/.at/1700000000/e/planet-7/mass"),
	}
	intents := map[string]OpenFlags{
		"write":               OpenWrite,
		"create":              OpenCreate,
		"truncate":            OpenTruncate,
		"append":              OpenAppend,
		"read and write":      OpenRead | OpenWrite,
		"create and truncate": OpenCreate | OpenTruncate,
	}

	for nodeName, node := range nodes {
		for intentName, flags := range intents {
			if errno := Open(node, flags); errno != EROFS {
				t.Fatalf("opening a %s to %s returned %s, want EROFS — the refusal must come from the caller's INTENT at open, before the node kind and long before any buffered write reaches close(2)", nodeName, intentName, errno)
			}
		}
	}

	// Positive controls: reading works, and a directory read as a file is EISDIR
	// rather than EROFS — so the EROFS above is about the write and not about
	// everything.
	if errno := Open(nodes["attribute file"], OpenRead); errno != OK {
		t.Fatalf("opening an attribute file for reading returned %s, want OK", errno)
	}
	if errno := Open(nodes["entity dir"], OpenRead); errno != EISDIR {
		t.Fatalf("opening a directory for reading returned %s, want EISDIR", errno)
	}
}

// TestAShreddedDatomIsIndistinguishableFromAnAbsentOne checks that stat is not an
// oracle.
//
// ⚠ "Both fail" is NOT the property — two different failures would still be an
// oracle. The assertion is that the two answers are EQUAL, which is the
// observable form of indistinguishable.
func TestAShreddedDatomIsIndistinguishableFromAnAbsentOne(t *testing.T) {
	absent := Stat(PresenceAbsent)
	shredded := Stat(PresenceShredded)

	if absent != shredded {
		t.Fatalf("absent reports %s and shredded reports %s; the difference confirms the subject existed, and anyone who can guess a name can ask", absent, shredded)
	}
	if shredded != ENOENT {
		t.Fatalf("a shredded datom reports %s, want ENOENT", shredded)
	}
	if present := Stat(PresencePresent); present != OK {
		t.Fatalf("a present datom reports %s, want OK — without this the test above would pass for a Stat that refused everything", present)
	}
}

// TestAParentReferenceIsRefusedNotResolved walks out of a snapshot.
//
// ⚠ The case that matters is inside a snapshot prefix: a resolved ".." climbs
// out, and the caller who asked for history gets a confident answer from live
// data. A test outside a snapshot would prove the wrong thing.
func TestAParentReferenceIsRefusedNotResolved(t *testing.T) {
	escapes := []string{
		"/.at/1700000000/e/planet-7/../../../e/planet-9/mass",
		"/.at/1700000000/e/planet-7/..",
		"/.at/1700000000/../e/planet-7/mass",
		"/e/planet-7/..",
		"/e/./planet-7",
		"/..",
	}

	for _, raw := range escapes {
		p, errno := ParsePath(raw)
		if errno != EINVAL {
			t.Fatalf("ParsePath(%q) = %s (%+v), want EINVAL — resolving a dot segment inside a snapshot answers a historical question from live data, which is a wrong answer the caller cannot see", raw, errno, p)
		}
	}
}

// TestModTimeIsTheTransactionNotTheRead reads the same unchanged fact twice.
//
// ⚠ A single read cannot fail this, because any value equals itself. Two reads at
// different times is what an incremental tool actually does, and it is the only
// shape that distinguishes the transaction time from the clock.
func TestModTimeIsTheTransactionNotTheRead(t *testing.T) {
	file := mustParse(t, "/e/planet-7/mass")
	const asserted = int64(100)

	first := StatAttr(file, Datom{Value: "5.97e24", TxTime: asserted, ReadAt: 5000})
	second := StatAttr(file, Datom{Value: "5.97e24", TxTime: asserted, ReadAt: 9000})

	if first.ModTime != second.ModTime {
		t.Fatalf("two reads of an unchanged fact reported mtimes %d and %d; every incremental tool over this mount would copy everything, every pass", first.ModTime, second.ModTime)
	}
	if first.ModTime != asserted {
		t.Fatalf("mtime is %d, want the transaction time %d", first.ModTime, asserted)
	}
	if first.AccessTime != 5000 || second.AccessTime != 9000 {
		t.Fatalf("atime should be the read: got %d and %d", first.AccessTime, second.AccessTime)
	}
	if first.Size != int64(len("5.97e24")) {
		t.Fatalf("size is %d, want %d — rsync compares mtime AND size", first.Size, len("5.97e24"))
	}
}

// TestNoModeCarriesAWriteBit checks read-only is visible in metadata, so a caller
// that checks permissions learns the same thing as one that tries.
func TestNoModeCarriesAWriteBit(t *testing.T) {
	for _, kind := range Kinds() {
		mode := ModeOf(kind)
		if mode&ModeWriteBits != 0 {
			t.Fatalf("%s has mode %#o, which carries a write bit", kind, mode)
		}
		if mode == 0 {
			t.Fatalf("%s has mode 0, which is unreadable rather than read-only", kind)
		}
	}
}

// TestBeyondAnAttributeIsNotADirectory checks an attribute file has no children.
func TestBeyondAnAttributeIsNotADirectory(t *testing.T) {
	for _, raw := range []string{
		"/e/planet-7/mass/deeper",
		"/e/planet-7/mass/deeper/still",
		"/.at/1700000000/e/planet-7/mass/deeper",
	} {
		if _, errno := ParsePath(raw); errno != ENOTDIR {
			t.Fatalf("ParsePath(%q) = %s, want ENOTDIR", raw, errno)
		}
	}
}

// TestThereIsNoFourthNodeKind is the control-file guard.
//
// ⚠ A node that changes behaviour when written is a write surface behind Open's
// refusal, and it makes a path's meaning depend on hidden state — so two
// processes reading the same path get different answers.
func TestThereIsNoFourthNodeKind(t *testing.T) {
	known := make(map[Kind]bool, len(Kinds()))
	for _, kind := range Kinds() {
		known[kind] = true
	}

	for _, raw := range []string{"/", "/e", "/e/planet-7", "/e/planet-7/mass", "/.at/1700000000/e/planet-7/mass"} {
		p := mustParse(t, raw)
		if !known[p.Kind] {
			t.Fatalf("ParsePath(%q) produced kind %s, which is outside the closed set", raw, p.Kind)
		}
	}

	// A dot-prefixed name at the ROOT — where a special node would be
	// interpreted specially — is an absent node, not a control node.
	for _, raw := range []string{"/.control", "/.snapshot", "/.at-now", "/config"} {
		if _, errno := ParsePath(raw); errno != ENOENT {
			t.Fatalf("ParsePath(%q) = %s, want ENOENT — a special node here is a write surface behind the read-only refusal", raw, errno)
		}
	}

	// ⚠ But a dot-prefixed name UNDER /e is an ordinary entity, and must stay
	// one. Entity names are arbitrary strings, so refusing this would make an
	// entity named ".control" unreachable through the mount — and data you
	// cannot read is a worse bug than a name that `ls` hides by convention.
	// Nothing under /e is ever interpreted, which is why no control file can
	// live there either.
	hidden := mustParse(t, "/e/.control")
	if hidden.Kind != KindEntityDir || hidden.Entity != ".control" {
		t.Fatalf("ParsePath(\"/e/.control\") produced %+v, want the entity %q", hidden, ".control")
	}
}
