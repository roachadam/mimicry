// Command pins prints the verified certificate chain a site presents to a
// profile's browser, with each certificate's pins, and marks the one to
// pin (mimicry.FetchPins; no request is sent):
//
//	go run github.com/roachadam/mimicry/cmd/pins URL [-profile name]
//
// It recommends the intermediate nearest the root, which rotates less
// than the leaf. Pin a backup key too.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/url"
	"os"
	"text/tabwriter"
	"time"

	"github.com/roachadam/mimicry"
	"github.com/roachadam/mimicry/profiles"
)

func main() {
	if err := run(os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "pins:", err)
		os.Exit(2)
	}
}

// run parses args (the URL and -profile, in either order) and prints the
// chain to out.
func run(args []string, out io.Writer) error {
	fs := flag.NewFlagSet("pins", flag.ContinueOnError)
	name := fs.String("profile", profiles.Chrome().Name, "the browser profile whose TLS hello to send")
	timeout := fs.Duration("timeout", 30*time.Second, "how long to wait for the connection")
	fs.Usage = func() {
		_, _ = fmt.Fprintln(fs.Output(), "usage: pins URL [-profile name] [-timeout 30s]")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return err
	}
	rawURL := fs.Arg(0)
	// Flags may follow the URL too.
	if err := fs.Parse(fs.Args()[min(1, fs.NArg()):]); err != nil {
		return err
	}
	if rawURL == "" || fs.NArg() > 0 {
		fs.Usage()
		return errors.New("one URL is required")
	}
	u, err := url.Parse(rawURL)
	if err != nil {
		return err
	}
	if u.Scheme == "" {
		// A bare host name.
		if u, err = url.Parse("https://" + rawURL); err != nil {
			return err
		}
	}
	p, err := profiles.ByName(*name)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	chain, err := mimicry.FetchPins(ctx, mimicry.Config{
		Profile: p,
	}, u.String())
	if err != nil {
		return err
	}

	return printChain(out, u.Hostname(), chain)
}

// printChain writes chain as a table, the recommended pin marked, and the
// Config.Pins entry that pins host to it.
func printChain(out io.Writer, host string, chain []mimicry.CertPin) error {
	recommended := -1
	for i, c := range chain {
		if c.Role == mimicry.CertIntermediate {
			recommended = i
		}
	}
	// Writes to tw fail at Flush, which reports them.
	tw := tabwriter.NewWriter(out, 0, 4, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, "ROLE\tNOT AFTER\tSUBJECT\tPIN")
	for i, c := range chain {
		role := c.Role.String()
		if i == recommended {
			role += " *"
		}
		_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\tkey, sha256/base64: %s\n", role, c.NotAfter.Format(time.DateOnly), c.Subject, c.SPKI)
		_, _ = fmt.Fprintf(tw, "\t\t\tkey, hex: %s\n", c.SPKIHex)
		_, _ = fmt.Fprintf(tw, "\t\t\tcertificate, SHA-256: %s\n", c.Fingerprint)
	}
	if err := tw.Flush(); err != nil {
		return err
	}
	if recommended < 0 {
		_, err := fmt.Fprintln(out, "\nThe chain has no intermediate: pin the root's key, or the leaf's (it changes with renewals).")
		return err
	}
	_, err := fmt.Fprintf(out, "\n* the recommended pin: it rotates least often.\n\nConfig.Pins:\n\t{Host: %q, SPKI: []string{%q}}\n", host, chain[recommended].SPKI)

	return err
}
