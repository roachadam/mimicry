package e2e

import (
	"errors"
	"net/http"
	"reflect"
	"slices"
	"strings"
	"sync"

	"github.com/roachadam/mimicry"
	"github.com/roachadam/mimicry/harness/internal/capture"
)

// planning is a page session's RoundTripper when its plans are checked
// (planEvery): it plans every request it is given, redirect hops
// included, with Transport.Plan just before the Transport sends it, and
// keeps each plan with how the request went.
type planning struct {
	tr    *mimicry.Transport
	mu    sync.Mutex
	plans []planned
}

// planned is one request's plan, taken just before it was sent: the plan
// or Plan's error, whether Plan changed the cookie jar (its cookies or
// their last-access times), and the protocol its response came over or
// RoundTrip's error.
type planned struct {
	plan       *mimicry.Plan
	planErr    error
	jarChanged bool
	proto      string
	err        error
}

func (p *planning) RoundTrip(req *http.Request) (*http.Response, error) {
	jar := p.tr.Cookies()
	before := jar.All()
	plan, planErr := p.tr.Plan(req)
	got := planned{
		plan:       plan,
		planErr:    planErr,
		jarChanged: !reflect.DeepEqual(before, jar.All()),
	}
	resp, err := p.tr.RoundTrip(req)
	got.err = err
	if err == nil {
		got.proto = resp.Proto
	}
	p.mu.Lock()
	p.plans = append(p.plans, got)
	p.mu.Unlock()

	return resp, err
}

// planEvery has s plan every request it sends from now on, for
// checkPlans.
func (s *pageSession) planEvery() {
	tr, ok := s.client.Transport.(*mimicry.Transport)
	if !ok {
		panic("planEvery: session client's Transport is not a *mimicry.Transport")
	}
	s.planning = &planning{tr: tr}
	s.client.Transport = s.planning
}

// wireRequest is one request as the capture server received it: the
// protocol (as a response's Proto), method, authority and path, and its
// lines as sent (HTTP/2: every field, pseudo-headers first; HTTP/1.1:
// the request line, then the header lines).
type wireRequest struct {
	proto, method, host, path string
	lines                     []string
	taken                     bool
}

// wireRequests lists page's requests: HTTP/2 connection by connection,
// then HTTP/1.1 in the order they arrived, then HTTP/3 connection by
// connection.
func wireRequests(page *capture.Page) []*wireRequest {
	var out []*wireRequest
	for _, tr := range page.H2 {
		for _, r := range tr.Requests {
			w := &wireRequest{
				proto: "HTTP/2.0",
				path:  r.Path,
			}
			for _, ins := range r.Instructions {
				switch ins.Name {
				case "":
					continue
				case ":method":
					w.method = ins.Value
				case ":authority":
					w.host = ins.Value
				}
				w.lines = append(w.lines, ins.Name+": "+ins.Value)
			}
			out = append(out, w)
		}
	}
	for _, r := range page.H1 {
		method, rest, _ := strings.Cut(r.Line, " ")
		target, _, _ := strings.Cut(rest, " ")
		w := &wireRequest{
			proto:  "HTTP/1.1",
			method: method,
			path:   target,
			lines:  append([]string{r.Line}, r.Headers...),
		}
		for _, l := range r.Headers {
			if name, value, _ := strings.Cut(l, ": "); strings.EqualFold(name, "Host") {
				w.host = value
			}
		}
		out = append(out, w)
	}
	for _, qc := range page.QUIC {
		if qc.H3 == nil {
			continue
		}
		for _, r := range qc.H3.Requests {
			w := &wireRequest{
				proto:  "HTTP/3.0",
				method: r.Method,
				path:   r.Path,
			}
			for _, f := range r.Section.Fields {
				if f.Name == ":authority" {
					w.host = f.Value
				}
				w.lines = append(w.lines, f.Name+": "+f.Value)
			}
			out = append(out, w)
		}
	}

	return out
}

// planLines are a plan's lines over proto as compared with the wire's.
func planLines(p *mimicry.Plan, proto string) (lines []string, err error) {
	l := &p.HTTP2
	switch proto {
	case "HTTP/1.1":
		l = &p.HTTP1
	case "HTTP/3.0":
		l = p.HTTP3
	}
	if l == nil {
		return nil, errors.New("no HTTP/3 lines")
	}
	if l.Err != nil {
		return nil, l.Err
	}
	if l.RequestLine != "" {
		lines = append(lines, l.RequestLine)
	}
	for _, h := range l.Headers {
		lines = append(lines, h.Name+": "+h.Value)
	}

	return lines, nil
}

// checkPlans checks that every request s planned (planEvery) went out as
// planned, in page, the capture of s's page: Plan left the cookie jar as
// it was, failed exactly when RoundTrip did without sending (a refused
// CORS check aside, which only the server decides), the response came
// over the plan's protocol, and the server received the plan's lines for
// that protocol, and its preflight's, where the plan has one, before it.
func (s *pageSession) checkPlans(page *capture.Page) {
	t := s.t
	t.Helper()
	wire := wireRequests(page)
	// take returns the first request of page not taken yet that p
	// describes, over proto (any, if empty).
	take := func(p *mimicry.Plan, proto string) *wireRequest {
		for _, w := range wire {
			if !w.taken && (proto == "" || w.proto == proto) && w.method == p.Method && w.host == p.URL.Host && w.path == p.URL.RequestURI() {
				w.taken = true
				return w
			}
		}

		return nil
	}
	counts := map[string]int{}
	decided := 0
	compare := func(p *mimicry.Plan, w *wireRequest) {
		t.Helper()
		counts[w.proto]++
		name := p.Method + " " + p.URL.String()
		if p.Protocol != "" {
			decided++
		}
		if p.Protocol != "" && p.Protocol != w.proto {
			t.Errorf("%s: planned over %s, sent over %s", name, p.Protocol, w.proto)
		}
		want, err := planLines(p, w.proto)
		if err != nil {
			t.Errorf("%s: sent over %s, planned as not possible there: %v", name, w.proto, err)
			return
		}
		if !slices.Equal(w.lines, want) {
			t.Errorf("%s over %s:\nplanned %q\n   sent %q", name, w.proto, want, w.lines)
		}
	}
	s.planning.mu.Lock()
	plans := slices.Clone(s.planning.plans)
	s.planning.mu.Unlock()
	if len(plans) == 0 {
		t.Error("no request was planned")
	}
	for _, pl := range plans {
		if pl.jarChanged {
			t.Errorf("Plan changed the cookie jar (planned %v)", pl.plan != nil)
		}
		var cors *mimicry.CORSError
		switch {
		case pl.planErr != nil && pl.err == nil:
			t.Errorf("Plan failed, RoundTrip sent it: %v", pl.planErr)
			continue
		case pl.planErr != nil && pl.planErr.Error() != pl.err.Error():
			t.Errorf("Plan failed with %v, RoundTrip with %v", pl.planErr, pl.err)
			continue
		case pl.planErr != nil:
			continue
		case pl.err != nil && !errors.As(pl.err, &cors):
			t.Errorf("%s %s: planned, RoundTrip failed: %v", pl.plan.Method, pl.plan.URL, pl.err)
			continue
		}
		if pre := pl.plan.Preflight; pre != nil {
			if w := take(pre, pre.Protocol); w != nil {
				compare(pre, w)
			} else {
				t.Errorf("%s %s: its planned preflight was never received", pl.plan.Method, pl.plan.URL)
			}
		}
		if pl.err != nil && !cors.Redirect {
			// The preflight refused it: nothing more was sent.
			continue
		}
		// A refused redirect's request was sent, over the protocol of
		// whichever request it is (pl.proto is empty then).
		if w := take(pl.plan, pl.proto); w != nil {
			compare(pl.plan, w)
		} else {
			t.Errorf("%s %s: never received over %s", pl.plan.Method, pl.plan.URL, pl.proto)
		}
	}
	for _, w := range wire {
		if !w.taken {
			t.Errorf("%s %s%s over %s: received, never planned", w.method, w.host, w.path, w.proto)
		}
	}
	t.Logf("plans checked against the wire: %v, %d with the protocol decided before sending", counts, decided)
}
