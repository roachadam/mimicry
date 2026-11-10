package native

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strconv"

	"github.com/roachadam/mimicry/internal/fingerprint"
	"github.com/roachadam/mimicry/internal/handshake"
	"github.com/roachadam/mimicry/profile"
)

// echLookup is a running HTTPS record lookup for a connection's host
// (startECHLookup): its ECH config, nil for none the browser uses, or the
// error that ends the request.
type echLookup chan echResult

type echResult struct {
	config *handshake.ECHConfig
	err    error
}

// startECHLookup looks up the HTTPS record of req's host beside the dial, as
// the profile's browser does with its address lookups (tls.realEch), for an
// https:// request on a direct route; nil otherwise: through a proxy the
// browser looks nothing up, and sends the ClientHello as to any server.
func (e *Engine) startECHLookup(ctx context.Context, req *http.Request, r route) echLookup {
	if e.https == nil || r.proxy != nil || req.URL.Scheme != "https" {
		return nil
	}

	host := req.URL.Hostname()
	if net.ParseIP(host) != nil {
		return nil
	}

	port := 443
	if p := req.URL.Port(); p != "" {
		if n, err := strconv.Atoi(p); err == nil {
			port = n
		}
	}

	ch := make(echLookup, 1)

	go func() {
		config, err := e.echConfig(ctx, host, port)

		ch <- echResult{
			config: config,
			err:    err,
		}
	}()

	return ch
}

// wait returns the lookup's result once it has one, or ctx's error.
func (l echLookup) wait(ctx context.Context) (*handshake.ECHConfig, error) {
	if l == nil {
		return nil, nil //nolint:nilnil // no lookup: no config
	}

	select {
	case res := <-l:
		return res.config, res.err
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// echConfig is the ECH config the browser encrypts to for host's origin on
// port, from its HTTPS record (handshake.SelectECH). A failed lookup or
// none is no config: the browser connects as without one. A list that
// does not parse is no config, or for a browser that fails such a request
// (tls.realEch.malformed) an error wrapping handshake.ErrMalformedECH.
func (e *Engine) echConfig(ctx context.Context, host string, port int) (*handshake.ECHConfig, error) {
	rec, ok, err := e.https.Lookup(ctx, host, port)
	if err != nil || !ok || rec.ECH == nil {
		return nil, nil //nolint:nilerr,nilnil // as the browser: no record, no ECH
	}

	return e.selectECH(host, rec.ECH)
}

// selectECH picks the config of list, from host's record or its server's
// retry configs, that the browser encrypts to.
func (e *Engine) selectECH(host string, list []byte) (*handshake.ECHConfig, error) {
	r := e.plan.RealECH()

	config, err := handshake.SelectECH(list, r)

	switch {
	case !errors.Is(err, handshake.ErrMalformedECH):
		return config, err
	case r.Malformed == profile.MalformedFail:
		return nil, fmt.Errorf("mimicry: %s publishes an ECH config list that does not parse, and profile %s's browser connects to no such server (tls.realEch.malformed): %w", host, e.profile.Name, err)
	}

	return nil, nil //nolint:nilnil // the usual hello, as the browser sends
}

// echHello returns hello as the outer hello of a connection encrypting to
// config, or as it is without one.
func (e *Engine) echHello(hello fingerprint.Hello, config *handshake.ECHConfig) fingerprint.Hello {
	if config == nil {
		return hello
	}

	return e.plan.ECHHello(hello)
}
