package ql

import (
	"errors"
	"strings"
	"testing"
)

// TestSelectIsRefusedByName is ADR-034's falsifier.
//
// ⚠ Asserting merely that `SELECT * FROM planet-3` FAILS would pass with the
// keyword deleted from the table — which is the alternative ADR-034 rule 3
// rejects, because an unreserved SELECT lexes as an identifier and the statement
// then dies inside the projection with a message about attribute names, never
// mentioning that the verb was the problem. So the assertion is on the MESSAGE
// and on the sentinel, not on the fact of failure.
func TestSelectIsRefusedByName(t *testing.T) {
	_, err := Parse("SELECT * FROM planet-3")
	if err == nil {
		t.Fatal("SELECT parsed; ADR-034 replaced it with READ and it must be refused")
	}
	if !errors.Is(err, ErrSelectRenamed) {
		t.Errorf("Parse(SELECT ...) = %v, want it to match ErrSelectRenamed so a caller can "+
			"recognise the refusal without comparing message text", err)
	}
	if !strings.Contains(err.Error(), "READ") {
		t.Errorf("the refusal does not name READ: %q\n"+
			"Every example written before the rename says SELECT, so the error is the one place "+
			"a caller learns what to type instead.", err.Error())
	}

	// ⚠ And it is refused at the VERB, before the projection is read. A message
	// about attribute names would mean the parser had already started reading
	// `*` as a column, which is exactly the failure reserving the word prevents.
	if strings.Contains(err.Error(), "attribute") {
		t.Errorf("the refusal talks about attributes (%q), so SELECT was parsed as an "+
			"identifier rather than refused as a renamed verb", err.Error())
	}

	// It is not an alias either: rule 4. Two spellings of one verb is two things
	// to document and a permanent question about which is canonical.
	if _, err := Parse("SELECT mass FROM planet-3 AS OF 100"); !errors.Is(err, ErrSelectRenamed) {
		t.Errorf("a fuller SELECT was not refused (%v); the old verb is a migration aid, not a "+
			"second spelling", err)
	}
}

// TestReadReplacesSelect checks the rename changed a word and nothing else.
func TestReadReplacesSelect(t *testing.T) {
	stmt, err := Parse("READ name, mass FROM planet-3 WHERE mass > 5 AS OF 1000 TRANSACTION 900")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	read, ok := stmt.(*Read)
	if !ok {
		t.Fatalf("Parse returned %T, want *Read", stmt)
	}

	if got := strings.Join(read.Attributes, ","); got != "name,mass" {
		t.Errorf("projection = %q, want \"name,mass\"", got)
	}
	if read.Entity != "planet-3" {
		t.Errorf("entity = %q, want planet-3", read.Entity)
	}
	if read.Where == nil || read.Where.Attribute != "mass" || read.Where.Op != ">" ||
		read.Where.Value != "5" || !read.Where.ValueIsNumber {
		t.Errorf("predicate = %+v, want mass > 5 lexed as a number", read.Where)
	}
	if read.Time.ValidAt == nil || *read.Time.ValidAt != 1000 {
		t.Errorf("AS OF = %v, want 1000", read.Time.ValidAt)
	}
	if read.Time.AsOf == nil || read.Time.AsOf.HLC.Wall != 900 {
		t.Errorf("TRANSACTION = %v, want 900", read.Time.AsOf)
	}

	// `READ *` is the whole entity, and an empty projection is how that is said.
	star, err := Parse("READ * FROM planet-3")
	if err != nil {
		t.Fatalf("Parse(READ *): %v", err)
	}
	if got := star.(*Read).Attributes; len(got) != 0 {
		t.Errorf("READ * gave attributes %v, want none — empty means every attribute", got)
	}

	// Keywords are case-insensitive, so the lowercase spelling is the same
	// statement. This is the form most callers will actually type.
	if _, err := Parse("read mass from planet-3"); err != nil {
		t.Errorf("lowercase `read` was refused: %v", err)
	}
}

// TestSelectIsStillAddressableAsAnAttribute checks that reserving a word did not
// take an attribute name away.
//
// ★ ADR-021 paid for this in advance: every keyword stays reachable as
// `` `like this` ``. Reserving SELECT would otherwise make an entity carrying an
// attribute of that name unreadable, with no way to ask for it and no way to
// migrate off it — a data-loss bug introduced by a rename.
func TestSelectIsStillAddressableAsAnAttribute(t *testing.T) {
	stmt, err := Parse("READ `select` FROM planet-3")
	if err != nil {
		t.Fatalf("READ `select`: %v — reserving a keyword must not cost an attribute name", err)
	}
	if got := stmt.(*Read).Attributes; len(got) != 1 || got[0] != "select" {
		t.Fatalf("READ `select` projected %v, want [select]", got)
	}

	// It is writable too, or the attribute is readable and unmaintainable.
	if _, err := Parse("ASSERT planet-3 `select` = 1"); err != nil {
		t.Errorf("ASSERT of a `select` attribute: %v", err)
	}

	// And in a predicate, which is where a keyword collision usually bites.
	pred, err := Parse("READ * FROM planet-3 WHERE `select` = 'yes'")
	if err != nil {
		t.Fatalf("WHERE `select`: %v", err)
	}
	if got := pred.(*Read).Where.Attribute; got != "select" {
		t.Errorf("predicate names %q, want select", got)
	}

	// ⚠ The unquoted word remains a keyword — that is what makes the refusal
	// above reachable, and the quoting above load-bearing rather than decorative.
	if _, err := Parse("READ select FROM planet-3"); err == nil {
		t.Error("an unquoted `select` parsed as an attribute name; the word is reserved, and if " +
			"it lexes as an identifier then SELECT can no longer be refused by name")
	}
}
