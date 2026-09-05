package certs

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"math"
	"os"
	"strings"
	"time"

	"github.com/atvirokodosprendimai/sdev1/internal/core/hlc"
	"github.com/atvirokodosprendimai/sdev1/internal/core/ports"
	"github.com/atvirokodosprendimai/sdev1/internal/core/temporal"
	"github.com/atvirokodosprendimai/sdev1/internal/core/tx"
)

// ErrNoSerial reports a denial that names no certificate.
var ErrNoSerial = errors.New("certs: a denial must name a serial")

// denyAttribute is how a denial is named as an attribute.
//
// ★ The ENTITY is the serial and the attribute is fixed, which is the opposite
// of ADR-033's grants — there the entity is the principal and the tenant lives in
// the attribute, because one principal has many independently retractable grants.
// A serial has exactly one thing that can be said about it.
const denyAttribute = "certificate:denied"

// serialPrefix keeps denial entities out of every other entity space.
//
// ⚠ Without it a denial's entity is a bare hex string, which is a name an
// ordinary entity could also have. Reading the denial set would then depend on
// nobody having chosen that name, which is not a property anything enforces.
const serialPrefix = "cert:"

// Entity is the reserved-tenant entity a serial's denial is filed under.
func Entity(serial string) string { return serialPrefix + serial }

// SerialOf reads the serial off a VERIFIED peer certificate.
//
// ⚠ From `VerifiedChains`, never `PeerCertificates`. The second is what the peer
// SENT — populated whether or not a chain was ever built — so a serial taken from
// it is a serial the peer chose, and denying it would deny nothing.
//
// ★ It returns the same spelling [FormatSerial] writes. One representation, used
// in both directions: a denial written as one and read as another denies nothing,
// silently, because both sides look right.
func SerialOf(state *tls.ConnectionState) (string, error) {
	if state == nil || len(state.VerifiedChains) == 0 || len(state.VerifiedChains[0]) == 0 {
		return "", fmt.Errorf("%w: no verified chain", ErrNoSerial)
	}
	return FormatSerial(state.VerifiedChains[0][0].SerialNumber), nil
}

// DenyDatom says that a certificate has stopped being believable.
//
// ⚠ `until` is the CERTIFICATE'S OWN EXPIRY, and it is required. A denial swept
// before then silently re-admits the certificate, and nothing about that looks
// like a change in access; a denial that ran forever would fill the reserved
// tenant with facts about certificates nobody can present. The expiry is in the
// certificate, so the retention rule is derivable rather than guessed.
//
// ★ An ordinary datom in the reserved tenant: bitemporal, retractable, and "who
// denied this and when" is answerable through the same history everything else
// uses. That is the whole reason this is not a CRL.
func DenyDatom(serial string, until time.Time, reason string, id tx.TxID, from int64) (ports.Datom, error) {
	if serial == "" {
		return ports.Datom{}, ErrNoSerial
	}
	if until.IsZero() {
		return ports.Datom{}, fmt.Errorf(
			"%w: a denial of %s must end when the certificate does", ErrNoSerial, serial)
	}
	return ports.Datom{
		Entity:    Entity(serial),
		Attribute: denyAttribute,
		Value:     []byte(reason),
		Valid:     temporal.Interval{From: from, To: until.Unix()},
		TxID:      id,
		Assert:    true,
	}, nil
}

// AllowDatom withdraws a denial.
//
// ★ A RETRACTION, not a deletion — ADR-033's rule, applied to a different fact.
// "This denial stopped applying" and "this certificate was never denied" are
// different statements, and only the first is true. The second would also lose
// the record that a key was once believed compromised, which is exactly what an
// auditor comes looking for.
func AllowDatom(serial string, until time.Time, id tx.TxID, from int64) (ports.Datom, error) {
	d, err := DenyDatom(serial, until, "", id, from)
	if err != nil {
		return ports.Datom{}, err
	}
	d.Assert = false
	return d, nil
}

// Denied reports whether a serial is currently denied.
//
// ⚠ `now` is the NODE's instant and never a caller's. A caller who chose the
// moment its certificate is judged at would simply name one before the denial —
// the same hole ADR-046 closed for grants, and it is available here for the same
// reason.
//
// ⚠ An unreadable denial store is an ERROR, never a pass. Failing open here would
// mean a compromised certificate is admitted precisely when the thing that would
// stop it is unreachable.
func Denied(ctx context.Context, r ports.Reader, serial string, now int64) (bool, string, error) {
	if serial == "" {
		return false, "", ErrNoSerial
	}

	at := ports.Snapshot{At: maxTxID(), ValidAt: now}
	datoms, err := r.Load(ctx, Entity(serial), at)
	if err != nil {
		return false, "", fmt.Errorf("certs: reading denials for %s: %w", serial, err)
	}

	// ports.Carried is the shared reduction: latest per attribute, retractions
	// suppressed. A withdrawn denial is therefore ABSENT rather than present and
	// false.
	carried := ports.Carried(datoms)
	d, ok := carried[denyAttribute]
	if !ok {
		return false, "", nil
	}
	return true, string(d.Value), nil
}

// ParseSerial normalises a serial an operator typed.
//
// ⚠ Case and separators are what a human copies wrong. A denial written for
// `AB:CD` and looked up as `abcd` denies nothing, and neither side looks wrong.
func ParseSerial(s string) (string, error) {
	cleaned := strings.ToLower(strings.NewReplacer(":", "", " ", "", "-", "").Replace(s))
	if cleaned == "" {
		return "", ErrNoSerial
	}
	if strings.TrimLeft(cleaned, "0123456789abcdef") != "" {
		return "", fmt.Errorf("%w: %q is not hexadecimal", ErrNoSerial, s)
	}
	if len(cleaned) > 40 {
		return "", fmt.Errorf("%w: %q is longer than a serial", ErrNoSerial, s)
	}
	return strings.Repeat("0", 40-len(cleaned)) + cleaned, nil
}

// Inspect reads a certificate file and returns the serial and expiry a denial
// needs.
//
// ★ It exists so an operator can deny a certificate they have a copy of without
// transcribing a forty-character hex string by hand — which is the step where a
// denial silently names the wrong certificate.
func Inspect(certFile string) (serial string, notAfter time.Time, err error) {
	body, err := os.ReadFile(certFile)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("certs: reading %s: %w", certFile, err)
	}
	block, _ := pem.Decode(body)
	if block == nil {
		return "", time.Time{}, fmt.Errorf("certs: %s holds no PEM", certFile)
	}
	leaf, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("certs: parsing %s: %w", certFile, err)
	}
	return FormatSerial(leaf.SerialNumber), leaf.NotAfter, nil
}

// maxTxID is the unbounded transaction axis: nothing can exceed a maximum clock
// reading, so it means "every transaction that has committed".
//
// ★ The same spelling `temporal.Query.Bounds` uses, so a denial written a
// millisecond ago is in force.
func maxTxID() tx.TxID {
	return tx.TxID{HLC: hlc.Timestamp{Wall: math.MaxInt64, Logical: math.MaxUint32}}
}
