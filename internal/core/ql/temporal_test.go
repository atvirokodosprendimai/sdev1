package ql

import (
	"go/ast"
	goparser "go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/atvirokodosprendimai/sdev1/internal/core/hlc"
	"github.com/atvirokodosprendimai/sdev1/internal/core/tx"
)

const now = int64(1_000_000)

func parseRead(t *testing.T, src string) *Read {
	t.Helper()
	stmt, err := Parse(src)
	if err != nil {
		t.Fatalf("Parse(%q): %v", src, err)
	}
	sel, ok := stmt.(*Read)
	if !ok {
		t.Fatalf("Parse(%q) returned %T, want *Read", src, stmt)
	}
	return sel
}

// TestTimeClauseImplementsTheDefaultsTable is the falsifier ADR-011 names in its
// Enforced-by header.
//
// ⚠ It drives ALL FOUR rows of ADR-002's table. A test covering only "nothing"
// and "both" would pass for an implementation that binds a lone instant to both
// axes — which is the defect a predecessor project shipped, with roughly 140
// green tests, because every one of them happened to write with the two axes
// equal.
func TestTimeClauseImplementsTheDefaultsTable(t *testing.T) {
	const (
		instant = int64(500)
		txWall  = int64(900)
	)

	for _, row := range []struct {
		wrote       string
		src         string
		wantAsOfSet bool
		wantAsOf    int64
		wantValidAt int64
	}{
		{
			wrote:       "nothing",
			src:         "READ name FROM person",
			wantAsOfSet: false,
			wantValidAt: now,
		},
		{
			wrote:       "AS OF t",
			src:         "READ name FROM person AS OF 500",
			wantAsOfSet: false, // OPEN — this is the row that matters
			wantValidAt: instant,
		},
		{
			wrote:       "AS OF t TRANSACTION u",
			src:         "READ name FROM person AS OF 500 TRANSACTION 900",
			wantAsOfSet: true,
			wantAsOf:    txWall,
			wantValidAt: instant,
		},
		{
			wrote:       "TRANSACTION u",
			src:         "READ name FROM person TRANSACTION 900",
			wantAsOfSet: true,
			wantAsOf:    txWall,
			wantValidAt: now,
		},
	} {
		sel := parseRead(t, row.src)
		q := sel.Time.Resolve(now)

		if row.wantAsOfSet {
			if q.AsOf == nil {
				t.Errorf("%s: AsOf is open, want %d", row.wrote, row.wantAsOf)
			} else if q.AsOf.HLC.Wall != row.wantAsOf {
				t.Errorf("%s: AsOf = %d, want %d", row.wrote, q.AsOf.HLC.Wall, row.wantAsOf)
			}
		} else if q.AsOf != nil {
			t.Errorf("%s: AsOf = %d, want OPEN — binding a lone instant to the transaction axis "+
				"makes a backdated write invisible at the instant it was backdated to, which is "+
				"the exact defect this table exists to prevent", row.wrote, q.AsOf.HLC.Wall)
		}

		if q.ValidAt == nil {
			t.Errorf("%s: ValidAt is open, want %d — it always resolves", row.wrote, row.wantValidAt)
		} else if *q.ValidAt != row.wantValidAt {
			t.Errorf("%s: ValidAt = %d, want %d", row.wrote, *q.ValidAt, row.wantValidAt)
		}
	}
}

// TestLoneInstantBindsValidTimeOnly is row two on its own, because it is the row
// that a real project got wrong.
//
// The scenario: a fact valid from the past, written now. Under the rule, a query
// at that past instant sees it. Under the rejected behaviour it does not,
// because the write's transaction time is after the cutoff.
func TestLoneInstantBindsValidTimeOnly(t *testing.T) {
	sel := parseRead(t, "READ balance FROM account AS OF 100")
	q := sel.Time.Resolve(now)

	if q.AsOf != nil {
		t.Fatalf("a lone instant bound the transaction axis to %d; it must leave it OPEN, or a "+
			"write committed now and valid from the past is excluded by its own commit time",
			q.AsOf.HLC.Wall)
	}
	if q.ValidAt == nil || *q.ValidAt != 100 {
		t.Fatalf("a lone instant bound ValidAt to %v, want 100", q.ValidAt)
	}

	// Writing the transaction qualifier explicitly is how a caller asks the other
	// question, and it is a DIFFERENT statement.
	both := parseRead(t, "READ balance FROM account AS OF 100 TRANSACTION 100")
	qb := both.Time.Resolve(now)
	if qb.AsOf == nil || qb.AsOf.HLC.Wall != 100 {
		t.Errorf("an explicit transaction qualifier was not carried: %v", qb.AsOf)
	}
	if qb.ValidAt == nil || *qb.ValidAt != 100 {
		t.Errorf("ValidAt = %v, want 100", qb.ValidAt)
	}

	// The clause records what was WRITTEN, so the resolved form can be compared
	// against the table at all.
	if sel.Time.AsOf != nil {
		t.Error("the unresolved clause carries a transaction qualifier nobody wrote")
	}
	if sel.Time.ValidAt == nil || *sel.Time.ValidAt != 100 {
		t.Errorf("the unresolved clause carries ValidAt %v, want the written 100", sel.Time.ValidAt)
	}
}

// TestPackageComputesNoDefaultsOfItsOwn is the guard against a second
// implementation of the table.
//
// ★ Resolve must FORWARD, not decide. A four-row table cannot be implemented
// without branching, so the absence of any branch in Resolve is exactly the
// property that says it did not implement one — and it is checkable in source.
func TestPackageComputesNoDefaultsOfItsOwn(t *testing.T) {
	sources, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatalf("globbing: %v", err)
	}

	fset := token.NewFileSet()
	found := false
	forwards := false
	var branches int

	for _, path := range sources {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		src, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("reading %s: %v", path, err)
		}
		file, err := goparser.ParseFile(fset, path, src, 0)
		if err != nil {
			t.Fatalf("parsing %s: %v", path, err)
		}
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Name.Name != "Resolve" || fn.Recv == nil || fn.Body == nil {
				continue
			}
			found = true
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				switch node := n.(type) {
				case *ast.IfStmt, *ast.SwitchStmt, *ast.TypeSwitchStmt:
					branches++
				case *ast.SelectorExpr:
					if node.Sel.Name == "ResolveQualifiers" {
						forwards = true
					}
				}
				return true
			})
		}
	}

	if !found {
		t.Fatal("no Resolve method was found in this package; the guard is looking at nothing, " +
			"which is indistinguishable from a package that is clean")
	}
	if !forwards {
		t.Error("Resolve does not call temporal.ResolveQualifiers — the defaults table must have " +
			"exactly one implementation, and a second one drifts invisibly until a query returns " +
			"the wrong history")
	}
	if branches != 0 {
		t.Errorf("Resolve contains %d branch(es); a four-row table cannot be implemented without "+
			"branching, so any branch here is a second implementation of ADR-002's table", branches)
	}
}

// TestGuardFlagsABranchingResolve is the positive control.
//
// Without it, TestPackageComputesNoDefaultsOfItsOwn passes whether the guard
// works or has stopped looking, and the two are identical from outside.
func TestGuardFlagsABranchingResolve(t *testing.T) {
	const offending = `package ql

type fixture struct{ ValidAt *int64 }

// Resolve here implements the table itself, which is what the guard must catch.
func (f fixture) Resolve(now int64) int64 {
	if f.ValidAt != nil {
		return *f.ValidAt
	}
	return now
}
`
	fset := token.NewFileSet()
	file, err := goparser.ParseFile(fset, "offending.go", offending, 0)
	if err != nil {
		t.Fatalf("parsing the fixture: %v", err)
	}

	branches := 0
	forwards := false
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Name.Name != "Resolve" || fn.Body == nil {
			continue
		}
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			switch node := n.(type) {
			case *ast.IfStmt, *ast.SwitchStmt, *ast.TypeSwitchStmt:
				branches++
			case *ast.SelectorExpr:
				if node.Sel.Name == "ResolveQualifiers" {
					forwards = true
				}
			}
			return true
		})
	}

	if branches == 0 {
		t.Error("the guard did not see a branch in a Resolve that plainly has one; it is not " +
			"looking, and the check on the real package proves nothing")
	}
	if forwards {
		t.Error("the guard reported a forwarding call in a fixture that has none")
	}
}

// TestResolveMatchesTheTemporalPackageDirectly checks the forwarding is
// behavioural as well as structural: the same inputs give the same answer as
// calling the resolver directly.
func TestResolveMatchesTheTemporalPackageDirectly(t *testing.T) {
	instant := int64(42)
	id := tx.TxID{HLC: hlc.Timestamp{Wall: 77}}

	for _, c := range []TimeClause{
		{},
		{ValidAt: &instant},
		{AsOf: &id},
		{ValidAt: &instant, AsOf: &id},
	} {
		got := c.Resolve(now)

		if (got.AsOf == nil) != (c.AsOf == nil) {
			t.Errorf("%+v: the transaction qualifier changed during resolution", c)
		}
		if got.AsOf != nil && *got.AsOf != *c.AsOf {
			t.Errorf("%+v: AsOf = %+v, want the written value", c, *got.AsOf)
		}
		if got.ValidAt == nil {
			t.Errorf("%+v: ValidAt is open after resolution, and it always resolves", c)
			continue
		}
		want := now
		if c.ValidAt != nil {
			want = *c.ValidAt
		}
		if *got.ValidAt != want {
			t.Errorf("%+v: ValidAt = %d, want %d", c, *got.ValidAt, want)
		}
	}
}
