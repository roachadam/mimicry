package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/roachadam/mimicry"
	"github.com/roachadam/mimicry/harness/internal/browser"
	"github.com/roachadam/mimicry/profile"
)

// endpoint is a public service that answers with what it saw of the
// request, as JSON.
type endpoint struct {
	id  string
	url string
	// want is the protocol the echo must report ("h3"): the URL is
	// visited again, up to h3Tries times, until it does. "" takes the
	// first answer.
	want string
}

// endpoints, in visiting order: each HTTP/3 endpoint follows a TCP visit to
// the same host, whose Alt-Svc moves the client to h3. tls.peet.ws answered
// no QUIC Initial (2026-10-25), so it is TCP only.
var endpoints = []endpoint{
	{
		id:  "peet",
		url: "https://tls.peet.ws/api/all",
	},
	{
		id:  "browserleaks-tls",
		url: "https://tls.browserleaks.com/json",
	},
	{
		id:  "browserleaks-h2",
		url: "https://tls.browserleaks.com/http2",
	},
	{
		id:  "impersonate-h2",
		url: "https://fp.impersonate.pro/api/http2",
	},
	{
		id:   "impersonate-h3",
		url:  "https://fp.impersonate.pro/api/http3",
		want: "h3",
	},
}

// h3Tries bounds the visits waiting for an echo to report HTTP/3.
const h3Tries = 5

// echoRecord is one client's answer from one endpoint.
type echoRecord struct {
	Client   string `json:"client"`
	Endpoint string `json:"endpoint"`
	URL      string `json:"url"`
	// Proto is the protocol the echo reported; Tries the visits made.
	Proto string    `json:"proto,omitempty"`
	Tries int       `json:"tries"`
	At    time.Time `json:"at"`
	Err   string    `json:"error,omitempty"`
	// Body is the echo's JSON.
	Body json.RawMessage `json:"body,omitempty"`
}

// visitor makes one top-level visit and returns the page's text.
type visitor interface {
	visit(ctx context.Context, url string) (string, error)
	// fresh lets the next visit start on a new connection, where the
	// client moves to HTTP/3 only on one (Safari); false if it cannot.
	fresh() bool
}

// visitEchoes walks every endpoint with v and saves each answer.
func visitEchoes(ctx context.Context, client string, v visitor, out string) error {
	for _, e := range endpoints {
		r := echoRecord{
			Client:   client,
			Endpoint: e.id,
			URL:      e.url,
			At:       time.Now().UTC(),
		}
		for r.Tries < h3Tries {
			if r.Tries > 0 {
				v.fresh()
				time.Sleep(time.Second)
			}
			r.Tries++
			text, err := v.visit(ctx, e.url)
			if err != nil {
				r.Err = err.Error()
				break
			}
			body, err := jsonOf(text)
			if err != nil {
				r.Err = err.Error()
				break
			}
			r.Body, r.Err = body, ""
			r.Proto = echoProto(body)
			if e.want == "" || r.Proto == e.want {
				break
			}
			r.Err = fmt.Sprintf("the echo reported %s, not %s", r.Proto, e.want)
		}
		log.Printf("%-20s %-17s %-3s tries %d %s", client, e.id, r.Proto, r.Tries, r.Err)
		if err := save(filepath.Join(out, "echo", client, e.id+".json"), r); err != nil {
			return err
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
	}

	return nil
}

// jsonOf finds the JSON document in a page's text (browsers may add a
// line of their own around it).
func jsonOf(text string) (json.RawMessage, error) {
	b := bytes.TrimSpace([]byte(text))
	if json.Valid(b) {
		return b, nil
	}
	if i, j := bytes.IndexByte(b, '{'), bytes.LastIndexByte(b, '}'); i >= 0 && j > i && json.Valid(b[i:j+1]) {
		return b[i : j+1], nil
	}
	if len(b) > 300 {
		b = b[:300]
	}

	return nil, fmt.Errorf("the page is not JSON: %q", b)
}

// echoProto is the protocol an echo reports ("h2", "h3"), or "".
func echoProto(body json.RawMessage) string {
	var v struct {
		HTTPVersion string `json:"http_version"`
		Protocol    string `json:"protocol"`
		// fp.impersonate.pro's /api/http3 answers a request over another
		// protocol with actual_protocol only.
		Actual string `json:"actual_protocol"`
	}
	_ = json.Unmarshal(body, &v)
	if v.HTTPVersion != "" {
		return v.HTTPVersion
	}
	if v.Protocol == "" {
		v.Protocol = v.Actual
	}
	switch v.Protocol {
	case "http3":
		return "h3"
	case "http2":
		return "h2"
	}

	return v.Protocol
}

// echoProfile walks the endpoints with a fresh mimicry client for p.
func echoProfile(ctx context.Context, name string, p *profile.Profile, out string) error {
	c, err := mimicry.NewClient(mimicry.Config{
		Profile: p,
	})
	if err != nil {
		return err
	}
	defer c.CloseIdleConnections()

	return visitEchoes(ctx, name, &mimicryVisitor{
		c:           c,
		newConnOnly: p.HTTP3 != nil && p.HTTP3.Discovery.Switch == profile.SwitchNewConnection,
	}, out)
}

// mimicryVisitor visits with a mimicry client, each visit a navigation
// (a typed URL: no page it comes from).
type mimicryVisitor struct {
	c *http.Client
	// newConnOnly: the profile moves to HTTP/3 only on a new connection,
	// which fresh gives it by closing the idle ones (the browser cannot
	// be made to).
	newConnOnly bool
}

func (m *mimicryVisitor) visit(ctx context.Context, url string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	resp, err := m.c.Do(req)
	if err != nil {
		return "", err
	}
	defer func() {
		_ = resp.Body.Close()
	}()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("%s: %s", url, resp.Status)
	}

	return string(b), nil
}

func (m *mimicryVisitor) fresh() bool {
	if m.newConnOnly {
		m.c.CloseIdleConnections()
		return true
	}

	return false
}

// liveVisitor visits with a real browser.
type liveVisitor struct {
	l *browser.Live
}

func (v liveVisitor) visit(ctx context.Context, url string) (string, error) {
	p, err := v.l.Visit(ctx, url)
	if err != nil {
		return "", err
	}

	return p.Text, nil
}

func (liveVisitor) fresh() bool {
	return false
}

// runBrowser drives b through the endpoints and the sites, in one
// session.
func runBrowser(ctx context.Context, b *browser.Browser, out string, echo, doSites bool) error {
	l, err := b.OpenLive(ctx)
	if err != nil {
		return err
	}
	client := browserClient(b)
	var errs []error
	if echo {
		errs = append(errs, visitEchoes(ctx, client, liveVisitor{
			l: l,
		}, out))
	}
	if doSites {
		errs = append(errs, sitesBrowser(ctx, client, l, out))
	}
	errs = append(errs, l.Close())

	return errors.Join(errs...)
}

// browserClient names a driven browser's answers browser-<name>-<major
// version> (from its application bundle), compared with the profile
// <name>-<major>-macos.
func browserClient(b *browser.Browser) string {
	name := "browser-" + b.Name
	if b.App == "" {
		return name
	}
	out, err := exec.Command("plutil", "-extract", "CFBundleShortVersionString", "raw", filepath.Join(b.App, "Contents", "Info.plist")).Output() //nolint:noctx,gosec // a local file of a known application
	if err != nil {
		return name
	}
	major, _, _ := strings.Cut(strings.TrimSpace(string(out)), ".")

	return name + "-" + major
}

// save writes v as indented JSON to path.
func save(path string, v any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return err
	}
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}

	return os.WriteFile(path, data, 0o600)
}
