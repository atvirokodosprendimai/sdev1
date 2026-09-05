package certs_test

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/atvirokodosprendimai/sdev1/internal/core/certs"
	"github.com/atvirokodosprendimai/sdev1/internal/core/serve"
)

// mint makes an authority, failing the test rather than the caller.
func mint(t *testing.T, name string) *certs.Authority {
	t.Helper()

	a, err := certs.Mint(t.TempDir(), name, certs.DefaultAuthorityLife)
	if err != nil {
		t.Fatalf("Mint(%q): %v", name, err)
	}
	return a
}

// issue signs a leaf into its own directory.
func issue(t *testing.T, a *certs.Authority, name string) certs.Issued {
	t.Helper()

	i, err := certs.Issue(a, t.TempDir(), certs.Subject{
		Name: name, Hosts: []string{"localhost", "127.0.0.1"},
	})
	if err != nil {
		t.Fatalf("Issue(%q): %v", name, err)
	}
	return i
}

// TestAnExistingAuthorityIsNeverOverwritten is ADR-047 rule 2.
//
// ⚠ The BYTE COMPARISON is the assertion, not the error. An implementation that
// generated a key, truncated the file and then noticed would return the same
// error and have already invalidated every certificate ever issued — and the
// outage would arrive later, elsewhere, looking like a network problem.
func TestAnExistingAuthorityIsNeverOverwritten(t *testing.T) {
	dir := t.TempDir()

	first, err := certs.Mint(dir, "cluster-ca", certs.DefaultAuthorityLife)
	if err != nil {
		t.Fatalf("the first Mint: %v", err)
	}
	keyPath := filepath.Join(dir, certs.AuthorityKey)
	before, err := os.ReadFile(keyPath)
	if err != nil {
		t.Fatalf("reading the key: %v", err)
	}

	second, err := certs.Mint(dir, "cluster-ca", certs.DefaultAuthorityLife)
	if !errors.Is(err, certs.ErrAuthorityExists) {
		t.Fatalf("a second Mint = %v, want ErrAuthorityExists", err)
	}
	if second != nil {
		t.Error("a refused Mint returned an authority alongside its error")
	}

	after, err := os.ReadFile(keyPath)
	if err != nil {
		t.Fatalf("reading the key after: %v", err)
	}
	if string(before) != string(after) {
		t.Fatal("the authority key CHANGED despite the refusal.\n" +
			"Every certificate ever issued from the old key is now unverifiable, and the " +
			"failure will not appear here — it appears later, on every node at once.")
	}

	// ★ And the original still works, which is the thing the byte comparison is
	// standing in for.
	if _, err := certs.Issue(first, t.TempDir(), certs.Subject{Name: "node-a"}); err != nil {
		t.Errorf("the surviving authority could not issue: %v", err)
	}
}

// TestAnIssuedCertificateNamesItsPrincipal is ADR-047 rule 4 meeting ADR-046
// rule 1.
//
// ★ Asserted through `serve.PrincipalOf` against a VERIFIED chain, not by
// re-reading the field this package just wrote. Re-reading it would prove the
// struct round-trips; this proves the transport agrees about who the certificate
// names.
func TestAnIssuedCertificateNamesItsPrincipal(t *testing.T) {
	a := mint(t, "cluster-ca")
	i := issue(t, a, "reader-1")

	leaf, pool := parse(t, i.Dir)
	chains, err := leaf.Verify(x509.VerifyOptions{
		Roots:     pool,
		KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	})
	if err != nil {
		t.Fatalf("the issued certificate does not verify: %v", err)
	}

	principal, err := serve.PrincipalOf(&tls.ConnectionState{VerifiedChains: chains})
	if err != nil {
		t.Fatalf("PrincipalOf: %v", err)
	}
	if principal != "reader-1" {
		t.Errorf("the transport reads the principal as %q, want reader-1", principal)
	}

	// The serial the caller was handed is the serial in the certificate — which
	// is what a denial will name.
	if got := certs.FormatSerial(leaf.SerialNumber); got != i.Serial {
		t.Errorf("Issued.Serial is %s, the certificate carries %s", i.Serial, got)
	}
}

// TestAnIssuedCertificateVerifiesAgainstItsAuthorityAndNoOther is rules 4 and 5.
//
// ⚠ The SECOND authority is what makes this a test. Verified against a pool
// holding its own CA, a leaf proves that CA is in the pool and nothing else; an
// implementation that signed with the wrong key would fail here and only here.
func TestAnIssuedCertificateVerifiesAgainstItsAuthorityAndNoOther(t *testing.T) {
	ours := mint(t, "cluster-ca")
	theirs := mint(t, "some-other-ca")
	i := issue(t, ours, "node-a")

	leaf, ourPool := parse(t, i.Dir)
	if _, err := leaf.Verify(x509.VerifyOptions{Roots: ourPool}); err != nil {
		t.Fatalf("a leaf did not verify against its own authority: %v", err)
	}

	otherPool := x509.NewCertPool()
	otherPEM, err := os.ReadFile(filepath.Join(theirs.Dir, certs.AuthorityCert))
	if err != nil {
		t.Fatalf("reading the other authority: %v", err)
	}
	if !otherPool.AppendCertsFromPEM(otherPEM) {
		t.Fatal("the other authority holds no certificate")
	}
	if _, err := leaf.Verify(x509.VerifyOptions{Roots: otherPool}); err == nil {
		t.Error("a leaf verified against an authority that did not sign it")
	}

	// ★★ And the bundle a node is given contains NO authority key. A node holding
	// one could mint peers, and one compromise would become all of them.
	if _, err := os.Stat(filepath.Join(i.Dir, certs.AuthorityKey)); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the issued bundle contains %s (stat err %v).\n"+
			"A node must never hold the key that signs its peers.", certs.AuthorityKey, err)
	}
	// It does contain the authority's certificate, so the bundle stands alone.
	if _, err := os.Stat(filepath.Join(i.Dir, certs.AuthorityCert)); err != nil {
		t.Errorf("the issued bundle has no %s, so a node cannot check its peers: %v",
			certs.AuthorityCert, err)
	}
}

// TestASubjectIsRequired is rule 4's refusal.
func TestASubjectIsRequired(t *testing.T) {
	a := mint(t, "cluster-ca")

	if _, err := certs.Issue(a, t.TempDir(), certs.Subject{}); !errors.Is(err, certs.ErrNoSubject) {
		t.Errorf("Issue with no subject = %v, want ErrNoSubject.\n"+
			"ADR-046 refuses a nameless certificate at the handshake, which is correct and "+
			"far too late — by then it has been deployed.", err)
	}
	if _, err := certs.Mint(t.TempDir(), "", certs.DefaultAuthorityLife); !errors.Is(err, certs.ErrNoSubject) {
		t.Errorf("Mint with no name = %v, want ErrNoSubject", err)
	}
}

// parse reads an issued bundle back as a leaf and the pool that should verify it.
func parse(t *testing.T, dir string) (*x509.Certificate, *x509.CertPool) {
	t.Helper()

	pair, err := tls.LoadX509KeyPair(filepath.Join(dir, certs.LeafCert), filepath.Join(dir, certs.LeafKey))
	if err != nil {
		t.Fatalf("loading the issued pair: %v", err)
	}
	leaf, err := x509.ParseCertificate(pair.Certificate[0])
	if err != nil {
		t.Fatalf("parsing the leaf: %v", err)
	}

	pool := x509.NewCertPool()
	caPEM, err := os.ReadFile(filepath.Join(dir, certs.AuthorityCert))
	if err != nil {
		t.Fatalf("reading the bundled authority: %v", err)
	}
	if !pool.AppendCertsFromPEM(caPEM) {
		t.Fatal("the bundled authority holds no certificate")
	}
	return leaf, pool
}

// TestAnIssuedCertificateIsUsableRightAway guards the clock-skew backdating.
//
// ⚠ A certificate whose NotBefore is "now" is not yet valid on a peer whose clock
// is a second behind, and the failure reads as an untrusted certificate rather
// than as a clock problem.
func TestAnIssuedCertificateIsUsableRightAway(t *testing.T) {
	a := mint(t, "cluster-ca")
	i := issue(t, a, "node-a")

	leaf, pool := parse(t, i.Dir)
	if _, err := leaf.Verify(x509.VerifyOptions{
		Roots:       pool,
		CurrentTime: time.Now().Add(-30 * time.Minute),
	}); err != nil {
		t.Errorf("a freshly issued certificate is not valid to a peer 30 minutes behind: %v", err)
	}
	if i.NotAfter.Before(time.Now().Add(certs.DefaultLeafLife - time.Hour)) {
		t.Errorf("NotAfter is %s, sooner than the default life allows", i.NotAfter)
	}
}
