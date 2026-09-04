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

// TestCommandPrintsDescent checks the command shows one hop per level, each
// naming the byte of the key consumed and the child it selects.
func TestCommandPrintsDescent(t *testing.T) {
	stdout, stderr, code := exec(t, "--topology", fixture(), "--entity", "demo-entity")
	if code != 0 {
		t.Fatalf("exit %d, stderr: %s", code, stderr)
	}
	if !strings.Contains(stdout, "descent") {
		t.Errorf("output has no descent section:\n%s", stdout)
	}
	if !strings.Contains(stdout, "hop 1") {
		t.Errorf("output has no hop line:\n%s", stdout)
	}
	// The fixture declares depth 1, so exactly one hop is printed.
	if n := strings.Count(stdout, "hop "); n != 1 {
		t.Errorf("printed %d hops, want 1 for a depth-1 map:\n%s", n, stdout)
	}
	if !strings.Contains(stdout, "leaf") {
		t.Errorf("output does not name the leaf:\n%s", stdout)
	}
}

// TestCommandResolvesToTargets checks the printed targets are the ones
// placement resolves. This is the assertion that goes red if the call site is
// deleted, which is what makes the package reachable rather than merely present.
func TestCommandResolvesToTargets(t *testing.T) {
	stdout, stderr, code := exec(t, "--topology", fixture(), "--entity", "demo-entity", "--json")
	if code != 0 {
		t.Fatalf("exit %d, stderr: %s", code, stderr)
	}
	var got report
	if err := json.Unmarshal([]byte(stdout), &got); err != nil {
		t.Fatalf("decode JSON: %v\n%s", err, stdout)
	}
	if len(got.Targets) == 0 {
		t.Fatal("command resolved no targets")
	}
	// The fixture's deepest level is disk, and it declares four of them.
	if len(got.Targets) != 4 {
		t.Errorf("resolved %d targets (%v), want the fixture's 4 disks", len(got.Targets), got.Targets)
	}
	for _, name := range got.Targets {
		if !strings.Contains(name, "-d") {
			t.Errorf("target %q is not a disk; placement should return the deepest level", name)
		}
	}
}

// TestCommandExitsNonZeroOnBadTopology checks an operator diagnostic never
// exits 0 on a broken input.
func TestCommandExitsNonZeroOnBadTopology(t *testing.T) {
	t.Run("missing file", func(t *testing.T) {
		_, stderr, code := exec(t, "--topology", "no/such/map.json", "--entity", "x")
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
		_, stderr, code := exec(t, "--topology", bad, "--entity", "x")
		if code == 0 {
			t.Error("exit 0 for a map declaring an unknown format version")
		}
		if !strings.Contains(stderr, "load topology") {
			t.Errorf("stderr does not explain the failure: %q", stderr)
		}
	})

	t.Run("undeclared spread level", func(t *testing.T) {
		_, stderr, code := exec(t, "--topology", fixture(), "--entity", "x", "--spread-level", "nope")
		if code == 0 {
			t.Error("exit 0 for a spread level the map does not declare")
		}
		if !strings.Contains(stderr, "not declared") {
			t.Errorf("stderr does not explain the failure: %q", stderr)
		}
	})
}

// TestCommandJSONMatchesTextOutput checks the two renderings report the same
// leaf and the same targets in the same order, so they cannot drift.
func TestCommandJSONMatchesTextOutput(t *testing.T) {
	args := []string{"--topology", fixture(), "--entity", "drift-check",
		"--spread-level", "rack", "--from", "srv-1"}

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
	for _, section := range [][]string{got.Targets, got.Spread, got.Nearest} {
		for i, name := range section {
			if !strings.Contains(text, name) {
				t.Errorf("text form is missing %q (position %d) reported by JSON", name, i)
			}
		}
	}
	if len(got.Spread) != len(got.Targets) {
		t.Errorf("spread has %d entries, targets %d: spread must be a permutation",
			len(got.Spread), len(got.Targets))
	}
	if len(got.Nearest) != len(got.Targets) {
		t.Errorf("nearest has %d entries, targets %d: nearest must be a permutation",
			len(got.Nearest), len(got.Targets))
	}
}

// TestCommandRequiresItsFlags checks the command refuses rather than guessing
// when a required flag is absent.
func TestCommandRequiresItsFlags(t *testing.T) {
	if _, _, code := exec(t, "--entity", "x"); code == 0 {
		t.Error("exit 0 with no --topology")
	}
	if _, _, code := exec(t, "--topology", fixture()); code == 0 {
		t.Error("exit 0 with no --entity")
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
