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

	"github.com/atvirokodosprendimai/sdev1/internal/core/certs"
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
