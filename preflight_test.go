package mimicry

import (
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/jonboulle/clockwork"

	"github.com/roachadam/mimicry/profiles"
)

// corsEngine answers preflights with answer's headers (status 204, or
// status when set) and other requests 200, recording what was sent.
type corsEngine struct {
	status int
	answer http.Header
	sent   []string
}

func (e *corsEngine) RoundTrip(req *http.Request) (*http.Response, error) {
	line := req.Method + " " + req.URL.Path
	if req.Method == http.MethodOptions {
		line += " " + req.Header.Get("Access-Control-Request-Method") + " " + req.Header.Get("Access-Control-Request-Headers")
	}
	e.sent = append(e.sent, line)
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{},
		Body:       io.NopCloser(strings.NewReader("")),
		Request:    req,
	}
	if req.Method == http.MethodOptions {
		resp.StatusCode = http.StatusNoContent
		if e.status != 0 {
			resp.StatusCode = e.status
		}
		resp.Header = e.answer.Clone()
	}
	return resp, nil
}

func (*corsEngine) CloseIdleConnections() {}

const corsPage = "https://www.a.test/"

// allowAll answers every preflight from corsPage's origin.
func allowAll() http.Header {
	return http.Header{
		"Access-Control-Allow-Origin":      {"https://www.a.test"},
		"Access-Control-Allow-Credentials": {"true"},
		"Access-Control-Allow-Methods":     {"GET, POST, PUT, DELETE, PATCH"},
		"Access-Control-Allow-Headers":     {"X-A, X-B, Content-Type, Accept, Accept-Language, Range, X-Api-Key, Authorization"},
	}
}

func corsTransport(t *testing.T, e *corsEngine, extra http.Header) *Transport {
	t.Helper()
	tr, err := NewTransport(Config{
		Profile: profiles.Chrome(),
		engine:  e,
		Headers: extra,
	})
	if err != nil {
		t.Fatal(err)
	}
	return tr
}

func corsDo(t *testing.T, tr *Transport, method, target string, h http.Header, r Request) error {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), method, target, nil)
	if err != nil {
		t.Fatal(err)
	}
	for k, v := range h {
		req.Header[k] = v
	}
	if r.From == nil {
		r.From, _ = url.Parse(corsPage)
	}
	resp, err := tr.RoundTrip(With(req, r))
	if err != nil {
		return err
	}
	return resp.Body.Close()
}

// A request gets a preflight exactly when the Fetch standard sends one:
// fetch() in CORS mode or XHR, to another origin, with a method other than
// GET, HEAD, POST or a header outside the safelist (by name and value);
// Access-Control-Request-Headers lists those, lowercase, sorted.
func TestPreflightWhenNeeded(t *testing.T) {
	cross := "https://api.b.test/x"
	for _, tc := range []struct {
		name   string
		method string
		target string
		h      http.Header
		extra  http.Header
		r      Request
		want   string // the preflight line, "" for none
	}{
		{name: "simple GET", target: cross, r: Request{Kind: Fetch}},
		{name: "same origin custom header", target: "https://www.a.test/x", h: http.Header{"X-A": {"1"}}, r: Request{Kind: Fetch}},
		{name: "custom headers", target: cross, h: http.Header{"X-B": {"1"}, "x-a": {"1"}}, r: Request{Kind: Fetch}, want: "OPTIONS /x GET x-a,x-b"},
		{name: "PUT", method: http.MethodPut, target: cross, r: Request{Kind: Fetch}, want: "OPTIONS /x PUT "},
		{name: "XHR", method: http.MethodDelete, target: cross, r: Request{Kind: XHR}, want: "OPTIONS /x DELETE "},
		{name: "text/plain", method: http.MethodPost, target: cross, h: http.Header{"Content-Type": {"text/plain;charset=UTF-8"}}, r: Request{Kind: Fetch}},
		{name: "JSON", method: http.MethodPost, target: cross, h: http.Header{"Content-Type": {"application/json"}}, r: Request{Kind: Fetch}, want: "OPTIONS /x POST content-type"},
		{name: "text/plain, invalid parameter", method: http.MethodPost, target: cross, h: http.Header{"Content-Type": {"text/plain; foo"}}, r: Request{Kind: Fetch}},
		{name: "text/plain, repeated charset", method: http.MethodPost, target: cross, h: http.Header{"Content-Type": {" Text/Plain ;charset=a;charset=b"}}, r: Request{Kind: Fetch}},
		{name: "no subtype", method: http.MethodPost, target: cross, h: http.Header{"Content-Type": {"text/"}}, r: Request{Kind: Fetch}, want: "OPTIONS /x POST content-type"},
		{name: "two types", method: http.MethodPost, target: cross, h: http.Header{"Content-Type": {"text/plain", "text/html"}}, r: Request{Kind: Fetch}, want: "OPTIONS /x POST content-type"},
		{name: "plain language", target: cross, h: http.Header{"Accept-Language": {"en-US,en;q=0.9"}}, r: Request{Kind: Fetch}},
		{name: "language with an underscore", target: cross, h: http.Header{"Accept-Language": {"en_US"}}, r: Request{Kind: Fetch}, want: "OPTIONS /x GET accept-language"},
		{name: "long Accept", target: cross, h: http.Header{"Accept": {strings.Repeat("a", 129)}}, r: Request{Kind: Fetch}, want: "OPTIONS /x GET accept"},
		{name: "simple range", target: cross, h: http.Header{"Range": {"bytes=0-99"}}, r: Request{Kind: Fetch}},
		{name: "suffix range", target: cross, h: http.Header{"Range": {"bytes=-99"}}, r: Request{Kind: Fetch}, want: "OPTIONS /x GET range"},
		{name: "Config.Headers are the page's", target: cross, extra: http.Header{"X-Api-Key": {"k"}}, r: Request{Kind: Fetch}, want: "OPTIONS /x GET x-api-key"},
		{name: "no-cors", target: cross, h: http.Header{"X-A": {"1"}}, r: Request{Kind: Fetch, NoCORS: true}},
		{name: "not fetch or XHR", target: cross, h: http.Header{"X-A": {"1"}}, r: Request{Kind: Image, CrossOrigin: CrossOriginAnonymous}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := &corsEngine{
				answer: allowAll(),
			}
			method := tc.method
			if method == "" {
				method = http.MethodGet
			}
			if err := corsDo(t, corsTransport(t, e, tc.extra), method, tc.target, tc.h, tc.r); err != nil {
				t.Fatal(err)
			}
			want := []string{method + " " + "/x"}
			if tc.want != "" {
				want = append([]string{tc.want}, want...)
			}
			if strings.Join(e.sent, "\n") != strings.Join(want, "\n") {
				t.Errorf("sent %q, want %q", e.sent, want)
			}
		})
	}
}

// A successful preflight is remembered for its Access-Control-Max-Age, or
// 5 seconds without one; Max-Age 0 not at all.
func TestPreflightCacheLifetime(t *testing.T) {
	for _, tc := range []struct {
		maxAge string
		fresh  time.Duration // still cached just before this
	}{
		{maxAge: "", fresh: 5 * time.Second},
		{maxAge: "600", fresh: 600 * time.Second},
		{maxAge: "0", fresh: 0},
	} {
		t.Run("max-age "+tc.maxAge, func(t *testing.T) {
			answer := allowAll()
			if tc.maxAge != "" {
				answer.Set("Access-Control-Max-Age", tc.maxAge)
			}
			e := &corsEngine{
				answer: answer,
			}
			tr := corsTransport(t, e, nil)
			clock := clockwork.NewFakeClock()
			tr.clock = clock
			preflights := func() int {
				n := 0
				for _, s := range e.sent {
					if strings.HasPrefix(s, http.MethodOptions) {
						n++
					}
				}
				return n
			}
			send := func() {
				if err := corsDo(t, tr, http.MethodPut, "https://api.b.test/x", nil, Request{Kind: Fetch}); err != nil {
					t.Fatal(err)
				}
			}
			send()
			if tc.fresh > 0 {
				clock.Advance(tc.fresh - time.Millisecond)
				send()
				if n := preflights(); n != 1 {
					t.Fatalf("%d preflights before %v, want 1", n, tc.fresh)
				}
				clock.Advance(time.Millisecond)
			}
			send()
			if n := preflights(); n != 2 {
				t.Errorf("%d preflights at %v, want 2", n, tc.fresh)
			}
		})
	}
}

// A preflight whose answer does not allow the request stops it with a
// *CORSError, unless Request.IgnoreCORS; the CORS rules for credentials
// and wildcards hold.
func TestPreflightRefused(t *testing.T) {
	for _, tc := range []struct {
		name    string
		status  int
		answer  func(http.Header)
		h       http.Header
		include bool
		refused bool
	}{
		{name: "allowed", h: http.Header{"X-A": {"1"}}},
		{name: "status 403", status: http.StatusForbidden, h: http.Header{"X-A": {"1"}}, refused: true},
		{name: "header not allowed", h: http.Header{"X-C": {"1"}}, refused: true},
		{name: "other origin", answer: func(h http.Header) { h.Set("Access-Control-Allow-Origin", "https://c.test") }, h: http.Header{"X-A": {"1"}}, refused: true},
		{name: "wildcards without credentials", answer: func(h http.Header) {
			h.Set("Access-Control-Allow-Origin", "*")
			h.Set("Access-Control-Allow-Headers", "*")
		}, h: http.Header{"X-C": {"1"}}},
		{name: "wildcard origin with credentials", answer: func(h http.Header) { h.Set("Access-Control-Allow-Origin", "*") }, h: http.Header{"X-A": {"1"}}, include: true, refused: true},
		{name: "wildcard headers with credentials", answer: func(h http.Header) { h.Set("Access-Control-Allow-Headers", "*") }, h: http.Header{"X-C": {"1"}}, include: true, refused: true},
		{name: "wildcard does not cover Authorization", answer: func(h http.Header) { h.Set("Access-Control-Allow-Headers", "*") }, h: http.Header{"Authorization": {"x"}}, refused: true},
		{name: "credentials not allowed", answer: func(h http.Header) { h.Del("Access-Control-Allow-Credentials") }, h: http.Header{"X-A": {"1"}}, include: true, refused: true},
	} {
		for _, ignore := range []bool{false, true} {
			t.Run(tc.name+map[bool]string{true: " ignored"}[ignore], func(t *testing.T) {
				answer := allowAll()
				if tc.answer != nil {
					tc.answer(answer)
				}
				e := &corsEngine{
					status: tc.status,
					answer: answer,
				}
				r := Request{
					Kind:       Fetch,
					IgnoreCORS: ignore,
				}
				if tc.include {
					r.Credentials = CredentialsInclude
				}
				err := corsDo(t, corsTransport(t, e, nil), http.MethodGet, "https://api.b.test/x", tc.h, r)
				var cerr *CORSError
				if refused := tc.refused && !ignore; refused != errors.As(err, &cerr) {
					t.Fatalf("got %v, refused %v", err, refused)
				}
				if sent := len(e.sent) == 2; sent != (!tc.refused || ignore) {
					t.Errorf("sent %q", e.sent)
				}
			})
		}
	}
}
