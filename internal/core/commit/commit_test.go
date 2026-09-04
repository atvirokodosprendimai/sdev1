package commit

import (
	"errors"
	"slices"
	"testing"

	"github.com/atvirokodosprendimai/sdev1/internal/core/durability"
	"github.com/atvirokodosprendimai/sdev1/internal/core/lease"
)

const epoch = lease.Epoch(7)

func mustCondition(t *testing.T, size, floor int) Condition {
	t.Helper()
	p, err := durability.Replicated(size, floor, "rack")
	if err != nil {
		t.Fatalf("durability.Replicated(%d, %d): %v", size, floor, err)
	}
	// ⚠ The level is POWER, not the policy's rack. For unflushed memory the
	// failure guarded against is a power event, and a rack is not a power
	// boundary.
	c, err := NewCondition(p, "power")
	if err != nil {
		t.Fatalf("NewCondition: %v", err)
	}
	return c
}

func acks(domains ...string) []Ack {
	out := make([]Ack, 0, len(domains))
	for i, d := range domains {
		out = append(out, Ack{Node: string(rune('a'+i)) + "-node", Domain: d, Epoch: epoch})
	}
	return out
}

// TestReplicasInOneDomainDoNotCommit is the falsifier ADR-020 names in its
// Enforced-by header.
//
// ⚠ Three acknowledgements from three PROCESSES on one power feed is one failure
// domain wearing three names. The count reads as triple durability right up
// until the feed drops, which is why counting acknowledgements rather than
// domains is the failure this record exists to prevent.
func TestReplicasInOneDomainDoNotCommit(t *testing.T) {
	c := mustCondition(t, 3, 3)

	// Three replies, one feed.
	err := c.Satisfied(acks("feed-a", "feed-a", "feed-a"), epoch)
	if !errors.Is(err, ErrOneDomain) {
		t.Fatalf("three acknowledgements from one domain: error = %v, want ErrOneDomain — the "+
			"count is sufficient and the durability is not", err)
	}

	// Two of three domains is still short, and it is the same kind of failure.
	if err := c.Satisfied(acks("feed-a", "feed-a", "feed-b"), epoch); !errors.Is(err, ErrOneDomain) {
		t.Errorf("three acknowledgements spanning two domains: error = %v, want ErrOneDomain", err)
	}

	// A blank domain cannot be shown distinct from anything, so it counts as
	// none rather than as its own.
	blank := []Ack{
		{Node: "a", Domain: "feed-a", Epoch: epoch},
		{Node: "b", Domain: "", Epoch: epoch},
		{Node: "c", Domain: "", Epoch: epoch},
	}
	if err := c.Satisfied(blank, epoch); err == nil {
		t.Error("acknowledgements with undeclared domains committed; a domain nobody declared " +
			"cannot be shown to be distinct from any other")
	}
}

// TestDistinctDomainsCommit is the companion that makes the refusal above mean
// something.
//
// ⚠ Without it, "one domain does not commit" would hold for a condition that
// never commits at all.
func TestDistinctDomainsCommit(t *testing.T) {
	c := mustCondition(t, 3, 3)

	if err := c.Satisfied(acks("feed-a", "feed-b", "feed-c"), epoch); err != nil {
		t.Fatalf("three acknowledgements across three domains did not commit: %v", err)
	}

	// More than enough also commits.
	if err := c.Satisfied(acks("feed-a", "feed-b", "feed-c", "feed-d"), epoch); err != nil {
		t.Errorf("four domains against a floor of three did not commit: %v", err)
	}

	// Extra acknowledgements inside domains already counted change nothing,
	// because domains are what is counted.
	extra := append(acks("feed-a", "feed-b", "feed-c"), Ack{Node: "d", Domain: "feed-a", Epoch: epoch})
	if err := c.Satisfied(extra, epoch); err != nil {
		t.Errorf("a duplicate domain broke a commit that was already satisfied: %v", err)
	}

	// And the domains are reportable, so an operator sees what was spanned.
	got := DistinctDomains(extra)
	if !slices.Equal(got, []string{"feed-a", "feed-b", "feed-c"}) {
		t.Errorf("DistinctDomains = %v, want the three spanned", got)
	}
}

// TestStaleEpochAcknowledgementsDoNotCount checks a fenced-out writer cannot
// reach a commit on replies meant for it.
func TestStaleEpochAcknowledgementsDoNotCount(t *testing.T) {
	c := mustCondition(t, 3, 3)

	stale := []Ack{
		{Node: "a", Domain: "feed-a", Epoch: epoch - 1},
		{Node: "b", Domain: "feed-b", Epoch: epoch - 1},
		{Node: "c", Domain: "feed-c", Epoch: epoch - 1},
	}
	err := c.Satisfied(stale, epoch)
	if !errors.Is(err, ErrStaleEpoch) {
		t.Fatalf("three acknowledgements under a superseded epoch: error = %v, want ErrStaleEpoch — "+
			"a replica acknowledging to a fenced-out writer is acknowledging to nobody", err)
	}

	// The same acknowledgements under the current epoch DO commit, so the
	// refusal is about the epoch rather than about the domains.
	current := []Ack{
		{Node: "a", Domain: "feed-a", Epoch: epoch},
		{Node: "b", Domain: "feed-b", Epoch: epoch},
		{Node: "c", Domain: "feed-c", Epoch: epoch},
	}
	if err := c.Satisfied(current, epoch); err != nil {
		t.Errorf("the same acknowledgements under the current epoch did not commit: %v", err)
	}

	// A NEWER epoch is fine — the writer holds a fresher lease than the caller
	// knew about, which is not a reason to discard its replies.
	newer := []Ack{
		{Node: "a", Domain: "feed-a", Epoch: epoch + 1},
		{Node: "b", Domain: "feed-b", Epoch: epoch + 1},
		{Node: "c", Domain: "feed-c", Epoch: epoch + 1},
	}
	if err := c.Satisfied(newer, epoch); err != nil {
		t.Errorf("acknowledgements under a newer epoch were discarded: %v", err)
	}

	// A mix: only the live ones count.
	mixed := []Ack{
		{Node: "a", Domain: "feed-a", Epoch: epoch},
		{Node: "b", Domain: "feed-b", Epoch: epoch},
		{Node: "c", Domain: "feed-c", Epoch: epoch - 1},
	}
	if err := c.Satisfied(mixed, epoch); err == nil {
		t.Error("a commit succeeded counting an acknowledgement from a superseded epoch")
	}
}

// TestShortfallIsRefusedNotDowngraded checks there is no partial success.
func TestShortfallIsRefusedNotDowngraded(t *testing.T) {
	c := mustCondition(t, 3, 3)

	// One domain short.
	err := c.Satisfied(acks("feed-a", "feed-b"), epoch)
	if err == nil {
		t.Fatal("two domains against a floor of three committed; an acknowledgement with a " +
			"warning is how a cluster ends up holding data at a durability nobody chose")
	}
	if !errors.Is(err, ErrBelowFloor) {
		t.Errorf("two acknowledgements against a floor of three: error = %v, want ErrBelowFloor", err)
	}

	// Nothing at all.
	if err := c.Satisfied(nil, epoch); !errors.Is(err, ErrBelowFloor) {
		t.Errorf("no acknowledgements: error = %v, want ErrBelowFloor", err)
	}

	// Satisfied returns an error or nil and nothing in between — there is no
	// "best achieved" for a caller to accept.
	if err := c.Satisfied(acks("feed-a", "feed-b", "feed-c"), epoch); err != nil {
		t.Errorf("a satisfied condition returned %v, want nil", err)
	}

	// A condition needs a domain level; leaving it unstated would silently judge
	// distinctness at whatever the policy happened to use.
	p, perr := durability.Replicated(3, 3, "rack")
	if perr != nil {
		t.Fatalf("durability.Replicated: %v", perr)
	}
	if _, err := NewCondition(p, ""); err == nil {
		t.Error("a condition with no domain level was accepted")
	}
}

// TestConditionNamesWhyItFailed checks the three failures are distinguishable,
// because they call for three different operator actions.
func TestConditionNamesWhyItFailed(t *testing.T) {
	c := mustCondition(t, 3, 3)

	cases := []struct {
		name  string
		acks  []Ack
		want  error
		fixes string
	}{
		{"too few", acks("feed-a"), ErrBelowFloor, "restore capacity"},
		{"one domain", acks("feed-a", "feed-a", "feed-a"), ErrOneDomain, "fix placement"},
		{"superseded", []Ack{
			{Node: "a", Domain: "feed-a", Epoch: epoch - 1},
			{Node: "b", Domain: "feed-b", Epoch: epoch - 1},
			{Node: "c", Domain: "feed-c", Epoch: epoch - 1},
		}, ErrStaleEpoch, "stop writing"},
	}

	for _, tc := range cases {
		err := c.Satisfied(tc.acks, epoch)
		if !errors.Is(err, tc.want) {
			t.Errorf("%s: error = %v, want %v (the operator's action here is to %s)",
				tc.name, err, tc.want, tc.fixes)
		}
	}

	// The three are distinct sentinels, so a caller can branch on them.
	seen := map[error]bool{}
	for _, e := range []error{ErrBelowFloor, ErrOneDomain, ErrStaleEpoch} {
		if seen[e] {
			t.Error("two of the three failures are the same sentinel; collapsing them loses the " +
				"operator's next action rather than any behaviour")
		}
		seen[e] = true
	}
	for _, a := range []error{ErrBelowFloor, ErrOneDomain, ErrStaleEpoch} {
		for _, b := range []error{ErrBelowFloor, ErrOneDomain, ErrStaleEpoch} {
			if a != b && errors.Is(a, b) {
				t.Errorf("%v matches %v; the two are not distinguishable by a caller", a, b)
			}
		}
	}
}
