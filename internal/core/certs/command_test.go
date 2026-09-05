package certs_test

import (
	"crypto/x509"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/atvirokodosprendimai/sdev1/internal/core/certs"
)

// verifyAgainst is the options a leaf issued for a node should satisfy.
func verifyAgainst(pool *x509.CertPool) x509.VerifyOptions {
	return x509.VerifyOptions{
		Roots: pool,
		KeyUsages: []x509.ExtKeyUsage{
			x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth,
		},
	}
}

// TestTheCommandIssuesWhatTheTransportAccepts closes rung 4 for `cmd/sdev1-ca`.
//
// ★ Every other test here calls the library. That leaves `main` — the flag names,
// the verbs, the wiring — covered by nothing, and a `go build` would prove only
// that it compiles. This runs it, twice, and then hands what it produced to the
// thing ADR-046 built.
//
// ⚠ The second `mint` is the important half: a command that overwrote an
// authority would produce this test's own passing output and destroy a cluster.
func TestTheCommandIssuesWhatTheTransportAccepts(t *testing.T) {
	if testing.Short() {
		t.Skip("builds and runs a command")
	}

	binary := filepath.Join(t.TempDir(), "sdev1-ca")
	build := exec.Command("go", "build", "-o", binary, "../../../cmd/sdev1-ca")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("building cmd/sdev1-ca: %v\n%s", err, out)
	}

	dir := t.TempDir()
	ca := filepath.Join(dir, "ca")
	out := filepath.Join(dir, "node-a")

	run := func(args ...string) (string, error) {
		cmd := exec.Command(binary, args...)
		combined, err := cmd.CombinedOutput()
		return string(combined), err
	}

	if got, err := run("mint", "--dir", ca, "--name", "cluster-ca"); err != nil {
		t.Fatalf("mint: %v\n%s", err, got)
	}
	keyPath := filepath.Join(ca, certs.AuthorityKey)
	before, err := os.ReadFile(keyPath)
	if err != nil {
		t.Fatalf("reading the authority key: %v", err)
	}

	// ⚠ A second mint must refuse AND leave the key alone.
	got, err := run("mint", "--dir", ca, "--name", "cluster-ca")
	if err == nil {
		t.Fatalf("a second mint succeeded:\n%s", got)
	}
	if !strings.Contains(got, "already exists") {
		t.Errorf("the refusal did not say why:\n%s", got)
	}
	after, err := os.ReadFile(keyPath)
	if err != nil {
		t.Fatalf("reading the authority key after: %v", err)
	}
	if string(before) != string(after) {
		t.Fatal("the command replaced the authority key despite refusing")
	}

	if got, err := run("issue", "--dir", ca, "--out", out,
		"--name", "node-a", "--host", "127.0.0.1", "--host", "localhost"); err != nil {
		t.Fatalf("issue: %v\n%s", err, got)
	}

	// ★★ The bundle holds no authority KEY. A node given one could mint peers.
	if _, err := os.Stat(filepath.Join(out, certs.AuthorityKey)); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the issued bundle contains the authority key (stat err %v)", err)
	}

	// ★ And the transport accepts it — which is the point of issuing at all.
	leaf, pool := parse(t, out)
	if _, err := leaf.Verify(verifyAgainst(pool)); err != nil {
		t.Fatalf("the command issued a certificate the transport rejects: %v", err)
	}
	if leaf.Subject.CommonName != "node-a" {
		t.Errorf("the issued certificate names %q, want node-a", leaf.Subject.CommonName)
	}

	// The serial reached the operator, in both the output and the log.
	log, err := os.ReadFile(filepath.Join(ca, certs.SerialLog))
	if err != nil {
		t.Fatalf("reading the issuance log: %v", err)
	}
	if !strings.Contains(string(log), certs.FormatSerial(leaf.SerialNumber)) {
		t.Errorf("the issued serial is not in %s — a serial nobody wrote down cannot "+
			"be denied later", certs.SerialLog)
	}
}
