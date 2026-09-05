// Command sdev1-ca mints the certificate authority a cluster trusts and issues
// the certificates its nodes and callers present.
//
// ⚠ RUN IT WHERE THE AUTHORITY KEY IS KEPT, WHICH IS NOT A NODE. It is a separate
// binary precisely so it need not be deployed: a node holding the signing key
// could mint peers, and one compromise would become all of them.
//
// ★ There is no issuance endpoint and there cannot be one. Every request to this
// system is authenticated by a client certificate, so an endpoint would have to
// authenticate a requester that does not have one yet. See
// docs/adr/ADR-047-certificate-lifecycle.md.
//
// ⚠ The authority's private key is written UNENCRYPTED, mode 0600. Protecting it
// is yours.
package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/urfave/cli/v3"

	"github.com/atvirokodosprendimai/sdev1/internal/core/authz"
	"github.com/atvirokodosprendimai/sdev1/internal/core/certs"
	"github.com/atvirokodosprendimai/sdev1/internal/core/hlc"
	"github.com/atvirokodosprendimai/sdev1/internal/core/leafstore"
	"github.com/atvirokodosprendimai/sdev1/internal/core/ports"
	"github.com/atvirokodosprendimai/sdev1/internal/core/tx"
)

func main() {
	cmd := &cli.Command{
		Name:  "sdev1-ca",
		Usage: "mint a certificate authority and issue certificates from it",
		Description: "Offline. Run this where the authority key lives — never on a node.\n\n" +
			"  sdev1-ca mint  --dir ./ca --name cluster-ca\n" +
			"  sdev1-ca issue --dir ./ca --out ./node-a --name node-a --host 127.0.0.1\n\n" +
			"`issue` writes the certificate, its key, and a COPY of the authority's\n" +
			"certificate. It never writes the authority's key.",
		Commands: []*cli.Command{
			{
				Name:  "mint",
				Usage: "create a new certificate authority",
				Flags: []cli.Flag{
					&cli.StringFlag{Name: "dir", Usage: "where the authority lives", Required: true},
					&cli.StringFlag{Name: "name", Usage: "the authority's common name", Required: true},
					&cli.DurationFlag{
						Name:  "life",
						Usage: "how long the authority is valid",
						Value: certs.DefaultAuthorityLife,
					},
				},
				Action: mint,
			},
			{
				Name:  "issue",
				Usage: "issue a certificate signed by the authority",
				Flags: []cli.Flag{
					&cli.StringFlag{Name: "dir", Usage: "the authority's directory", Required: true},
					&cli.StringFlag{Name: "out", Usage: "where to write the bundle", Required: true},
					&cli.StringFlag{
						Name:     "name",
						Usage:    "the subject's common name — this is the PRINCIPAL a node authorizes",
						Required: true,
					},
					&cli.StringSliceFlag{
						Name: "host",
						Usage: "a name or address this certificate will be reached at; " +
							"repeat for several. Omit for a caller that only dials",
					},
					&cli.DurationFlag{
						Name:  "life",
						Usage: "how long the certificate is valid",
						Value: certs.DefaultLeafLife,
					},
				},
				Action: issue,
			},
			{
				Name:  "deny",
				Usage: "stop believing one certificate, by serial",
				Description: "Writes a denial datom into the grant leaf — reserved tenant 0000.\n" +
					"⚠ By SERIAL, not by principal: a leaked key is ONE certificate, and its\n" +
					"holder must be able to carry on with a new one. To withdraw somebody's\n" +
					"ACCESS instead, retract their grant.\n\n" +
					"⚠ A denial only reaches the nodes whose grant leaf you write to. Nothing\n" +
					"replicates it yet.",
				Flags: []cli.Flag{
					&cli.StringFlag{
						Name:     "grants",
						Usage:    "the grant leaf's directory, as a node reads it",
						Required: true,
					},
					&cli.StringFlag{
						Name:  "serial",
						Usage: "the certificate's serial, as `issue` printed it",
					},
					&cli.StringFlag{
						Name: "cert",
						Usage: "read the serial and expiry from this certificate file instead " +
							"of typing them — a forty-character hex string copied by hand is " +
							"where a denial silently names the wrong certificate",
					},
					&cli.TimestampFlag{
						Name: "until",
						Usage: "when the denial stops applying. ⚠ Must be the CERTIFICATE'S own " +
							"expiry: swept earlier it silently re-admits the certificate. Taken " +
							"from --cert when that is given",
						Config: cli.TimestampConfig{Layouts: []string{time.RFC3339}},
					},
					&cli.StringFlag{Name: "reason", Usage: "why, for whoever reads this later"},
				},
				Action: deny,
			},
			{
				Name:  "allow",
				Usage: "withdraw a denial",
				Description: "★ A RETRACTION, not a deletion. The record that the certificate was\n" +
					"once denied survives, which is what an auditor comes looking for.",
				Flags: []cli.Flag{
					&cli.StringFlag{Name: "grants", Usage: "the grant leaf's directory", Required: true},
					&cli.StringFlag{Name: "serial", Usage: "the certificate's serial"},
					&cli.StringFlag{Name: "cert", Usage: "read the serial from this certificate file"},
					&cli.TimestampFlag{
						Name:   "until",
						Usage:  "the certificate's expiry, as the denial carried it",
						Config: cli.TimestampConfig{Layouts: []string{time.RFC3339}},
					},
				},
				Action: allow,
			},
		},
	}
	if err := cmd.Run(context.Background(), os.Args); err != nil {
		fmt.Fprintln(os.Stderr, "sdev1-ca:", err)
		os.Exit(1)
	}
}

func mint(_ context.Context, cmd *cli.Command) error {
	a, err := certs.Mint(cmd.String("dir"), cmd.String("name"), cmd.Duration("life"))
	if err != nil {
		return err
	}
	fmt.Printf("minted %s\n", a.Certificate().Subject.CommonName)
	fmt.Printf("  authority: %s/%s\n", a.Dir, certs.AuthorityCert)
	fmt.Printf("  key:       %s/%s  ⚠ never copy this to a node\n", a.Dir, certs.AuthorityKey)
	fmt.Printf("  expires:   %s\n", a.Certificate().NotAfter.UTC().Format(time.RFC3339))
	return nil
}

func issue(_ context.Context, cmd *cli.Command) error {
	a, err := certs.Load(cmd.String("dir"))
	if err != nil {
		return err
	}

	i, err := certs.Issue(a, cmd.String("out"), certs.Subject{
		Name:  cmd.String("name"),
		Hosts: cmd.StringSlice("host"),
		Life:  cmd.Duration("life"),
	})
	if err != nil {
		return err
	}

	fmt.Printf("issued %s\n", cmd.String("name"))
	fmt.Printf("  bundle:  %s  (%s, %s, %s)\n", i.Dir, certs.LeafCert, certs.LeafKey, certs.AuthorityCert)
	fmt.Printf("  serial:  %s\n", i.Serial)
	fmt.Printf("  expires: %s\n", i.NotAfter.UTC().Format(time.RFC3339))
	// ★ Printed because a serial nobody wrote down cannot be denied later.
	fmt.Printf("  recorded in %s/%s\n", a.Dir, certs.SerialLog)
	return nil
}

func deny(ctx context.Context, cmd *cli.Command) error {
	serial, until, err := target(cmd)
	if err != nil {
		return err
	}

	return write(ctx, cmd.String("grants"), func(id tx.TxID, at int64) (ports.Datom, error) {
		return certs.DenyDatom(serial, until, cmd.String("reason"), id, at)
	}, func() {
		fmt.Printf("denied %s until %s\n", serial, until.UTC().Format(time.RFC3339))
		fmt.Println("  ⚠ only on the nodes reading this grant leaf — nothing replicates it")
	})
}

func allow(ctx context.Context, cmd *cli.Command) error {
	serial, until, err := target(cmd)
	if err != nil {
		return err
	}

	return write(ctx, cmd.String("grants"), func(id tx.TxID, at int64) (ports.Datom, error) {
		return certs.AllowDatom(serial, until, id, at)
	}, func() {
		fmt.Printf("allowed %s again\n", serial)
		fmt.Println("  ★ a retraction — the record that it was denied survives")
	})
}

// target resolves which certificate a denial names, from a file or by hand.
//
// ⚠ `--cert` is the safer route and exists because a forty-character hex string
// copied by hand is where a denial silently names the wrong certificate.
func target(cmd *cli.Command) (serial string, until time.Time, err error) {
	if file := cmd.String("cert"); file != "" {
		return certs.Inspect(file)
	}

	serial, err = certs.ParseSerial(cmd.String("serial"))
	if err != nil {
		return "", time.Time{}, fmt.Errorf("--serial: %w", err)
	}
	until = cmd.Timestamp("until")
	if until.IsZero() {
		return "", time.Time{}, fmt.Errorf(
			"--until is required without --cert: a denial must end when the certificate " +
				"does, because one swept earlier silently re-admits it")
	}
	return serial, until, nil
}

// write appends one datom to the grant leaf and seals it.
func write(ctx context.Context, dir string,
	make func(tx.TxID, int64) (ports.Datom, error), report func()) error {

	store, err := leafstore.Open(dir, authz.SystemTenant.TenantSubtree())
	if err != nil {
		return fmt.Errorf("--grants: %w", err)
	}
	defer func() { _ = store.Close() }()

	now := time.Now().Unix()
	// ⚠ A wall-clock identifier. This command is not a node and mints nothing
	// from ADR-002's sequence; what matters is that a later denial sorts after an
	// earlier one, which a clock reading gives.
	d, err := make(tx.TxID{HLC: hlc.Timestamp{Wall: now}, Seq: 1}, now)
	if err != nil {
		return err
	}
	if err := store.Append(ctx, d); err != nil {
		return err
	}
	if err := store.Seal(ctx); err != nil {
		return err
	}
	report()
	return nil
}
