package main

import (
	"context"
	"fmt"
	"io"
	"log"
	"net/http"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/roachadam/mimicry"
	"github.com/roachadam/mimicry/harness/internal/browser"
	"github.com/roachadam/mimicry/profiles"
)

// site is a homepage behind a bot-management vendor.
type site struct {
	url    string
	vendor string
}

var sites = []site{
	{
		url:    "https://fp-cf.impersonate.pro/cloudflare/protected",
		vendor: "Cloudflare (test page)",
	},
	{
		url:    "https://www.g2.com/",
		vendor: "Cloudflare",
	},
	{
		url:    "https://www.homedepot.com/",
		vendor: "Akamai",
	},
	{
		url:    "https://www.nike.com/",
		vendor: "Akamai",
	},
	{
		url:    "https://www.leboncoin.fr/",
		vendor: "DataDome",
	},
	{
		url:    "https://www.zillow.com/",
		vendor: "HUMAN",
	},
	{
		url:    "https://www.amazon.com/",
		vendor: "Amazon",
	},
}

// siteGap separates two clients' requests to the same site.
const siteGap = 3 * time.Second

// siteRecord is what one client got from one site.
type siteRecord struct {
	Client string    `json:"client"`
	URL    string    `json:"url"`
	Vendor string    `json:"vendor"`
	At     time.Time `json:"at"`
	// Status, Proto and FinalURL are the response's (FinalURL after
	// redirects); for a browser, its first document's as Navigation
	// Timing reports them (none for Safari).
	Status   int    `json:"status,omitempty"`
	Proto    string `json:"proto,omitempty"`
	FinalURL string `json:"finalUrl,omitempty"`
	Title    string `json:"title,omitempty"`
	Bytes    int    `json:"bytes"`
	// Cookies are the names of the cookies set (a browser's: those it
	// holds once settled; none listed for Safari).
	Cookies []string `json:"cookies,omitempty"`
	// Markers are the challenge or block signs found (see markers); a
	// browser's from its first document's text.
	Markers []string `json:"markers,omitempty"`
	// Settled is a browser's page siteGap later, once its scripts ran (a
	// challenge passed or not).
	Settled *settled `json:"settled,omitempty"`
	Err     string   `json:"error,omitempty"`
}

// settled is a browser's page after its scripts ran.
type settled struct {
	URL     string   `json:"url"`
	Title   string   `json:"title,omitempty"`
	Markers []string `json:"markers,omitempty"`
}

// sitesClients visits every site with each profile, then Go's client,
// site by site, siteGap apart.
func sitesClients(ctx context.Context, names []string, withGo bool, out string) error {
	recs := map[string][]siteRecord{}
	for _, s := range sites {
		for _, n := range names {
			p, err := profiles.ByName(n)
			if err != nil {
				return err
			}
			c, err := mimicry.NewClient(mimicry.Config{
				Profile: p,
			})
			if err != nil {
				return err
			}
			recs[n] = append(recs[n], getSite(ctx, n, c, s))
			c.CloseIdleConnections()
			time.Sleep(siteGap)
		}
		if withGo {
			c := &http.Client{}
			recs["go-net-http"] = append(recs["go-net-http"], getSite(ctx, "go-net-http", c, s))
			c.CloseIdleConnections()
			time.Sleep(siteGap)
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
	}
	for client, rs := range recs {
		if err := save(filepath.Join(out, "sites", client+".json"), rs); err != nil {
			return err
		}
	}

	return nil
}

// getSite GETs s's homepage with c and classifies the answer.
func getSite(ctx context.Context, client string, c *http.Client, s site) (r siteRecord) {
	r = siteRecord{
		Client: client,
		URL:    s.url,
		Vendor: s.vendor,
		At:     time.Now().UTC(),
	}
	defer func() {
		log.Printf("%-20s %-50s %d %-8s %v %s", client, s.url, r.Status, r.Proto, r.Markers, r.Err)
	}()
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.url, nil)
	if err != nil {
		r.Err = err.Error()
		return r
	}
	resp, err := c.Do(req)
	if err != nil {
		r.Err = err.Error()
		return r
	}
	defer func() {
		_ = resp.Body.Close()
	}()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		r.Err = err.Error()
	}
	r.Status, r.Proto, r.FinalURL, r.Bytes = resp.StatusCode, resp.Proto, resp.Request.URL.String(), len(body)
	if m := titleRE.FindSubmatch(body); m != nil {
		r.Title = strings.TrimSpace(string(m[1]))
	}
	// Every response of the chain set cookies; the final one is enough
	// to tell a vendor's.
	for _, ck := range resp.Cookies() {
		r.Cookies = append(r.Cookies, ck.Name)
	}
	r.Markers = markers(resp.StatusCode, resp.Header, r.Cookies, string(body))

	return r
}

var titleRE = regexp.MustCompile(`(?is)<title[^>]*>(.*?)</title>`)

// sitesBrowser visits every site with a real browser.
func sitesBrowser(ctx context.Context, client string, l *browser.Live, out string) error {
	var recs []siteRecord
	for _, s := range sites {
		r := siteRecord{
			Client: client,
			URL:    s.url,
			Vendor: s.vendor,
			At:     time.Now().UTC(),
		}
		p, err := l.Visit(ctx, s.url)
		if err != nil {
			r.Err = err.Error()
		} else {
			r.Status, r.Proto, r.FinalURL, r.Title, r.Bytes = p.Status, p.Protocol, p.URL, p.Title, len(p.Text)
			r.Markers = markers(p.Status, nil, nil, p.Title+"\n"+p.Text)
			// Let a challenge run as it would while a user glances at it.
			time.Sleep(siteGap)
			if q, err := l.Read(ctx); err != nil {
				r.Err = "reading the settled page: " + err.Error()
			} else {
				r.Cookies = q.Cookies
				r.Settled = &settled{
					URL:     q.URL,
					Title:   q.Title,
					Markers: markers(0, nil, q.Cookies, q.Title+"\n"+q.Text),
				}
			}
		}
		log.Printf("%-20s %-50s %d %-8s %v %s", client, s.url, r.Status, r.Proto, r.Markers, r.Err)
		recs = append(recs, r)
		if ctx.Err() != nil {
			return ctx.Err()
		}
	}

	return save(filepath.Join(out, "sites", client+".json"), recs)
}

// markers lists the signs of a challenge or block in an answer: a
// vendor's challenge page or header, its cookies (set on every first
// visit, challenged or not, so listed apart as "cookie:"), and a refusal
// status.
func markers(status int, h http.Header, cookies []string, body string) []string {
	var m []string
	add := func(s string) {
		if !slices.Contains(m, s) {
			m = append(m, s)
		}
	}
	if v := h.Get("Cf-Mitigated"); v != "" {
		add("cf-mitigated:" + v)
	}
	if h.Get("X-Datadome") != "" || h.Get("X-Dd-B") != "" {
		add("datadome-header")
	}
	if h.Get("X-Kpsdk-Ct") != "" || h.Get("X-Kpsdk-R") != "" {
		add("kasada-header")
	}
	lower := strings.ToLower(body)
	for _, b := range bodyMarkers {
		if strings.Contains(lower, b.text) {
			add(b.marker)
		}
	}
	for _, c := range cookies {
		for _, v := range vendorCookies {
			if c == v || (strings.HasSuffix(v, "*") && strings.HasPrefix(c, strings.TrimSuffix(v, "*"))) {
				add("cookie:" + c)
			}
		}
	}
	switch status {
	case http.StatusForbidden, http.StatusTooManyRequests, http.StatusServiceUnavailable, http.StatusUnauthorized:
		add(fmt.Sprintf("status:%d", status))
	}

	return m
}

var bodyMarkers = []struct {
	text   string
	marker string
}{
	{"just a moment...", "cf-challenge-page"},
	{"/cdn-cgi/challenge-platform/", "cf-challenge-script"},
	{"captcha-delivery.com", "datadome-captcha"},
	{"px-captcha", "human-captcha"},
	{"press &amp; hold", "human-press-hold"},
	{"press & hold", "human-press-hold"},
	{"access denied", "access-denied"},
	{"errors.edgesuite.net", "akamai-denied"},
	{"api-services-support@amazon.com", "amazon-captcha"},
	{"token.awswaf.com", "aws-waf-challenge"},
	{"type the characters you see", "amazon-captcha"},
	{"kpsdk", "kasada-script"},
}

// vendorCookies are the cookies bot-management vendors set.
var vendorCookies = []string{
	"__cf_bm", "cf_clearance", "_cfuvid",
	"_abck", "bm_sz", "ak_bmsc", "bm_sv", "bm_mi",
	"datadome",
	"_px*", "_pxhd", "_pxvid",
	"KP_UIDz", "KP_UIDz-ssn",
}
