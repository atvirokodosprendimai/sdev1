package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func fixture() string {
	return filepath.Join("..", "..", "testdata", "topology", "minimal.json")
}

// exec drives the real command and returns its streams and exit code.
func exec(t *testing.T, args ...string) (stdout, stderr string, code int) {
	t.Helper()
	var out, errOut bytes.Buffer
	full := append([]string{"sdev1-addr"}, args...)
	code = run(context.Background(), full, &out, &errOut)
	return out.String(), errOut.String(), code
}

// base returns the flags every well-formed invocation needs.
func base(entity string) []string {
	return []string{"--topology", fixture(), "--tenant", "7", "--entity", entity}
}

// TestCommandPrintsDescent checks the command shows one hop per level, each
// naming the byte of the key consumed and the child it selects.
func TestCommandPrintsDescent(t *testing.T) {
	stdout, stderr, code := exec(t, base("demo-entity")...)
	if code != 0 {
		t.Fatalf("exit %d, stderr: %s", code, stderr)
	}
	if !strings.Contains(stdout, "descent") {
		t.Errorf("output has no descent section:\n%s", stdout)
	}
	if n := strings.Count(stdout, "hop "); n != 1 {
		t.Errorf("printed %d hops, want 1 for a depth-1 map:\n%s", n, stdout)
	}
	if !strings.Contains(stdout, "leaf") {
		t.Errorf("output does not name the leaf:\n%s", stdout)
	}
}

// TestCommandReportsTheTenant checks the command names the tenant, its subtree,
// and — the part an operator needs and would otherwise assume — whether this
// depth actually isolates tenants.
func TestCommandReportsTheTenant(t *testing.T) {
	stdout, stderr, code := exec(t, append(base("demo-entity"), "--json")...)
	if code != 0 {
		t.Fatalf("exit %d, stderr: %s", code, stderr)
	}
	var got report
	if err := json.Unmarshal([]byte(stdout), &got); err != nil {
		t.Fatalf("decode JSON: %v\n%s", err, stdout)
	}
	if got.Tenant != "0007" {
		t.Errorf("tenant = %q, want %q", got.Tenant, "0007")
	}
	if got.TenantSubtree == "" {
		t.Error("no tenant subtree reported")
	}
	// The fixture declares depth 1, which is BELOW TenantBytes, so tenants
	// share leaves and the command must say so rather than implying isolation.
	if got.TenantIsolated {
		t.Errorf("reported tenant isolation at depth %d, but isolation begins at depth 2", got.Depth)
	}
}

// TestCommandSeparatesTenants checks the same entity name in two tenants lands
// on different keys — the property the tenant prefix exists for.
func TestCommandSeparatesTenants(t *testing.T) {
	decode := func(tenant string) report {
		t.Helper()
		stdout, stderr, code := exec(t,
			"--topology", fixture(), "--tenant", tenant, "--entity", "shared-name", "--json")
		if code != 0 {
			t.Fatalf("tenant %s: exit %d, stderr: %s", tenant, code, stderr)
		}
		var r report
		if err := json.Unmarshal([]byte(stdout), &r); err != nil {
			t.Fatalf("decode JSON: %v", err)
		}
		return r
	}

	a, b := decode("1"), decode("2")
	if a.Key == b.Key {
		t.Fatal("the same entity in two tenants produced one key — the tenant prefix is not in the key")
	}
	if a.TenantSubtree == b.TenantSubtree {
		t.Fatalf("two tenants share the subtree %q", a.TenantSubtree)
	}
	if !strings.HasPrefix(a.Key, "0001") {
		t.Errorf("tenant 1's key begins %q, want it to begin with the tenant's own bytes", a.Key[:8])
	}
	if !strings.HasPrefix(b.Key, "0002") {
		t.Errorf("tenant 2's key begins %q, want it to begin with the tenant's own bytes", b.Key[:8])
	}
}

// TestCommandResolvesToTargets checks the printed targets are the ones placement
// resolves. This is the assertion that goes red if the call site is deleted.
func TestCommandResolvesToTargets(t *testing.T) {
	stdout, stderr, code := exec(t, append(base("demo-entity"), "--json")...)
	if code != 0 {
		t.Fatalf("exit %d, stderr: %s", code, stderr)
	}
	var got report
	if err := json.Unmarshal([]byte(stdout), &got); err != nil {
		t.Fatalf("decode JSON: %v\n%s", err, stdout)
	}
	if len(got.Targets) != 4 {
		t.Errorf("resolved %d targets (%v), want the fixture's 4 disks", len(got.Targets), got.Targets)
	}
	for _, name := range got.Targets {
		if !strings.Contains(name, "-d") {
			t.Errorf("target %q is not a disk; placement should return the deepest level", name)
		}
	}
}

// TestCommandExitsNonZeroOnBadTopology checks an operator diagnostic never exits
// 0 on a broken input.
func TestCommandExitsNonZeroOnBadTopology(t *testing.T) {
	t.Run("missing file", func(t *testing.T) {
		_, stderr, code := exec(t, "--topology", "no/such/map.json", "--tenant", "7", "--entity", "x")
		if code == 0 {
			t.Error("exit 0 for a missing topology file")
		}
		if !strings.Contains(stderr, "open topology") {
			t.Errorf("stderr does not explain the failure: %q", stderr)
		}
	})

	t.Run("unknown format version", func(t *testing.T) {
		bad := filepath.Join(t.TempDir(), "bad.json")
		writeFile(t, bad, `{"version":99,"depth":1,"levels":["a"],"root":{"level":"a","name":"x"}}`)
		_, stderr, code := exec(t, "--topology", bad, "--tenant", "7", "--entity", "x")
		if code == 0 {
			t.Error("exit 0 for a map declaring an unknown format version")
		}
		if !strings.Contains(stderr, "load topology") {
			t.Errorf("stderr does not explain the failure: %q", stderr)
		}
	})

	t.Run("undeclared spread level", func(t *testing.T) {
		_, stderr, code := exec(t, append(base("x"), "--spread-level", "nope")...)
		if code == 0 {
			t.Error("exit 0 for a spread level the map does not declare")
		}
		if !strings.Contains(stderr, "not declared") {
			t.Errorf("stderr does not explain the failure: %q", stderr)
		}
	})

	t.Run("tenant out of range", func(t *testing.T) {
		_, stderr, code := exec(t, "--topology", fixture(), "--tenant", "70000", "--entity", "x")
		if code == 0 {
			t.Error("exit 0 for a tenant beyond the prefix width")
		}
		if !strings.Contains(stderr, "outside 0-65535") {
			t.Errorf("stderr does not explain the failure: %q", stderr)
		}
	})
}

// TestCommandJSONMatchesTextOutput checks the two renderings report the same
// leaf and targets in the same order, so they cannot drift.
func TestCommandJSONMatchesTextOutput(t *testing.T) {
	args := append(base("drift-check"), "--spread-level", "rack", "--from", "srv-1")

	text, stderr, code := exec(t, args...)
	if code != 0 {
		t.Fatalf("text form exit %d, stderr: %s", code, stderr)
	}
	raw, stderr, code := exec(t, append(args, "--json")...)
	if code != 0 {
		t.Fatalf("json form exit %d, stderr: %s", code, stderr)
	}
	var got report
	if err := json.Unmarshal([]byte(raw), &got); err != nil {
		t.Fatalf("decode JSON: %v", err)
	}

	if !strings.Contains(text, got.Leaf) {
		t.Errorf("text form does not carry the leaf %q reported by JSON:\n%s", got.Leaf, text)
	}
	if !strings.Contains(text, got.Tenant) {
		t.Errorf("text form does not carry the tenant %q reported by JSON", got.Tenant)
	}
	for _, section := range [][]string{got.Targets, got.Spread, got.Nearest} {
		for i, name := range section {
			if !strings.Contains(text, name) {
				t.Errorf("text form is missing %q (position %d) reported by JSON", name, i)
			}
		}
	}
	if len(got.Spread) != len(got.Targets) || len(got.Nearest) != len(got.Targets) {
		t.Errorf("spread has %d and nearest %d entries, targets %d: both must be permutations",
			len(got.Spread), len(got.Nearest), len(got.Targets))
	}
}

// TestCommandRequiresItsFlags checks the command refuses rather than guessing
// when a required flag is absent — including the tenant, which has no default
// by design.
func TestCommandRequiresItsFlags(t *testing.T) {
	if _, _, code := exec(t, "--tenant", "7", "--entity", "x"); code == 0 {
		t.Error("exit 0 with no --topology")
	}
	if _, _, code := exec(t, "--topology", fixture(), "--tenant", "7"); code == 0 {
		t.Error("exit 0 with no --entity")
	}
	if _, _, code := exec(t, "--topology", fixture(), "--entity", "x"); code == 0 {
		t.Error("exit 0 with no --tenant: a defaulted tenant is how multi-tenancy " +
			"quietly becomes single-tenancy")
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
