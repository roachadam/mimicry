// Package convert turns mimicry-core's protocol messages (internal/rpc/pb)
// into mimicry's public types and back (decision 0069): a Config with its
// callbacks as data, a request and its options, a response, a plan, a
// connection's TLS state, cookies, errors as codes, and the framing of a
// WebSocket's messages inside DATA. It holds no state: the objects a
// message names by id are the server's to resolve.
package convert

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"io"
	"maps"
	"net"
	"net/http"
	"net/netip"
	"net/textproto"
	"net/url"
	"slices"
	"strings"

	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/roachadam/mimicry"
	"github.com/roachadam/mimicry/internal/rpc/pb"
	"github.com/roachadam/mimicry/profile"
)

// Config returns c's data as a mimicry.Config: everything but its
// objects (Profile, Cookies, ClientHintStore, AltSvc, Tickets), its
// VerifyConnection and its KeyLogWriter, which name things the server
// holds.
func Config(c *pb.Config) (mimicry.Config, error) {
	cfg := mimicry.Config{
		DialTimeout:          c.GetDialTimeout().AsDuration(),
		Timeout:              c.GetTimeout().AsDuration(),
		DisableDecompression: c.GetDisableDecompression(),
		DisableHTTP3:         c.GetDisableHttp3(),
		BrowserQUICFallback:  c.GetBrowserQuicFallback(),
		BrowserDialDelay:     c.GetBrowserDialDelay(),
		BrowserProxyAuth:     c.GetBrowserProxyAuth(),
		DisableCoalescing:    c.GetDisableCoalescing(),
		MaxConnsPerHost:      int(c.GetMaxConnsPerHost()),
		IdleConnTimeout:      c.GetIdleConnTimeout().AsDuration(),
		MaxIdleProxies:       int(c.GetMaxIdleProxies()),
		HTTP3OverSOCKS5:      c.GetHttp3OverSocks5(),
		HTTP3ReadBufferSize:  int(c.GetHttp3ReadBufferSize()),
		HTTP3ACKEvery:        int(c.GetHttp3AckEvery()),
		Header:               Header(c.GetHeader()),
		HeaderPlacement:      Places(c.GetHeaderPlacement()),
	}

	if roots := c.GetRootCas(); len(roots) > 0 {
		cfg.RootCAs = x509.NewCertPool()
		if !cfg.RootCAs.AppendCertsFromPEM(roots) {
			return mimicry.Config{}, Invalid("config.root_cas holds no PEM certificate")
		}
	}

	for _, a := range c.GetLocalAddrs() {
		addr, err := netip.ParseAddr(a)
		if err != nil {
			return mimicry.Config{}, Invalid("config.local_addrs: %v", err)
		}

		cfg.LocalAddrs = append(cfg.LocalAddrs, addr)
	}

	if server := c.GetDnsServer(); server != "" {
		cfg.Resolver = resolver(server)
	}

	proxy, err := proxyFunc(c.GetProxy())
	if err != nil {
		return mimicry.Config{}, err
	}

	cfg.Proxy = proxy

	if m := c.GetConnectUdpProxy(); len(m) > 0 {
		if cfg.ConnectUDPProxy, err = connectUDPProxy(m); err != nil {
			return mimicry.Config{}, err
		}
	}

	if len(c.GetClientHints()) > 0 {
		var m profile.Machine
		if err := json.Unmarshal(c.GetClientHints(), &m); err != nil {
			return mimicry.Config{}, Invalid("config.client_hints: %v", err)
		}

		cfg.ClientHints = &m
	}

	for _, p := range c.GetPins() {
		cfg.Pins = append(cfg.Pins, mimicry.Pin{
			Host:              p.GetHost(),
			IncludeSubdomains: p.GetIncludeSubdomains(),
			SPKI:              p.GetSpki(),
		})
	}

	if certs := c.GetClientCertificates(); len(certs) > 0 {
		if cfg.GetClientCertificate, err = clientCertificates(certs); err != nil {
			return mimicry.Config{}, err
		}
	}

	return cfg, nil
}

// resolver looks names up at the DNS server addr, over the network the
// lookup asks for (UDP, then TCP for a truncated answer).
func resolver(addr string) *net.Resolver {
	return &net.Resolver{
		PreferGo: true,
		Dial: func(ctx context.Context, network, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, network, addr)
		},
	}
}

// proxyKey is the context key of a request's own proxy (Request.proxy).
type proxyKey struct{}

// proxyFunc is Config.Proxy for the client's proxy URL raw: a request's
// own proxy where it has one, else raw's ("" connects directly).
func proxyFunc(raw string) (func(*http.Request) (*url.URL, error), error) {
	var def *url.URL

	if raw != "" {
		u, err := url.Parse(raw)
		if err != nil {
			return nil, Invalid("config.proxy: %v", err)
		}

		def = u
	}

	return func(req *http.Request) (*url.URL, error) {
		if u, ok := req.Context().Value(proxyKey{}).(*url.URL); ok {
			return proxyOrDirect(u)
		}

		return proxyOrDirect(def)
	}, nil
}

// proxyOrDirect returns u, nil for no proxy: Config.Proxy's answer.
func proxyOrDirect(u *url.URL) (*url.URL, error) {
	if u == nil || u.Host == "" {
		return nil, nil //nolint:nilnil // Config.Proxy's (nil, nil) is a direct connection.
	}

	return u, nil
}

// connectUDPProxy is Config.ConnectUDPProxy for the map m, proxy URL to
// CONNECT-UDP proxy URL, keys matched as the URLs they parse to.
func connectUDPProxy(m map[string]string) (func(*url.URL) (*url.URL, error), error) {
	byProxy := make(map[string]*url.URL, len(m))

	for k, v := range m {
		p, err := url.Parse(k)
		if err != nil {
			return nil, Invalid("config.connect_udp_proxy key: %v", err)
		}

		u, err := url.Parse(v)
		if err != nil {
			return nil, Invalid("config.connect_udp_proxy[%s]: %v", p.Redacted(), err)
		}

		byProxy[p.String()] = u
	}

	return func(proxy *url.URL) (*url.URL, error) {
		if u, ok := byProxy[proxy.String()]; ok {
			c := *u

			return &c, nil
		}

		return nil, nil //nolint:nilnil // ConnectUDPProxy's (nil, nil) keeps the proxy's requests on TCP.
	}, nil
}

// clientCertificates is Config.GetClientCertificate for certs: the
// connection's host's, else the one for every host, else none.
func clientCertificates(certs []*pb.ClientCertificate) (func(string, *tls.CertificateRequestInfo) (*tls.Certificate, error), error) {
	byHost := make(map[string]*tls.Certificate, len(certs))

	for _, c := range certs {
		cert, err := tls.X509KeyPair(c.GetCertificate(), c.GetKey())
		if err != nil {
			return nil, Invalid("config.client_certificates[%q]: %v", c.GetHost(), err)
		}

		byHost[strings.ToLower(c.GetHost())] = &cert
	}

	return func(host string, _ *tls.CertificateRequestInfo) (*tls.Certificate, error) {
		if c, ok := byHost[strings.ToLower(host)]; ok {
			return c, nil
		}

		if c, ok := byHost[""]; ok {
			return c, nil
		}

		return &tls.Certificate{}, nil
	}, nil
}

// Header returns hs as an http.Header, names canonical as Header.Add makes
// them; a name with no values is kept, with none.
func Header(hs []*pb.HeaderValues) http.Header {
	if len(hs) == 0 {
		return nil
	}

	h := make(http.Header, len(hs))

	for _, hv := range hs {
		k := textproto.CanonicalMIMEHeaderKey(hv.GetName())
		if _, ok := h[k]; !ok {
			h[k] = []string{}
		}

		h[k] = append(h[k], Strings(hv.GetValues())...)
	}

	return h
}

// HeaderValues returns h as a list sorted by name, names made valid
// UTF-8 (a protobuf string; a valid name is a token).
func HeaderValues(h http.Header) []*pb.HeaderValues {
	out := make([]*pb.HeaderValues, 0, len(h))
	for _, k := range slices.Sorted(maps.Keys(h)) {
		out = append(out, &pb.HeaderValues{
			Name:   Text(k),
			Values: Bytes(h[k]),
		})
	}

	return out
}

// Bytes returns ss as byte slices.
func Bytes(ss []string) [][]byte {
	if ss == nil {
		return nil
	}

	out := make([][]byte, 0, len(ss))
	for _, s := range ss {
		out = append(out, []byte(s))
	}

	return out
}

// Strings returns bs as strings.
func Strings(bs [][]byte) []string {
	out := make([]string, 0, len(bs))
	for _, b := range bs {
		out = append(out, string(b))
	}

	return out
}

// Text returns s made valid UTF-8, for a protobuf string: a message,
// status or reason, never a value that must cross byte for byte.
func Text(s string) string {
	return strings.ToValidUTF8(s, "\uFFFD")
}

// Places returns ps as mimicry.Places.
func Places(ps []*pb.Place) []mimicry.Place {
	if len(ps) == 0 {
		return nil
	}

	out := make([]mimicry.Place, 0, len(ps))
	for _, p := range ps {
		out = append(out, mimicry.Place{
			Header: p.GetHeader(),
			After:  p.GetAfter(),
			Before: p.GetBefore(),
		})
	}

	return out
}

// Request returns r as an http.Request under ctx, made as its options
// say. body is the streamed body (Request.stream_body), read until EOF;
// nil otherwise.
func Request(ctx context.Context, r *pb.Request, body io.Reader) (*http.Request, error) {
	if r.Proxy != nil {
		var u *url.URL

		if r.GetProxy() != "" {
			p, err := url.Parse(r.GetProxy())
			if err != nil {
				return nil, Invalid("request.proxy: %v", err)
			}

			u = p
		}

		ctx = context.WithValue(ctx, proxyKey{}, u)
	}

	method := r.GetMethod()
	if method == "" {
		method = http.MethodGet
	}

	var rd io.Reader

	switch {
	case r.GetStreamBody():
		rd = body
	case len(r.GetBody()) > 0:
		rd = bytes.NewReader(r.GetBody())
	}

	req, err := http.NewRequestWithContext(ctx, method, r.GetUrl(), rd)
	if err != nil {
		return nil, Invalid("request: %v", err)
	}

	if r.GetStreamBody() {
		req.ContentLength = -1
		if r.ContentLength != nil {
			req.ContentLength = r.GetContentLength()
		}

		if req.ContentLength == 0 {
			req.Body = http.NoBody
		}
	}

	if h := Header(r.GetHeader()); h != nil {
		req.Header = h
	}

	if order := r.GetHeaderOrder(); len(order) > 0 {
		req.Header[mimicry.HeaderOrderKey] = order
	}

	if o := r.GetOptions(); o != nil {
		opts, err := Options(o)
		if err != nil {
			return nil, err
		}

		req = mimicry.With(req, opts)
	}

	if r.GetExact() {
		req = mimicry.Exact(req)
	}

	return req, nil
}

// Options returns o as a mimicry.Request.
func Options(o *pb.Options) (mimicry.Request, error) {
	page, err := optionalURL("options.page", o.GetPage())
	if err != nil {
		return mimicry.Request{}, err
	}

	stylesheet, err := optionalURL("options.stylesheet", o.GetStylesheet())
	if err != nil {
		return mimicry.Request{}, err
	}

	return mimicry.Request{
		Kind:            mimicry.Kind(o.GetKind()),
		From:            page,
		Priority:        mimicry.Priority(o.GetPriority()),
		CrossOrigin:     mimicry.CrossOrigin(o.GetCrossOrigin()),
		Credentials:     mimicry.Credentials(o.GetCredentials()),
		NoCORS:          o.GetNoCors(),
		Stylesheet:      stylesheet,
		IgnoreCORS:      o.GetIgnoreCors(),
		HeaderPlacement: Places(o.GetHeaderPlacement()),
		ClientHints:     mimicry.HintDelegation(o.GetClientHints()),
	}, nil
}

// WebSocketOptions returns o as mimicry.WebSocketOptions.
func WebSocketOptions(o *pb.WebSocketOptions) (mimicry.WebSocketOptions, error) {
	page, err := optionalURL("options.page", o.GetPage())
	if err != nil {
		return mimicry.WebSocketOptions{}, err
	}

	return mimicry.WebSocketOptions{
		From:      page,
		Protocols: o.GetProtocols(),
		Header:    Header(o.GetHeader()),
	}, nil
}

// optionalURL parses raw, a URL or "" for none, named field in errors.
func optionalURL(field, raw string) (*url.URL, error) {
	if raw == "" {
		return nil, nil //nolint:nilnil // "" is no URL, and no error.
	}

	u, err := url.Parse(raw)
	if err != nil {
		return nil, Invalid("%s: %v", field, err)
	}

	return u, nil
}

// Response returns resp's head.
func Response(resp *http.Response) *pb.Response {
	out := &pb.Response{
		StatusCode:    int32(resp.StatusCode),
		Status:        Text(resp.Status),
		Proto:         resp.Proto,
		Header:        HeaderValues(resp.Header),
		ContentLength: resp.ContentLength,
		Uncompressed:  resp.Uncompressed,
	}

	if resp.Request != nil && resp.Request.URL != nil {
		out.Url = Text(resp.Request.URL.String())
	}

	if resp.TLS != nil {
		out.Tls = ConnectionState(resp.TLS)
	}

	return out
}

// ConnectionState returns cs as data.
func ConnectionState(cs *tls.ConnectionState) *pb.ConnectionState {
	out := &pb.ConnectionState{
		Version:                     uint32(cs.Version),
		HandshakeComplete:           cs.HandshakeComplete,
		DidResume:                   cs.DidResume,
		CipherSuite:                 uint32(cs.CipherSuite),
		NegotiatedProtocol:          cs.NegotiatedProtocol,
		ServerName:                  cs.ServerName,
		SignedCertificateTimestamps: cs.SignedCertificateTimestamps,
		OcspResponse:                cs.OCSPResponse,
		EchAccepted:                 cs.ECHAccepted,
	}

	for _, c := range cs.PeerCertificates {
		out.PeerCertificates = append(out.PeerCertificates, c.Raw)
	}

	for _, chain := range cs.VerifiedChains {
		c := &pb.CertificateChain{}
		for _, cert := range chain {
			c.Certificates = append(c.Certificates, cert.Raw)
		}

		out.VerifiedChains = append(out.VerifiedChains, c)
	}

	return out
}

// Plan returns p as data.
func Plan(p *mimicry.Plan) *pb.PlanReply {
	out := &pb.PlanReply{
		Method:    Text(p.Method),
		Kind:      pb.Kind(p.Kind),
		Variant:   p.Variant,
		Condition: p.Condition,
		Protocol:  p.Protocol,
		Http2:     lines(&p.HTTP2),
		Http1:     lines(&p.HTTP1),
		Dropped:   p.Dropped,
		Text:      Text(p.String()),
	}

	if p.URL != nil {
		out.Url = Text(p.URL.String())
	}

	if p.HTTP3 != nil {
		out.Http3 = lines(p.HTTP3)
	}

	if p.Preflight != nil {
		out.Preflight = Plan(p.Preflight)
	}

	for _, o := range p.Overrides {
		out.Overrides = append(out.Overrides, &pb.Override{
			Name:    Text(o.Name),
			Mimicry: []byte(o.Mimicry),
			Caller:  Bytes(o.Caller),
		})
	}

	return out
}

func lines(l *mimicry.Lines) *pb.Lines {
	out := &pb.Lines{
		RequestLine: Text(l.RequestLine),
	}

	for _, h := range l.Headers {
		out.Headers = append(out.Headers, &pb.Header{
			Name:  Text(h.Name),
			Value: []byte(h.Value),
		})
	}

	if l.Err != nil {
		out.Error = Error(l.Err)
	}

	return out
}

// CertPins returns pins as data.
func CertPins(pins []mimicry.CertPin) *pb.CertPins {
	out := &pb.CertPins{}
	for _, p := range pins {
		out.Pins = append(out.Pins, &pb.CertPin{
			Role:        pb.CertRole(p.Role),
			Subject:     p.Subject,
			Issuer:      p.Issuer,
			NotAfter:    timestamppb.New(p.NotAfter),
			Spki:        p.SPKI,
			SpkiHex:     p.SPKIHex,
			Fingerprint: p.Fingerprint,
		})
	}

	return out
}
