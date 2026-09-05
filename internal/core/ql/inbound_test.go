package ql

import (
	"errors"
	"strings"
	"testing"
)

func mustParseRead(t *testing.T, src string) *Read {
	t.Helper()
	stmt, err := Parse(src)
	if err != nil {
		t.Fatalf("Parse(%q): %v", src, err)
	}
	read, ok := stmt.(*Read)
	if !ok {
		t.Fatalf("Parse(%q) returned %T, want *Read", src, stmt)
	}
	return read
}

// TestABracketedSourceIsASet checks `FROM [e]` and `FROM e` are two sources
// naming one identifier.
func TestABracketedSourceIsASet(t *testing.T) {
	set := mustParseRead(t, "READ ->name FROM [staff]")
	if !set.Inbound {
		t.Error("FROM [staff] did not set Inbound; the brackets are the whole difference")
	}
	// ★ Stored WITHOUT the brackets. They say which question is being asked, not
	// which entity is being addressed — the same identifier answers both.
	if set.Entity != "staff" {
		t.Errorf("entity = %q, want staff — the brackets are grammar, not part of the name",
			set.Entity)
	}
	if len(set.Attributes) != 1 || set.Attributes[0] != "name" {
		t.Errorf("projection = %v, want [name] — the -> marker is grammar, not part of the "+
			"attribute name", set.Attributes)
	}

	one := mustParseRead(t, "READ name FROM staff")
	if one.Inbound {
		t.Error("FROM staff set Inbound; an unbracketed source reads one entity")
	}
	if one.Entity != "staff" {
		t.Errorf("entity = %q, want staff", one.Entity)
	}

	// `*` is allowed over a set: every member, every attribute it carries.
	star := mustParseRead(t, "READ * FROM [staff]")
	if !star.Inbound || len(star.Attributes) != 0 {
		t.Errorf("READ * FROM [staff] = %+v, want an inbound read with an empty projection", star)
	}

	// ⚠ An unclosed bracket is refused WITH A POSITION rather than read to the
	// end of input. A source that swallows the rest of the statement turns a typo
	// into a different query.
	if _, err := Parse("READ ->name FROM [staff"); err == nil {
		t.Error("an unclosed [ parsed; it must be refused")
	} else if !strings.Contains(err.Error(), "]") {
		t.Errorf("the refusal does not mention the missing bracket: %v", err)
	}
	if _, err := Parse("READ ->name FROM [] "); err == nil {
		t.Error("an empty [] parsed; it names no entity")
	}
}

// TestABareAttributeInAnInboundReadIsRefused is ADR-035 rule 3.
//
// ★ Both directions are asserted. Refusing only one leaves the other spelling
// meaning two things depending on the source, which is the ambiguity the rule
// exists to prevent — and it is the direction a later join would break.
func TestABareAttributeInAnInboundReadIsRefused(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want string // a fragment the message must carry
	}{
		{"bare projection over a set", "READ name FROM [staff]", "->name"},
		{"bare predicate over a set", "READ ->name FROM [staff] WHERE lastname = 'a'", "->lastname"},
		{"marked projection over one entity", "READ ->name FROM staff", "[staff]"},
		{"marked predicate over one entity", "READ name FROM staff WHERE ->lastname = 'a'", "[staff]"},
		{"one bare among marked", "READ ->name, lastname FROM [staff]", "->lastname"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := Parse(c.src)
			if err == nil {
				t.Fatalf("%q parsed; the marker disagrees with the source", c.src)
			}
			if !errors.Is(err, ErrJoinNotSupported) {
				t.Errorf("%q = %v, want ErrJoinNotSupported so a caller can recognise it "+
					"without reading message text", c.src, err)
			}
			// ⚠ The message must name the form that was MEANT. "Unexpected token"
			// leaves a caller guessing between two spellings that both look right.
			if !strings.Contains(err.Error(), c.want) {
				t.Errorf("%q refused with %q, which does not name %q — the refusal is the only "+
					"place the caller learns which spelling to use", c.src, err.Error(), c.want)
			}
		})
	}

	// And the correct spellings still parse, or the refusal above is just a
	// broken parser rather than a rule.
	mustParseRead(t, "READ ->name FROM [staff] WHERE ->lastname = 'a'")
	mustParseRead(t, "READ name FROM staff WHERE lastname = 'a'")
}

// TestAPageClauseNeedsSomethingToPage checks paging is refused where it means
// nothing.
func TestAPageClauseNeedsSomethingToPage(t *testing.T) {
	// ⚠ One entity's attributes are a shape, not a sequence. Paging them would
	// have to invent an order, and any answer would be arbitrary.
	if _, err := Parse("READ * FROM planet-7 LIMIT 5"); err == nil {
		t.Error("LIMIT on a read of one entity parsed; there is nothing to page")
	} else if !strings.Contains(err.Error(), "[planet-7]") {
		t.Errorf("the refusal does not point at the form that CAN be paged: %v", err)
	}

	// An offset with no limit names a starting point and no page.
	if _, err := Parse("READ ->name FROM [staff] OFFSET 5"); err == nil {
		t.Error("OFFSET without LIMIT parsed")
	}

	// ⚠ A negative bound is refused rather than clamped: reading `LIMIT -1` as
	// zero or as unlimited picks one of two opposite answers for the caller.
	for _, src := range []string{
		"READ ->name FROM [staff] LIMIT -1",
		"READ ->name FROM [staff] LIMIT 5 OFFSET -1",
	} {
		if _, err := Parse(src); err == nil {
			t.Errorf("%q parsed; a negative bound means nothing", src)
		}
	}
}

// TestPageValuesAreRecordedAsWritten checks the clause survives parsing intact,
// and that an absent clause is distinguishable from `LIMIT 0`.
func TestPageValuesAreRecordedAsWritten(t *testing.T) {
	paged := mustParseRead(t, "READ ->name FROM [staff] WHERE ->rank = 3 LIMIT 20 OFFSET 40 AS OF 900")
	if !paged.Page.Has || paged.Page.Limit != 20 || paged.Page.Offset != 40 {
		t.Errorf("page = %+v, want {20 40 true}", paged.Page)
	}
	// The clause sits between WHERE and the time qualifier, and neither is lost.
	if paged.Where == nil || paged.Where.Attribute != "rank" || !paged.Where.ValueIsNumber {
		t.Errorf("predicate = %+v, want rank = 3 lexed as a number", paged.Where)
	}
	if paged.Time.ValidAt == nil || *paged.Time.ValidAt != 900 {
		t.Errorf("AS OF = %v, want 900", paged.Time.ValidAt)
	}

	// No clause: Has is clear, and the bounds are meaningless rather than zero.
	if got := mustParseRead(t, "READ ->name FROM [staff]").Page; got.Has {
		t.Errorf("an absent page clause gave %+v, want Has clear", got)
	}

	// ⚠ THE POINT: `LIMIT 0` is a written clause asking for no rows, and an
	// absent clause asks for all of them. They are opposite answers, so they must
	// be different states — a Page without Has would make the emptier one the
	// default for every statement that omits the clause.
	none := mustParseRead(t, "READ ->name FROM [staff] LIMIT 0").Page
	if !none.Has || none.Limit != 0 {
		t.Errorf("LIMIT 0 gave %+v, want {0 0 true}", none)
	}

	// Offset defaults to zero when only a limit is written.
	if got := mustParseRead(t, "READ ->name FROM [staff] LIMIT 7").Page; !got.Has ||
		got.Limit != 7 || got.Offset != 0 {
		t.Errorf("LIMIT 7 gave %+v, want {7 0 true}", got)
	}
}
