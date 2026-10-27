package e2e

import (
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/roachadam/mimicry"
	"github.com/roachadam/mimicry/harness/internal/capture"
	"github.com/roachadam/mimicry/harness/internal/diff"
	"github.com/roachadam/mimicry/harness/internal/server"
	"github.com/roachadam/mimicry/profile"
)

// mimicry resumes TLS sessions as the browser does on the resume page, from
// TLS 1.3 and TLS 1.2-only servers: per host, as many used and resumed
// connections as one browser sample, and every resumed ClientHello is the
// browser's. Unused speculative connections are ignored.
func TestResumptionMatchesBrowser(t *testing.T) {
	for _, name := range testProfiles(t) {
		for _, sc := range []*server.Scenario{server.Resume, server.ResumeTLS12} {
			t.Run(name+"/"+sc.Name, func(t *testing.T) {
				p, err := loadProfile(name)
				if err != nil {
					t.Fatal(err)
				}
				want, err := loadPages(t, p, sc.Name, ".pagetrace.json.gz")
				if err != nil {
					t.Fatal(err)
				}
				checkResumption(t, p, sc, want, runResumePage(t, p, sc))
			})
		}
	}
}

func checkResumption(t *testing.T, p *profile.Profile, sc *server.Scenario, want []*capture.Page, got *capture.Page) {
	t.Helper()
	g := resumeCounts(got)
	samples := make([]string, 0, len(want))
	for _, page := range want {
		samples = append(samples, resumeCounts(page))
	}
	if !slices.Contains(samples, g) {
		t.Errorf("used connections per host (total/resumed):\n got %s\nwant one of %q", g, samples)
	}
	browserResumed := resumedHello(want)
	tls13 := sc == server.Resume
	for i, c := range got.Connections {
		if !c.Resumed {
			continue
		}
		if browserResumed == nil {
			t.Errorf("connection %d resumed; the browser never does", i+1)
			continue
		}
		offered := c.PSKIdentities == 1 && c.Ticket == ""
		if !tls13 {
			offered = c.PSKIdentities == 0 && c.Ticket != ""
		}
		if !offered || !c.Accepted {
			t.Errorf("connection %d: %d TLS 1.3 tickets, TLS 1.2 ticket %q, accepted %v", i+1, c.PSKIdentities, c.Ticket, c.Accepted)
		}
		w := profile.Profile{
			TLS: *browserResumed,
		}
		h := profile.Profile{
			TLS: *c.Hello,
		}
		w.TLS.ExtensionOrder, h.TLS.ExtensionOrder = p.TLS.ExtensionOrder, p.TLS.ExtensionOrder
		for _, d := range diff.Compare(&w, &h) {
			t.Errorf("connection %d's resumed ClientHello: %s", i+1, d)
		}
	}
}

// resumedHello returns the browser's resumed ClientHello as captured, the
// first sample's first, with GREASE ECH's cipher suites and payload lengths
// every resumed connection of every sample picked from; nil if the browser
// never resumed.
func resumedHello(pages []*capture.Page) *profile.TLS {
	var hellos []*profile.TLS
	for _, page := range pages {
		for _, c := range page.Connections {
			if c.Resumed {
				hellos = append(hellos, c.Hello)
			}
		}
	}

	return mergeHellos(hellos)
}

// mergeHellos returns the first of hellos, with GREASE ECH's cipher
// suites and payload lengths every one of them picked from; nil for none.
func mergeHellos(hellos []*profile.TLS) *profile.TLS {
	var hello *profile.TLS
	var ech *profile.ECH
	for _, h := range hellos {
		if hello == nil {
			t := *h
			t.Extensions = slices.Clone(t.Extensions)
			hello = &t
		}
		for _, e := range h.Extensions {
			if e.ECH == nil {
				continue
			}
			if ech == nil {
				ech = &profile.ECH{
					EncLength: e.ECH.EncLength,
				}
			}
			ech.CipherSuites = append(ech.CipherSuites, e.ECH.CipherSuites...)
			ech.PayloadLengths = append(ech.PayloadLengths, e.ECH.PayloadLengths...)
		}
	}
	if hello == nil || ech == nil {
		return hello
	}
	slices.SortFunc(ech.CipherSuites, profile.HPKESuite.Compare)
	ech.CipherSuites = slices.Compact(ech.CipherSuites)
	slices.Sort(ech.PayloadLengths)
	ech.PayloadLengths = slices.Compact(ech.PayloadLengths)
	for i := range hello.Extensions {
		if hello.Extensions[i].ECH != nil {
			hello.Extensions[i].ECH = ech
		}
	}

	return hello
}

// resumeCounts summarizes a page's used connections as host
// total/resumed, in the order hosts first appear.
func resumeCounts(page *capture.Page) string {
	var hosts []string
	total, resumed := map[string]int{}, map[string]int{}
	for _, c := range page.Connections {
		if c.Requests == 0 {
			continue
		}
		if total[c.Host] == 0 {
			hosts = append(hosts, c.Host)
		}
		total[c.Host]++
		if c.Resumed {
			resumed[c.Host]++
		}
	}
	parts := make([]string, len(hosts))
	for i, h := range hosts {
		parts[i] = fmt.Sprintf("%s %d/%d", h, total[h], resumed[h])
	}

	return strings.Join(parts, ", ")
}

// runResumePage walks the resume page of sc through mimicry as profile p:
// the page, then each step from it with credentials (the parallel step's
// requests together), then /done.
func runResumePage(t *testing.T, p *profile.Profile, sc *server.Scenario) *capture.Page {
	t.Helper()
	s := startPage(t, p, sc)
	hosts, _, _ := sc.MultiHost()
	hostOf := map[string]string{
		"so": hosts[0],
		"ss": hosts[1],
		"h1": hosts[2],
	}
	pageURL := s.url(hosts[0], "/")
	s.send(http.MethodGet, pageURL, mimicry.Request{}, nil, "")
	page, err := url.Parse(pageURL)
	if err != nil {
		t.Fatal(err)
	}
	fetch := mimicry.Request{
		Kind:        mimicry.Fetch,
		From:        page,
		Credentials: mimicry.CredentialsInclude,
	}
	for _, step := range server.ResumeSteps {
		labels := strings.Split(step, "|")
		errs := make([]error, len(labels))
		var wg sync.WaitGroup
		for i, label := range labels {
			_, suffix, _ := strings.Cut(label, ".")
			target := s.url(hostOf[suffix], "/k/"+label)
			wg.Go(func() {
				req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, target, nil)
				if err != nil {
					errs[i] = err
					return
				}
				resp, err := s.client.Do(mimicry.With(req, fetch))
				if err != nil {
					errs[i] = err
					return
				}
				_, _ = io.Copy(io.Discard, resp.Body)
				errs[i] = resp.Body.Close()
			})
		}
		wg.Wait()
		for _, err := range errs {
			if err != nil {
				t.Fatal(err)
			}
		}
	}

	return s.finish(s.url(hosts[0], "/done"))
}
