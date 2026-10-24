package e2e

import (
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/roachadam/mimicry"
	"github.com/roachadam/mimicry/harness/internal/capture"
	"github.com/roachadam/mimicry/harness/internal/diff"
	"github.com/roachadam/mimicry/harness/internal/server"
	"github.com/roachadam/mimicry/profile"
	"github.com/roachadam/mimicry/profiles"
)

// mimicry resumes TLS sessions as the browser does on the resume page, from
// a TLS 1.3 server and a TLS 1.2-only one: per host, as many used
// connections and as many of them resumed as in one of the browser's
// samples (connections a browser opened speculatively and never used are
// left out, and Chrome sometimes has no ticket yet for its second
// connection). That covers ticket use: TLS 1.3 tickets once (one of three
// parallel connections resumes); TLS 1.2 tickets shared (Chrome: all three)
// or once with no ticket kept from a resumed connection (Firefox:
// alternating). Every resumption offers one ticket, the server accepts it,
// and every resumed ClientHello is the browser's. Safari never resumes.
func TestResumptionMatchesBrowser(t *testing.T) {
	for _, name := range profiles.Names() {
		for _, sc := range []*server.Scenario{server.Resume, server.ResumeTLS12} {
			t.Run(name+"/"+sc.Name, func(t *testing.T) {
				p, err := profiles.ByName(name)
				if err != nil {
					t.Fatal(err)
				}
				want, err := capture.LoadPages(captureDir + name + "." + sc.Name + ".pagetrace.json.gz")
				if os.IsNotExist(err) {
					t.Skipf("no %s capture for %s", sc.Name, name)
				}
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
	var browserResumed *profile.TLS
	for _, c := range want[0].Connections {
		if c.Resumed {
			browserResumed = c.Hello
			break
		}
	}
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
		Credentials: mimicry.Include,
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
