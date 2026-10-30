// Command hellogen derives the shape of a browser's ClientHello (record and
// legacy versions, session ID length, key share lengths and X25519 key
// reuse, ECH) and how it cuts a new HTTP/2 connection's opening into TLS
// records from its captures, and writes them into the profile and the
// capture's JSON.
//
//	go run ./cmd/hellogen -captures testdata/captures -profile ../../profiles/data/chrome-155-macos.json
//
// GREASE ECH draws suites and payload lengths per connection, so their sets
// gather every connection of every capture the profile is checked against.
// It refuses samples that disagree beyond that shape.
package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/roachadam/mimicry/internal/harness/internal/capture"
	"github.com/roachadam/mimicry/internal/harness/internal/diff"
	"github.com/roachadam/mimicry/internal/harness/internal/server"
	"github.com/roachadam/mimicry/profile"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "hellogen:", err)
		os.Exit(1)
	}
}

func run() error {
	captures := flag.String("captures", "testdata/captures", "directory of captures (testdata/captures, or a refresh overlay); the profile's name picks its files")
	path := flag.String("profile", "", "profile JSON to update (required); its name picks the captures")

	flag.Parse()

	if *path == "" {
		flag.Usage()
		os.Exit(2)
	}

	data, err := os.ReadFile(*path)
	if err != nil {
		return err
	}

	p, err := profile.Decode(data)
	if err != nil {
		return err
	}

	source := capture.Source(p, capture.DefaultScenario)
	capturePath := filepath.Join(*captures, source+".json")

	captured, err := os.ReadFile(capturePath) //nolint:gosec // the capture of the profile named on the command line
	if err != nil {
		return err
	}

	out, outCapture, err := regenerate(data, captured, *captures)
	if err != nil {
		return err
	}

	if err := os.WriteFile(*path, out, 0o600); err != nil { //nolint:gosec // rewriting the profile named on the command line is the point
		return err
	}

	if source != p.Name {
		// Another profile's capture: its own run of hellogen writes it.
		return nil
	}

	return os.WriteFile(capturePath, outCapture, 0o600) //nolint:gosec // and its capture's profile JSON
}

// regenerate returns the profile and default-page capture JSON with the
// derived ClientHello shape. Validation runs after the shape is in, since a
// profile without one cannot pass.
func regenerate(data, captured []byte, dir string) ([]byte, []byte, error) {
	p, err := profile.Decode(data)
	if err != nil {
		return nil, nil, err
	}

	c, err := profile.Decode(captured)
	if err != nil {
		return nil, nil, fmt.Errorf("capture: %w", err)
	}

	derived, err := derive(dir, p)
	if err != nil {
		return nil, nil, err
	}

	if err := apply(&p.TLS, derived); err != nil {
		return nil, nil, fmt.Errorf("profile: %w", err)
	}

	if err := checkX25519Reuse(dir, p); err != nil {
		return nil, nil, fmt.Errorf("tls.x25519Reuse: %w", err)
	}

	if p.HTTP2 != nil {
		if p.HTTP2.OpeningRecords, err = capture.OpeningRecords(dir, p); err != nil {
			return nil, nil, fmt.Errorf("http2.openingRecords: %w", err)
		}
	}

	if p.TLS.WebSocketH1, err = websocketHello(dir, p); err != nil {
		return nil, nil, fmt.Errorf("tls.websocketH1: %w", err)
	}

	if p.TLS.QUIC, err = quicHello(dir, p); err != nil {
		return nil, nil, fmt.Errorf("tls.quic: %w", err)
	}

	if err := apply(&c.TLS, derived); err != nil {
		return nil, nil, fmt.Errorf("capture: %w", err)
	}

	if err := p.Validate(); err != nil {
		return nil, nil, err
	}

	out, err := profile.Encode(p)
	if err != nil {
		return nil, nil, err
	}

	outCapture, err := profile.Encode(c)
	if err != nil {
		return nil, nil, err
	}

	return out, outCapture, nil
}

// shape is what derive measures: the full ClientHello, its ECH sets taken
// from every full handshake, and the payload lengths of resumed hellos'
// GREASE ECH (nil if no connection resumed with a TLS 1.3 ticket).
type shape struct {
	full              *profile.TLS
	resumedECHLengths []uint16
}

// derive parses every sample of the default page's .clienthello and
// combines them, then folds in the GREASE ECH choices of every page
// capture's connections.
func derive(dir string, p *profile.Profile) (*shape, error) {
	path := filepath.Join(dir, capture.Source(p, capture.DefaultScenario)+".clienthello")

	hellos, err := capture.ReadClientHellos(path)
	if err != nil {
		return nil, err
	}

	samples := make([]*capture.Capture, len(hellos))
	for i, h := range hellos {
		s := &capture.Capture{
			ClientHello: h,
		}
		if h[0] != 0x01 {
			s.Records = h
			if _, s.ClientHello, err = server.ParseRecords(h); err != nil {
				return nil, fmt.Errorf("%s: sample %d: %w", path, i+1, err)
			}
		}

		if s.Profile.TLS, err = server.ParseClientHello(s.ClientHello); err != nil {
			return nil, fmt.Errorf("%s: sample %d: %w", path, i+1, err)
		}

		samples[i] = s
	}

	combined, err := capture.Combine(samples)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}

	sh := &shape{
		full: &combined.Profile.TLS,
	}

	pages, err := capture.Files(dir, p, ".pagetrace.json.gz")
	if err != nil {
		return nil, err
	}

	for _, f := range pages {
		loaded, err := capture.LoadPages(f.Path)
		if err != nil {
			return nil, err
		}

		for i, page := range loaded {
			for j, conn := range page.Connections {
				if err := sh.add(&conn); err != nil {
					return nil, fmt.Errorf("%s: sample %d, connection %d: %w", f.Path, i+1, j+1, err)
				}
			}
		}
	}

	slices.Sort(sh.resumedECHLengths)

	sh.resumedECHLengths = slices.Compact(sh.resumedECHLengths)

	return sh, nil
}

// add folds one page connection's ClientHello into the shape: its record
// version, legacy_version, session ID and key share lengths must be the
// samples'; its GREASE ECH cipher suite joins the full hello's set, and its
// payload length the full or the resumed hello's set. Connections captured
// before the shape was recorded (no legacy_version) carry nothing to add.
func (sh *shape) add(conn *capture.Connection) error {
	h := conn.Hello
	if h == nil || h.LegacyVersion == 0 {
		return nil
	}

	full := sh.full
	if conn.RecordVersion != 0 && full.RecordVersion != 0 && profile.Code(conn.RecordVersion) != full.RecordVersion {
		return fmt.Errorf("record version %#04x, the samples' %s", conn.RecordVersion, full.RecordVersion)
	}

	if h.LegacyVersion != full.LegacyVersion || h.SessionIDLength != full.SessionIDLength {
		return fmt.Errorf("legacyVersion %s with a %d-byte session ID, the samples' %s with %d bytes",
			h.LegacyVersion, h.SessionIDLength, full.LegacyVersion, full.SessionIDLength)
	}

	if got, want := extension(h, profile.ExtKeyShare), extension(full, profile.ExtKeyShare); (got == nil) != (want == nil) ||
		got != nil && !slices.Equal(got.KeyShareLengths, want.KeyShareLengths) {
		return fmt.Errorf("key share lengths %v, the samples' %v", lengths(got), lengths(want))
	}

	got, want := extension(h, profile.ExtEncryptedClientHello), extension(full, profile.ExtEncryptedClientHello)
	if (got == nil) != (want == nil) {
		return fmt.Errorf("encrypted_client_hello present %v, in the samples %v", got != nil, want != nil)
	}

	if got == nil {
		return nil
	}

	if got.ECH.EncLength != want.ECH.EncLength {
		return fmt.Errorf("ECH encLength %d, the samples' %d", got.ECH.EncLength, want.ECH.EncLength)
	}

	// Combine gave the combined hello an ECH of its own (mergeECH): its
	// sets grow in place.
	ech := want.ECH

	ech.CipherSuites = append(ech.CipherSuites, got.ECH.CipherSuites...)
	slices.SortFunc(ech.CipherSuites, profile.HPKESuite.Compare)

	ech.CipherSuites = slices.Compact(ech.CipherSuites)

	if extension(h, profile.ExtPreSharedKey) != nil {
		sh.resumedECHLengths = append(sh.resumedECHLengths, got.ECH.PayloadLengths...)
		return nil
	}

	ech.PayloadLengths = append(ech.PayloadLengths, got.ECH.PayloadLengths...)
	slices.Sort(ech.PayloadLengths)

	ech.PayloadLengths = slices.Compact(ech.PayloadLengths)

	return nil
}

func lengths(e *profile.Extension) []uint16 {
	if e == nil {
		return nil
	}

	return e.KeyShareLengths
}

// apply copies the derived shape into t, after checking that t is, shape
// aside, the ClientHello the samples show (in t's extension order). A t
// that resumes TLS 1.3 sessions with GREASE ECH takes the resumed hellos'
// payload lengths, which some connection must have measured.
func apply(t *profile.TLS, sh *shape) error {
	derived := sh.full

	t.RecordVersion = derived.RecordVersion
	t.LegacyVersion = derived.LegacyVersion
	t.SessionIDLength = derived.SessionIDLength
	// From the samples' key bytes, which Combine required to agree.
	t.X25519Reuse = derived.X25519Reuse

	for i := range t.Extensions {
		e := &t.Extensions[i]
		if e.Type != profile.ExtKeyShare && e.Type != profile.ExtEncryptedClientHello {
			continue
		}

		d := extension(derived, e.Type)
		if d == nil {
			return fmt.Errorf("extension %s is not in the captured ClientHellos", e.Type)
		}

		e.KeyShareLengths = d.KeyShareLengths
		e.ECH = d.ECH
	}

	if r := t.Resumption; r != nil && extension(t, profile.ExtEncryptedClientHello) != nil {
		if len(sh.resumedECHLengths) == 0 {
			return errors.New("tls.resumption: no captured connection resumed with a TLS 1.3 ticket, so its GREASE ECH payload lengths are unmeasured")
		}

		r.ECHPayloadLengths = sh.resumedECHLengths
	}

	want := profile.Profile{
		TLS: *derived,
	}
	got := profile.Profile{
		TLS: *t,
	}

	got.TLS.Resumption = nil

	if diffs := diff.Compare(&want, &got); len(diffs) > 0 {
		lines := make([]string, len(diffs))
		for i, d := range diffs {
			lines[i] = d.String()
		}

		return errors.New("its ClientHello is not the captured one:\n" + strings.Join(lines, "\n"))
	}

	return nil
}

func extension(t *profile.TLS, typ profile.Code) *profile.Extension {
	for i := range t.Extensions {
		if t.Extensions[i].Type == typ {
			return &t.Extensions[i]
		}
	}

	return nil
}
