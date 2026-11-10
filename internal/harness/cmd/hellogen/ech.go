package main

import (
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"

	"github.com/roachadam/mimicry/internal/harness/internal/capture"
	"github.com/roachadam/mimicry/internal/harness/internal/server"
	"github.com/roachadam/mimicry/profile"
)

// echGaps pin ech connections that differ from the rest of their browser's
// for timing reasons. Keys are "<capture file>: sample N, connection M";
// values say why.
var echGaps = map[string]string{
	"firefox-159-macos.ech.pagetrace.json.gz: sample 2, connection 8": "a second ClientHello with the config the server rejects without retry configs, 7 ms after the first and before the GREASE one: two connections racing for the request (1 of 3 samples); mimicry sends one",
}

// realECH derives tls.realEch from the ech capture, whose hosts publish
// ECH configs (server.ECHHostConfigs): which config and suite each
// connection used, the inner hello's shape and padding, the outer hello
// of a resumed inner one, what follows a rejection, and what a list that
// does not parse leads to; and from the ech-h3 capture, whether Alt-Svc
// moves such a host to HTTP/3 (altSvcIgnored). Every connection must
// follow the rules profile.RealECH states; anything else is an error,
// unless pinned in echGaps. Nil without an ech capture: not measured.
func realECH(dir string, p *profile.Spec) (*profile.RealECH, error) {
	source, _, err := capture.Inherited(p, server.ECH.Name)
	if err != nil {
		return nil, err
	}

	name := source + "." + server.ECH.Name + ".pagetrace.json.gz"
	path := filepath.Join(dir, name)

	if _, err := os.Stat(path); errors.Is(err, fs.ErrNotExist) { //nolint:gosec // a capture of the profile named on the command line
		return nil, nil //nolint:nilnil // nil: no capture, not measured
	}

	pages, err := capture.LoadPages(path)
	if err != nil {
		return nil, err
	}

	d := &echDerivation{
		t:     &p.TLS,
		inner: map[string][]profile.Code{},
	}

	var errs []error

	for si, page := range pages {
		if err := d.page(name, si+1, page); err != nil {
			errs = append(errs, err)
		}
	}

	if err := errors.Join(errs...); err != nil {
		return nil, err
	}

	r, err := d.result()
	if err != nil || r.Ignore {
		return r, err
	}

	if r.AltSvcIgnored, err = altSvcIgnored(dir, p); err != nil {
		return nil, err
	}

	return r, nil
}

// altSvcIgnored reads the ech-h3 captures, whose one host publishes an
// ECH config and advertises HTTP/3 through Alt-Svc on every response:
// true when the browser used the config on every connection and no QUIC
// connection reached the server, though every connection but the first
// followed an Alt-Svc; false without a capture, not measured. A QUIC
// connection is an error: HTTP/3 to a host whose ECH config the browser
// uses is not modelled.
func altSvcIgnored(dir string, p *profile.Spec) (bool, error) {
	files, err := server.ECHH3Captures(dir, p)
	if err != nil || len(files) == 0 {
		return false, err
	}

	var errs []error

	for _, f := range files {
		pages, err := capture.LoadPages(f.Path)
		if err != nil {
			return false, err
		}

		for si, page := range pages {
			at := fmt.Sprintf("%s: sample %d", filepath.Base(f.Path), si+1)

			if n := len(page.QUIC); n > 0 {
				errs = append(errs, fmt.Errorf("%s: %d QUIC connections to a host whose ECH config the browser uses, which is not modelled", at, n))
			}

			if len(page.Connections) < 2 {
				errs = append(errs, fmt.Errorf("%s: no connection after the first response's Alt-Svc", at))
			}

			for ci, conn := range page.Connections {
				if conn.ECH == nil || !conn.ECH.Accepted {
					errs = append(errs, fmt.Errorf("%s, connection %d: no accepted ECH, so it shows nothing of a host whose config the browser uses", at, ci+1))
				}
			}
		}
	}

	if err := errors.Join(errs...); err != nil {
		return false, err
	}

	return true, nil
}

// echDerivation collects what the ech capture's samples show.
type echDerivation struct {
	t *profile.TLS
	// used says some connection carried real ECH, grease that some
	// connection to a host with a config carried the GREASE extension
	// instead (after a rejection, or for a list that does not parse),
	// plain that one carried neither.
	used, plain bool
	r           profile.RealECH
	// inner is the first inner hello seen, and order its own extensions'
	// types by connection.
	first *profile.TLS
	inner map[string][]profile.Code
	// drops are each inner hello's left-out extensions, with its outer
	// hello's types: a resumed outer hello lacks some (tls.resumption.drop).
	drops []echDrop
	// malformed counts the samples with a connection to the host whose
	// list does not parse, of samples.
	malformed, samples int
	// retryDrop is what the hellos sent after a rejection left out, nil
	// before the first.
	retryDrop []profile.Code
	retried   bool
	// retryResume is what later offers to a host showed of the session
	// its connection with retry configs made: nil before one did.
	retryResume *bool
	// retryPlain says a later connection to such a host carried no real
	// ECH but resumed that session (tls.realEch.retryPlain).
	retryPlain bool
}

// page checks one sample's connections in order.
func (d *echDerivation) page(file string, sample int, page *capture.Page) error {
	d.samples++

	var (
		rejected     *capture.ECH
		rejectedConn *capture.Connection
		errs         []error
		bad          bool
	)

	// firstRejection is the payload length of each host's first rejected
	// offer, made without a session; a host is in it once a connection
	// with retry configs followed.
	firstRejection := map[string]uint16{}

	for ci, conn := range page.Connections {
		at := fmt.Sprintf("%s: sample %d, connection %d", file, sample, ci+1)
		ech := conn.ECH

		if ech != nil && !ech.Accepted {
			if rejected != nil {
				if _, pinned := echGaps[at]; !pinned {
					errs = append(errs, fmt.Errorf("%s: a second rejected offer before the browser connected again", at))
				}
			}

			rejected, rejectedConn = ech, &page.Connections[ci]

			continue
		}

		if rejected != nil && rejected.Retry && ech != nil {
			if err := d.offerAfterRetry(conn.Host, rejectedConn, firstRejection); err != nil {
				errs = append(errs, fmt.Errorf("%s: %w", at, err))
			}
		}

		configs, malformed, published := server.ECHHostConfigs(conn.Host)
		if !published {
			continue
		}

		bad = bad || malformed

		var err error

		// A connection with retry configs went to the host earlier in
		// the sample (offerAfterRetry).
		_, retried := firstRejection[conn.Host]

		switch {
		case ech != nil:
			err = d.accepted(&conn, configs, rejected)
		case malformed:
			err = d.malformedHello(&conn)
		default:
			err = d.withoutECH(&conn, rejected, retried)
		}

		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", at, err))
		}

		rejected = nil
	}

	if bad {
		d.malformed++
	}

	return errors.Join(errs...)
}

// accepted checks a connection whose inner hello the server decrypted:
// the config and suite it chose, then its inner and outer hellos.
func (d *echDerivation) accepted(conn *capture.Connection, configs []server.ECHConfigInfo, rejected *capture.ECH) error {
	ech := conn.ECH

	d.used = true

	config, ok := server.ECHConfigByID(ech.ConfigID)
	if !ok {
		return fmt.Errorf("config %#02x is not the ech page's", ech.ConfigID)
	}

	if ech.Inner == nil {
		return fmt.Errorf("config %#02x was accepted, but its inner hello was not kept", ech.ConfigID)
	}

	switch {
	case rejected != nil && !rejected.Retry:
		return fmt.Errorf("real ECH (config %#02x) after a rejection without retry configs", ech.ConfigID)
	case rejected != nil:
		if !config.Retry {
			return fmt.Errorf("config %#02x after a rejection with retry configs, not a retry config", ech.ConfigID)
		}

		if err := d.retryHello(conn.Hello); err != nil {
			return err
		}

	default:
		i := slices.IndexFunc(configs, func(c server.ECHConfigInfo) bool {
			return c.ID == ech.ConfigID
		})
		if i < 0 {
			return fmt.Errorf("config %#02x is not in %s's list", ech.ConfigID, conn.Host)
		}

		for _, skipped := range configs[:i] {
			addCode(&d.r.SkippedKEMs, skipped.KEM)
		}

		// A resumed inner hello's outer one is outerPSK's to check.
		if !innerResumes(ech.Inner) {
			if err := d.sameOuter(conn.Hello, false, nil); err != nil {
				return err
			}
		}
	}

	addCode(&d.r.KEMs, config.KEM)

	suite := profile.HPKESuite{
		KDF:  ech.KDF,
		AEAD: ech.AEAD,
	}
	if suite != config.Suites[0] {
		return fmt.Errorf("HPKE suite %s/%s from config %#02x, whose first is %s/%s", suite.KDF, suite.AEAD, ech.ConfigID, config.Suites[0].KDF, config.Suites[0].AEAD)
	}

	if !slices.Contains(d.r.CipherSuites, suite) {
		d.r.CipherSuites = append(d.r.CipherSuites, suite)
		slices.SortFunc(d.r.CipherSuites, profile.HPKESuite.Compare)
	}

	if err := d.innerHello(conn, config); err != nil {
		return err
	}

	return d.outerPSK(conn)
}

// innerResumes reports an inner hello offering a session.
func innerResumes(t *profile.TLS) bool {
	return extension(t, profile.ExtPreSharedKey) != nil
}

// innerHello checks conn's inner hello against the first one seen: its
// cipher suites and own extensions (their order may vary: a shuffle), its
// ech_outer_extensions against the outer hello, and its padding against
// RFC 9849's rule for config.
func (d *echDerivation) innerHello(conn *capture.Connection, config server.ECHConfigInfo) error {
	ech := conn.ECH
	in := ech.Inner

	var own []profile.Code

	for _, e := range in.Extensions {
		if e.Type != profile.ExtPreSharedKey {
			own = append(own, e.Type)
		}
	}

	if n := len(own); n == 0 || own[n-1] != profile.ExtECHOuterExtensions {
		return errors.New("the inner hello's own extensions do not end with ech_outer_extensions")
	}

	if d.first == nil {
		d.first = in
	} else {
		if !slices.Equal(in.CipherSuites, d.first.CipherSuites) {
			return fmt.Errorf("inner cipher suites %v, the first inner hello's %v", in.CipherSuites, d.first.CipherSuites)
		}

		if !sameSet(own, ownTypes(d.first)) {
			return fmt.Errorf("inner extensions %v, the first inner hello's %v", own, ownTypes(d.first))
		}

		for _, e := range in.Extensions {
			if w := extension(d.first, e.Type); w != nil && e.Type == profile.ExtSupportedVersions && !slices.Equal(e.Versions, w.Versions) {
				return fmt.Errorf("inner supported_versions %v, the first inner hello's %v", e.Versions, w.Versions)
			}
		}
	}

	d.inner[fmt.Sprint(own)] = own

	// ech_outer_extensions is the outer hello's list, in its order, but the
	// inner hello's own extensions, pre_shared_key and what it drops.
	var drop, compressed []profile.Code

	for _, e := range conn.Hello.Extensions {
		switch {
		case slices.Contains(own, e.Type) || e.Type == profile.ExtPreSharedKey:
		case slices.Contains(ech.OuterExtensions, e.Type):
			compressed = append(compressed, e.Type)
		default:
			addCode(&drop, e.Type)
		}
	}

	if !slices.Equal(compressed, ech.OuterExtensions) {
		return fmt.Errorf("ech_outer_extensions %v, not the outer hello's extensions in its order (%v)", ech.OuterExtensions, compressed)
	}

	d.drops = append(d.drops, echDrop{
		drop:  drop,
		outer: conn.Hello,
	})

	encoded, err := hex.DecodeString(ech.InnerHello)
	if err != nil {
		return err
	}

	if want := echPadding(len(encoded)-ech.Padding, len(conn.Host), config.MaxNameLength); ech.Padding != want {
		return fmt.Errorf("%d bytes of inner padding, not RFC 9849's %d (maximum_name_length %d)", ech.Padding, want, config.MaxNameLength)
	}

	return nil
}

// echPadding is RFC 9849's padding (section 6.1.3) of an encoded inner
// hello of length n naming a server of nameLength bytes, for a config's
// maximum_name_length.
func echPadding(n, nameLength, maxNameLength int) int {
	pad := max(0, maxNameLength-nameLength)

	return pad + 31 - (n+pad-1)%32
}

// outerPSK records what the outer hello of a resumed inner one carries.
func (d *echDerivation) outerPSK(conn *capture.Connection) error {
	if !innerResumes(conn.ECH.Inner) {
		return nil
	}

	got := profile.OuterPSKNone
	if extension(conn.Hello, profile.ExtPreSharedKey) != nil {
		got = profile.OuterPSKGREASE
	}

	if d.r.OuterPSK != "" && d.r.OuterPSK != got {
		return fmt.Errorf("an outer pre_shared_key %q, another connection's %q", got, d.r.OuterPSK)
	}

	d.r.OuterPSK = got

	return d.sameOuter(conn.Hello, got == profile.OuterPSKGREASE, d.t.Resumption.Drop)
}

// withoutECH checks a connection to a host with a config that carried no
// real ECH: after a rejection without retry configs, the GREASE extension
// and the real server_name; after a connection with retry configs to the
// host (retried), the same, resuming its session (plainAfterRetry);
// otherwise a browser that ignores configs.
func (d *echDerivation) withoutECH(conn *capture.Connection, rejected *capture.ECH, retried bool) error {
	greased := extension(conn.Hello, profile.ExtEncryptedClientHello) != nil

	switch {
	case rejected != nil && rejected.Retry:
		return errors.New("no ECH after a rejection with retry configs")
	case rejected != nil:
		if !greased {
			return errors.New("no GREASE extension after a rejection without retry configs")
		}

		return d.retryHello(conn.Hello)

	case greased && retried:
		return d.plainAfterRetry(conn)
	case greased:
		return errors.New("the GREASE extension to a host whose config was neither rejected nor malformed")
	}

	d.plain = true

	return nil
}

// plainAfterRetry checks a connection without real ECH to a host an
// earlier connection with retry configs reached: the usual resumed hello,
// the GREASE extension and the real server_name, offering a session.
func (d *echDerivation) plainAfterRetry(conn *capture.Connection) error {
	if d.t.Resumption == nil || extension(conn.Hello, profile.ExtPreSharedKey) == nil {
		return errors.New("the GREASE extension without a session, after a connection with retry configs to the host")
	}

	d.retryPlain = true

	return d.sameOuter(conn.Hello, true, d.t.Resumption.Drop)
}

// malformedHello checks a connection to the host whose list does not
// parse: the usual hello, with the GREASE extension.
func (d *echDerivation) malformedHello(conn *capture.Connection) error {
	if extension(conn.Hello, profile.ExtEncryptedClientHello) == nil {
		d.plain = true
		return nil
	}

	if d.r.Malformed != "" && d.r.Malformed != profile.MalformedGREASE {
		return errors.New("a connection to the host whose config list does not parse, in another sample none")
	}

	d.r.Malformed = profile.MalformedGREASE

	return d.sameOuter(conn.Hello, false, nil)
}

// retryHello records what the hello sent again after a rejection leaves
// out of the usual one; every such hello must leave out the same.
func (d *echDerivation) retryHello(h *profile.TLS) error {
	var missing []profile.Code

	for _, e := range d.t.Extensions {
		if extension(h, e.Type) == nil {
			addCode(&missing, e.Type)
		}
	}

	if d.retried && !slices.Equal(missing, d.retryDrop) {
		return fmt.Errorf("the hello after a rejection leaves out %v, another one %v", missing, d.retryDrop)
	}

	d.retried = true
	d.retryDrop = missing

	return d.sameOuter(h, false, missing)
}

// sameOuter checks that an outer hello's extensions are the profile's
// ClientHello's but drop (in its order, when that is fixed), with
// pre_shared_key last when psk.
func (d *echDerivation) sameOuter(h *profile.TLS, psk bool, drop []profile.Code) error {
	var want []profile.Code

	for _, e := range d.t.Extensions {
		if !slices.Contains(drop, e.Type) {
			want = append(want, e.Type)
		}
	}

	if psk {
		want = append(want, profile.ExtPreSharedKey)
	}

	got := make([]profile.Code, len(h.Extensions))
	for i, e := range h.Extensions {
		got[i] = e.Type
	}

	if d.t.ExtensionOrder == profile.ExtensionOrderFixed && !slices.Equal(got, want) ||
		d.t.ExtensionOrder != profile.ExtensionOrderFixed && !sameSet(got, want) {
		return fmt.Errorf("outer hello extensions %v, the profile's %v", got, want)
	}

	return nil
}

// offerAfterRetry reads a rejected offer, rejected, that the server's retry
// configs answered on a connection to host. The host's first such offer
// is made without a session; a later one, after a connection with retry
// configs gave the browser a session, shows whether the browser offers it:
// in the outer hello (a GREASE pre_shared_key) or, where the outer hello
// carries none, in a longer payload than the first offer's (the same
// inner hello but for pre_shared_key).
func (d *echDerivation) offerAfterRetry(host string, rejected *capture.Connection, first map[string]uint16) error {
	payload := echPayload(rejected.Hello)

	n, seen := first[host]
	if !seen {
		first[host] = payload
		return nil
	}

	offered := extension(rejected.Hello, profile.ExtPreSharedKey) != nil || payload > n
	if d.retryResume != nil && *d.retryResume != offered {
		return fmt.Errorf("an offer after a connection with retry configs offers its session %v, another one %v", offered, *d.retryResume)
	}

	d.retryResume = &offered

	return nil
}

// echPayload is the length of h's encrypted_client_hello payload.
func echPayload(h *profile.TLS) uint16 {
	e := extension(h, profile.ExtEncryptedClientHello)
	if e == nil || e.ECH == nil || len(e.ECH.PayloadLengths) == 0 {
		return 0
	}

	return e.ECH.PayloadLengths[0]
}

// result is tls.realEch from what the samples showed.
func (d *echDerivation) result() (*profile.RealECH, error) {
	if !d.used {
		if d.plain {
			return &profile.RealECH{
				Ignore: true,
			}, nil
		}

		return nil, errors.New("no connection to a host with an ECH config")
	}

	if d.plain {
		return nil, errors.New("real ECH on some connections, and on others to a host with a config neither ECH nor GREASE")
	}

	switch {
	case d.malformed == 0:
		d.r.Malformed = profile.MalformedFail
	case d.malformed != d.samples || d.r.Malformed == "":
		return nil, fmt.Errorf("a connection to the host whose config list does not parse in %d of %d samples", d.malformed, d.samples)
	}

	d.r.RetryDrop = d.retryDrop

	switch {
	case d.retryPlain && d.retryResume != nil:
		return nil, errors.New("after a connection with retry configs, a later connection to the host offered the config again in one place and carried no ECH in another")
	case d.retryPlain:
		d.r.RetryPlain = true
	case d.retryResume == nil:
		return nil, errors.New("no rejected offer followed a connection with retry configs, so whether its session is resumed is unmeasured")
	default:
		d.r.RetryResume = *d.retryResume
	}

	slices.Sort(d.r.KEMs)
	slices.Sort(d.r.SkippedKEMs)

	drop, err := d.innerDrop()
	if err != nil {
		return nil, err
	}

	first := d.first
	in := &profile.ECHInner{
		Drop: drop,
	}

	d.r.Inner = in

	in.CipherSuites = first.CipherSuites
	in.ExtensionOrder = profile.ExtensionOrderFixed

	if len(d.inner) > 1 {
		in.ExtensionOrder = profile.ExtensionOrderShuffled
	}

	for _, e := range first.Extensions {
		switch e.Type {
		case profile.ExtPreSharedKey:
			continue
		case profile.ExtEncryptedClientHello, profile.ExtECHOuterExtensions:
			e = profile.Extension{
				Type: e.Type,
			}

		default:
			// The rest keep their fields.
		}

		in.Extensions = append(in.Extensions, e)
	}

	if d.t.Resumption != nil && d.r.OuterPSK == "" {
		return nil, errors.New("no inner hello resumed a session, so the outer hello of a resumed one is unmeasured")
	}

	return &d.r, nil
}

// ownTypes are an inner hello's own extension types, pre_shared_key left
// out.
func ownTypes(t *profile.TLS) []profile.Code {
	var out []profile.Code

	for _, e := range t.Extensions {
		if e.Type != profile.ExtPreSharedKey {
			out = append(out, e.Type)
		}
	}

	return out
}

// sameSet reports whether a and b hold the same codes as many times each.
func sameSet(a, b []profile.Code) bool {
	a, b = slices.Clone(a), slices.Clone(b)
	slices.Sort(a)
	slices.Sort(b)

	return slices.Equal(a, b)
}

// addCode adds c to *codes unless it is there.
func addCode(codes *[]profile.Code, c profile.Code) {
	if !slices.Contains(*codes, c) {
		*codes = append(*codes, c)
	}
}

// echDrop is what an inner hello left out of its outer one.
type echDrop struct {
	drop  []profile.Code
	outer *profile.TLS
}

// innerDrop is every extension of the profile's ClientHello some inner
// hello left out, in the hello's order; each inner hello must have left
// out those of them its outer hello carried.
func (d *echDerivation) innerDrop() ([]profile.Code, error) {
	var all []profile.Code

	for _, e := range d.t.Extensions {
		if slices.ContainsFunc(d.drops, func(x echDrop) bool {
			return slices.Contains(x.drop, e.Type)
		}) {
			addCode(&all, e.Type)
		}
	}

	for _, x := range d.drops {
		var want []profile.Code

		for _, c := range all {
			if extension(x.outer, c) != nil {
				want = append(want, c)
			}
		}

		if !sameSet(x.drop, want) {
			return nil, fmt.Errorf("an inner hello leaves out %v of its outer one, others %v", x.drop, all)
		}
	}

	return all, nil
}
