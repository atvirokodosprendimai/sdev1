package command

import (
	"errors"
	"testing"

	"github.com/atvirokodosprendimai/sdev1/internal/core/addr"
	"github.com/atvirokodosprendimai/sdev1/internal/core/temporal"
)

const depth = uint8(1)

// testTenant is an arbitrary tenant. It is named rather than defaulted because
// the whole point of the tenant parameter is that a caller must say which one
// they mean.
var testTenant = addr.TenantFromUint(42)

func newTx(t *testing.T, entity string) *Transaction {
	t.Helper()
	tr, err := New(testTenant, entity, depth)
	if err != nil {
		t.Fatalf("New(%q): %v", entity, err)
	}
	return tr
}

func window() temporal.Interval {
	return temporal.Interval{From: 0, To: temporal.Forever}
}

// TestNewBindsOneEntity checks the entity and its leaf are fixed at
// construction, and that an empty entity is refused rather than defaulted.
func TestNewBindsOneEntity(t *testing.T) {
	tr := newTx(t, "alice")
	if tr.Entity() != "alice" {
		t.Errorf("Entity() = %q, want %q", tr.Entity(), "alice")
	}
	want, err := addr.Descend(addr.KeyOf(testTenant, "alice"), depth)
	if err != nil {
		t.Fatalf("Descend: %v", err)
	}
	if tr.Leaf() != want {
		t.Errorf("Leaf() = %v, want %v", tr.Leaf(), want)
	}

	if _, err := New(testTenant, "", depth); !errors.Is(err, ErrNoEntity) {
		t.Errorf("New with an empty entity: error = %v, want ErrNoEntity", err)
	}
}

// TestCommandRequiresATenant checks a transaction names its tenant, so a caller
// cannot land in a default one by omission.
//
// The parameter is required rather than defaulted because a KeyOf with a default
// tenant is how multi-tenancy quietly becomes single-tenancy with an extra
// field: every call site compiles, everything lands in tenant zero, and nothing
// reports it.
// ⚠ It runs at depth TenantBytes, not at the package's depth of 1. Below
// TenantBytes tenants SHARE leaves — that is documented behaviour and
// appropriate for a small deployment, so a test asserting isolation at depth 1
// would be asserting something the design does not claim.
func TestCommandRequiresATenant(t *testing.T) {
	const isolating = uint8(addr.TenantBytes)

	a, err := New(addr.TenantFromUint(1), "shared-name", isolating)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	b, err := New(addr.TenantFromUint(2), "shared-name", isolating)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if a.Leaf() == b.Leaf() {
		t.Fatalf("the same entity name in two tenants reached one leaf %v — tenants are not isolated",
			a.Leaf())
	}
	if !addr.TenantFromUint(1).TenantSubtree().Contains(a.Leaf()) {
		t.Errorf("transaction for tenant 1 landed outside tenant 1's subtree")
	}
	if !addr.TenantFromUint(2).TenantSubtree().Contains(b.Leaf()) {
		t.Errorf("transaction for tenant 2 landed outside tenant 2's subtree")
	}
}

// TestAssertRefusesASecondEntity is the refusal that removes distributed commit
// from the system. It is a named error so the first genuine case of a domain
// needing more surfaces loudly rather than being routed around.
func TestAssertRefusesASecondEntity(t *testing.T) {
	tr := newTx(t, "alice")

	if err := tr.Assert("alice", "email", []byte("a@example.test"), window()); err != nil {
		t.Fatalf("asserting about the bound entity failed: %v", err)
	}
	err := tr.Assert("bob", "email", []byte("b@example.test"), window())
	if !errors.Is(err, ErrCrossEntity) {
		t.Fatalf("Assert about a second entity returned %v, want ErrCrossEntity", err)
	}
	// The message must name both entities, or a caller cannot see what they did.
	if msg := err.Error(); !contains(msg, "alice") || !contains(msg, "bob") {
		t.Errorf("error %q does not name both entities", msg)
	}
}

// TestRetractRefusesASecondEntity checks the boundary has no back door.
func TestRetractRefusesASecondEntity(t *testing.T) {
	tr := newTx(t, "alice")
	if err := tr.Retract("bob", "email", nil, window()); !errors.Is(err, ErrCrossEntity) {
		t.Fatalf("Retract about a second entity returned %v, want ErrCrossEntity", err)
	}
}

// TestRefusalHappensBeforeAnythingIsRecorded checks a rejected operation leaves
// the transaction unchanged, so it can be discarded without cleanup and a
// partially-applied transaction is not a reachable state.
func TestRefusalHappensBeforeAnythingIsRecorded(t *testing.T) {
	tr := newTx(t, "alice")
	if err := tr.Assert("alice", "name", []byte("Alice"), window()); err != nil {
		t.Fatalf("Assert: %v", err)
	}
	before := len(tr.Datoms())

	_ = tr.Assert("bob", "name", []byte("Bob"), window())
	_ = tr.Retract("carol", "name", nil, window())

	if after := len(tr.Datoms()); after != before {
		t.Errorf("a refused operation changed the transaction: %d datoms became %d", before, after)
	}
	for _, d := range tr.Datoms() {
		if d.Entity != "alice" {
			t.Errorf("transaction carries a datom for %q despite the refusal", d.Entity)
		}
	}
}

// TestRetractionIsADatomNotAnAbsence checks withdrawal is recorded rather than
// erased, so "no longer true" stays distinguishable from "never recorded".
func TestRetractionIsADatomNotAnAbsence(t *testing.T) {
	tr := newTx(t, "alice")
	if err := tr.Assert("alice", "email", []byte("old@example.test"), window()); err != nil {
		t.Fatalf("Assert: %v", err)
	}
	if err := tr.Retract("alice", "email", []byte("old@example.test"), window()); err != nil {
		t.Fatalf("Retract: %v", err)
	}

	ds := tr.Datoms()
	if len(ds) != 2 {
		t.Fatalf("got %d datoms, want 2 — a retraction must ADD a datom, never remove one", len(ds))
	}
	if !ds[0].Assert {
		t.Error("the assertion is not marked as one")
	}
	if ds[1].Assert {
		t.Error("the retraction is marked as an assertion")
	}
	if ds[1].Attribute != "email" {
		t.Errorf("the retraction names attribute %q, want %q", ds[1].Attribute, "email")
	}
}

// TestTransactionResolvesToOneLeaf checks every datom lands on the same leaf,
// which is what makes the commit single-leaf and therefore local.
func TestTransactionResolvesToOneLeaf(t *testing.T) {
	tr := newTx(t, "alice")
	for _, attr := range []string{"name", "email", "phone", "address"} {
		if err := tr.Assert("alice", attr, []byte("x"), window()); err != nil {
			t.Fatalf("Assert(%q): %v", attr, err)
		}
	}
	want := tr.Leaf()
	for _, d := range tr.Datoms() {
		got, err := addr.Descend(addr.KeyOf(testTenant, d.Entity), depth)
		if err != nil {
			t.Fatalf("Descend(%q): %v", d.Entity, err)
		}
		if got != want {
			t.Fatalf("datom for %q resolves to %v, but the transaction is on %v — "+
				"the commit would span leaves", d.Entity, got, want)
		}
	}
}

// TestDatomsIsACopy checks a caller cannot reach in and mutate the recorded
// datoms, which would defeat every guarantee above after the fact.
func TestDatomsIsACopy(t *testing.T) {
	tr := newTx(t, "alice")
	if err := tr.Assert("alice", "name", []byte("Alice"), window()); err != nil {
		t.Fatalf("Assert: %v", err)
	}
	got := tr.Datoms()
	got[0].Entity = "bob"

	if tr.Datoms()[0].Entity != "alice" {
		t.Error("mutating the returned slice changed the transaction's own datoms")
	}
}

func contains(haystack, needle string) bool {
	return len(haystack) >= len(needle) && (func() bool {
		for i := 0; i+len(needle) <= len(haystack); i++ {
			if haystack[i:i+len(needle)] == needle {
				return true
			}
		}
		return false
	})()
}
