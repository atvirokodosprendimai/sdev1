package serve_test

import (
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"math/big"
	"strings"
	"testing"
	"time"

	"github.com/atvirokodosprendimai/sdev1/internal/core/addr"
	"github.com/atvirokodosprendimai/sdev1/internal/core/certs"
	"github.com/atvirokodosprendimai/sdev1/internal/core/hlc"
	"github.com/atvirokodosprendimai/sdev1/internal/core/ports"
	"github.com/atvirokodosprendimai/sdev1/internal/core/routing"
	"github.com/atvirokodosprendimai/sdev1/internal/core/serve"
	"github.com/atvirokodosprendimai/sdev1/internal/core/tx"
)

// serialOf reads the serial out of a bundle the shared authority issued, which
// is what a denial names.
func serialOf(t *testing.T, conf serve.TLSConfig) (string, time.Time) {
	t.Helper()

	serial, notAfter, err := certs.Inspect(conf.CertFile)
	if err != nil {
		t.Fatalf("Inspect(%s): %v", conf.CertFile, err)
	}
	return serial, notAfter
}

// TestDenyingASerialReachesALiveConnection is ADR-047's falsifier.
//
// ★★ THE CONNECTION IS NEVER CLOSED. A client reads successfully, its
// certificate's serial is denied, and the SAME client reading over the SAME
// pooled connection is refused — with `Server.Accepted` unmoved, so nothing about
// a new handshake can explain it.
//
// ⚠ THE OBVIOUS IMPLEMENTATION FAILS ONLY HERE. Checking the denial at the
// handshake is cheaper, is once per connection, refuses before anything else
// runs, and passes every other test in this record. ADR-046 rule 8 made
// connections pooled and long-lived, so a handshake-only check leaves a stolen
// certificate reading for as long as the pool holds its connection — which is
// exactly "the revocation reported success and stopped nothing".
func TestDenyingASerialReachesALiveConnection(t *testing.T) {
	const (
		granted = int64(1_700_000_000)
		denied  = granted + 500
	)

	g := newGrantLeaf(t)
	g.grant(t, "reader-1", tenant(), granted)

	now, setNow := clockAt(denied - 1)

	id := tx.TxID{HLC: hlc.Timestamp{Wall: registered}, Seq: 1}
	srv, held := authorizedNode(t, g, now, ports.Datom{
		Entity: "planet-7", Attribute: "name", Value: []byte("Kepler"),
		Valid: forever(registered), TxID: id, Assert: true,
	})

	// ⚠ The client's certificate is the one that will be denied, so its serial
	// has to be knowable — the shared helper issues it and this reads it back.
	conf := sharedCA.issue(t, "reader-1")
	serial, notAfter := serialOf(t, conf)

	key := addr.KeyOf(tenant(), "planet-7")
	c := clientWithTLS(t, routing.Route{Prefix: held, NextHops: []string{srv.Addr()}, Epoch: 1}, 0, conf)

	run, err := c.Read(key, "READ name FROM planet-7", denied-1)
	if err != nil {
		t.Fatalf("a granted principal with a good certificate could not read: %v", err)
	}
	if len(run) != 1 || string(run[0].Value) != "Kepler" {
		t.Fatalf("the first read returned %v", run)
	}

	// ⚠ The connection is now WARM and stays warm across the denial. Without
	// this the next read would redial, and a handshake-time check would pass.
	if c.Idle(srv.Addr()) != 1 {
		t.Fatalf("the client holds %d idle connections, so the second read would redial "+
			"and this test would prove nothing", c.Idle(srv.Addr()))
	}
	before := srv.Accepted()

	// Deny the certificate. The grant is untouched: this is about the KEY.
	g.deny(t, serial, notAfter, "key on a lost laptop", denied)
	setNow(denied + 1)

	_, err = c.Read(key, "READ name FROM planet-7", denied+1)
	if err == nil {
		t.Fatal("a denied certificate still read, over a connection that was never closed.\n" +
			"The denial must be checked PER REQUEST. Checking it at the handshake is " +
			"cheaper and passes every other test here — and it leaves a stolen certificate " +
			"reading for as long as the pool holds its connection.")
	}
	if !strings.Contains(err.Error(), serve.ErrDeniedCertificate.Error()) {
		t.Errorf("the refusal was %v, which does not name ErrDeniedCertificate", err)
	}

	// ★ And it really was the same connection: no new handshake happened, so
	// nothing the TLS layer re-checked can explain the refusal.
	if got := srv.Accepted(); got != before {
		t.Errorf("%d new connection(s) were accepted between the two reads, so the denial "+
			"may have been noticed at handshake time rather than per request", got-before)
	}
}

// TestADeniedCertificateIsRefusedEvenWithAGrant separates the two facts.
//
// ★ "Your key is revoked" and "you have no grant" are different, with different
// remedies — a new certificate versus a grant — and a caller told the wrong one
// goes to the wrong person.
func TestADeniedCertificateIsRefusedEvenWithAGrant(t *testing.T) {
	const (
		granted = int64(1_700_000_000)
		denied  = granted + 100
	)

	g := newGrantLeaf(t)
	g.grant(t, "reader-1", tenant(), granted)

	now, _ := clockAt(denied + 1)
	id := tx.TxID{HLC: hlc.Timestamp{Wall: registered}, Seq: 1}
	srv, held := authorizedNode(t, g, now, ports.Datom{
		Entity: "planet-7", Attribute: "name", Value: []byte("Kepler"),
		Valid: forever(registered), TxID: id, Assert: true,
	})

	route := routing.Route{Prefix: held, NextHops: []string{srv.Addr()}, Epoch: 1}
	key := addr.KeyOf(tenant(), "planet-7")

	revoked := sharedCA.issue(t, "reader-1")
	serial, notAfter := serialOf(t, revoked)
	g.deny(t, serial, notAfter, "compromised", denied)

	// The grant is intact and the certificate is not.
	blocked := clientWithTLS(t, route, 0, revoked)
	if _, err := blocked.Read(key, "READ name FROM planet-7", denied+1); err == nil {
		t.Fatal("a denied certificate was served despite the grant being untouched")
	} else if !strings.Contains(err.Error(), serve.ErrDeniedCertificate.Error()) {
		t.Errorf("the refusal was %v, which does not name ErrDeniedCertificate", err)
	}

	// ★★ THE POSITIVE CONTROL, and it is the point of denying by serial: the SAME
	// principal, a DIFFERENT certificate, reads perfectly well. A leaked key is
	// one certificate, and its holder carries on with a new one.
	replacement := clientWithTLS(t, route, 0, sharedCA.issue(t, "reader-1"))
	if _, err := replacement.Read(key, "READ name FROM planet-7", denied+1); err != nil {
		t.Fatalf("the same principal's replacement certificate was refused: %v.\n"+
			"Denying by principal would punish the legitimate holder and block their "+
			"reissuance — which is why a denial names a serial.", err)
	}
}

// TestTheSerialComesFromTheVerifiedChain is ADR-047 rule 9's identity half.
//
// ⚠ `PeerCertificates` is what the peer SENT, populated whether or not a chain
// was ever built. A serial read from it is a serial the peer chose — so denying
// it would deny nothing at all, and the hole would be invisible because every
// honest caller sends the same value.
func TestTheSerialComesFromTheVerifiedChain(t *testing.T) {
	serial := big.NewInt(0xC0FFEE)
	claimed := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: "impostor"},
	}
	unverified := &tls.ConnectionState{PeerCertificates: []*x509.Certificate{claimed}}

	if got, err := certs.SerialOf(unverified); err == nil {
		t.Errorf("an unverified peer certificate produced serial %q; SerialOf must read "+
			"VerifiedChains, which is empty unless a chain was actually built", got)
	}
	if _, err := certs.SerialOf(nil); err == nil {
		t.Error("SerialOf(nil) produced a serial")
	}

	verified := &tls.ConnectionState{
		VerifiedChains: [][]*x509.Certificate{{claimed}},
	}
	got, err := certs.SerialOf(verified)
	if err != nil {
		t.Fatalf("SerialOf(verified): %v", err)
	}
	if want := certs.FormatSerial(serial); got != want {
		t.Errorf("serial = %q, want %q", got, want)
	}

	// ★ And an Identity carries BOTH off one chain, so the principal and the
	// serial can never disagree about which certificate was presented.
	who, err := serve.IdentityOf(verified)
	if err != nil {
		t.Fatalf("IdentityOf: %v", err)
	}
	if who.Principal != "impostor" || who.Serial != certs.FormatSerial(serial) {
		t.Errorf("identity = %+v, want both fields from the same leaf", who)
	}
}
