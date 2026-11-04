package mimicry

import (
	"encoding/json"
	"io"
	"math"
	"math/rand/v2"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"

	"github.com/roachadam/mimicry/internal/site"
	"github.com/roachadam/mimicry/profile"
)

// HintDelegation is whether a page shares the client hints its origin opted
// in to (Accept-CH) with a request to another origin. A browser decides it
// from the page's Permissions-Policy and Delegate-CH meta tag, which
// mimicry does not read: the caller says (decision 0055).
type HintDelegation int

// Delegations. Whatever the page decides, a request to another site's
// frame, image or fetch() never gets hints the page's origin did not opt
// in to.
const (
	// HintsDefault: a request to the page's own origin sends every hint
	// that origin opted in to; one to another origin (a subdomain
	// included) only the low-entropy sec-ch-ua, sec-ch-ua-mobile and
	// sec-ch-ua-platform.
	HintsDefault HintDelegation = iota
	// HintsDelegated: the page delegates its hints to the request's origin
	// (Permissions-Policy: ch-ua-arch=(self "https://cdn.example"), or a
	// <meta http-equiv="Delegate-CH">), which then gets them all.
	HintsDelegated
	// HintsWithheld: the page's Permissions-Policy names its own allowlist
	// for the hints (ch-ua=(self ...)) and leaves the request's origin out:
	// it gets no client hints at all, not even sec-ch-ua.
	HintsWithheld
)

// ClientHintStore keeps which client hints each origin opted in to: the
// Accept-CH of its last navigation response, as a browser keeps it until
// the site replaces or clears it. Like the cookie jar, it belongs to one
// identity: save it with the jar to resume that identity later.
// Implementations must be safe for concurrent use.
type ClientHintStore interface {
	// ClientHints returns the hints origin ("https://example.com", with
	// its port unless the scheme's default) opted in to, lowercase; nil
	// when none.
	ClientHints(origin string) []string
	// SetClientHints replaces origin's hints; none forgets origin.
	SetClientHints(origin string, hints []string)
}

// MemoryClientHintStore is the ClientHintStore NewClientHintStore returns:
// in memory, safe for concurrent use, saved and loaded as JSON with
// WriteJSON and ReadJSON. Its zero value is an empty store.
type MemoryClientHintStore struct {
	mu      sync.Mutex
	origins map[string][]string
}

// NewClientHintStore returns an empty MemoryClientHintStore.
func NewClientHintStore() *MemoryClientHintStore {
	return &MemoryClientHintStore{}
}

// ClientHints implements ClientHintStore.
func (s *MemoryClientHintStore) ClientHints(origin string) []string {
	s.mu.Lock()
	defer s.mu.Unlock()

	return slices.Clone(s.origins[origin])
}

// SetClientHints implements ClientHintStore.
func (s *MemoryClientHintStore) SetClientHints(origin string, hints []string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.set(origin, hints)
}

// set is SetClientHints with s.mu held.
func (s *MemoryClientHintStore) set(origin string, hints []string) {
	if len(hints) == 0 {
		delete(s.origins, origin)

		return
	}

	if s.origins == nil {
		s.origins = map[string][]string{}
	}

	s.origins[origin] = slices.Clone(hints)
}

// WriteJSON writes every origin's hints as a JSON object, origins as keys:
// what ReadJSON reads back.
func (s *MemoryClientHintStore) WriteJSON(w io.Writer) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	origins := s.origins
	if origins == nil {
		origins = map[string][]string{}
	}

	return json.NewEncoder(w).Encode(origins)
}

// ReadJSON adds the origins of a JSON object WriteJSON wrote, replacing
// those already held; hint names are lowercased, and an origin with none
// is forgotten. A malformed object fails it before anything is added.
func (s *MemoryClientHintStore) ReadJSON(r io.Reader) error {
	var origins map[string][]string
	if err := json.NewDecoder(r).Decode(&origins); err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	for origin, hints := range origins {
		lower := make([]string, len(hints))
		for i, h := range hints {
			lower[i] = strings.ToLower(h)
		}

		s.set(origin, lower)
	}

	return nil
}

// hintsOut is how a request's client hints differ from its list's: the
// opted-in hints it sends beyond the list's low-entropy ones, their
// values, whether it sends none at all, and whether it is a navigation
// restarted by Critical-CH.
type hintsOut struct {
	// send are the opted-in hints, lowercase, in ClientHints.Hints' order,
	// low-entropy ones excluded: the list has those.
	send     []string
	values   map[string]string
	withheld bool
	restart  bool
}

// criticalRestartKey marks, in its context, a navigation sent again
// because its response's Critical-CH named a hint it did not send.
type criticalRestartKey struct{}

// pageHints are the client hints a page loaded with: its origin's opt-in
// once its navigation's response was read. Its subresources send these
// even after a later response clears them (Clear-Site-Data on a fetch):
// Chrome fixes a document's hints when it loads.
type pageHints struct {
	url   string
	hints []string
}

// hintsFor returns how the request to req.URL that r describes, from page
// from, sends client hints: nil when as its list says.
func (t *Transport) hintsFor(req *http.Request, r Request, from *url.URL) *hintsOut {
	ch := t.profile.ClientHints
	if ch == nil || !site.Trustworthy(req.URL) {
		// Firefox and Safari ignore the opt-in; no browser sends hints
		// to a non-secure URL.
		return nil
	}

	owner := req.URL

	var stored []string

	if r.Kind != Navigate {
		switch {
		case from == nil:
			return nil
		case isSameOrigin(from, req.URL):
			// The page's own origin gets every hint it opted in to.
		case r.ClientHints == HintsWithheld:
			return &hintsOut{
				withheld: true,
			}

		case r.ClientHints != HintsDelegated:
			// Another origin gets the low-entropy hints, which the
			// lists hold.
			return nil
		}

		owner = from

		if p := t.lastPage.Load(); p != nil && p.url == from.String() {
			stored = p.hints
		} else {
			stored = t.hintStore.ClientHints(origin(owner))
		}
	} else {
		stored = t.hintStore.ClientHints(origin(owner))
	}

	restart, _ := req.Context().Value(criticalRestartKey{}).(bool)

	var send []string
	for _, name := range ch.Hints {
		if !profile.LowEntropyHint(name) && slices.Contains(stored, name) {
			send = append(send, name)
		}
	}

	if len(send) == 0 && !restart {
		return nil
	}

	// Frames are navigations too: Chrome computes their network hints
	// where it does the top-level page's, with other noise than a page's
	// subresources get (client-hints capture).
	nav := r.Kind == Navigate || r.Kind == Frame
	values := make(map[string]string, len(send))

	for _, name := range send {
		if v, ok := t.profile.ClientHintValue(name, t.machine); ok {
			values[name] = v
		} else {
			values[name] = t.networkHint(name, req.URL.Hostname(), nav)
		}
	}

	return &hintsOut{
		send:    send,
		values:  values,
		restart: restart,
	}
}

// noiseKey is what Chrome's network hints are fixed by: the request's
// host and whether a navigation (the browser's) or a subresource (the
// page's renderer) asks.
type noiseKey struct {
	host string
	nav  bool
}

// netHints are a noiseKey's network hint values.
type netHints struct {
	rtt      string
	downlink string
}

// networkHint is the value of network hint name (profile.HintRTT,
// profile.HintDownlink) for a request to host, fixed on its key's first
// use for the Transport's life, as Chrome's stay per host: the estimate
// (the last TCP connect's time for rtt, the machine's downlink) times a
// noise factor of 0.90 to 1.10, then capped and rounded as Chrome does
// (50 ms, 3 s; 0.05 Mbps, 10 Mbps).
func (t *Transport) networkHint(name, host string, nav bool) string {
	key := noiseKey{
		host: host,
		nav:  nav,
	}

	v, ok := t.netHints.Load(key)
	if !ok {
		noise := 0.9 + float64(rand.IntN(21))/100 //nolint:gosec // noise needs no secrecy
		ms := min(float64(t.connectRTT.Load())/1e6*noise, 3000)
		mbps := min(t.machine.Downlink*noise, 10)

		v, _ = t.netHints.LoadOrStore(key, netHints{
			rtt:      strconv.Itoa(int(math.Round(ms/50) * 50)),
			downlink: strconv.FormatFloat(math.Round(mbps*20)/20, 'f', -1, 64),
		})
	}

	h, _ := v.(netHints)
	if name == profile.HintRTT {
		return h.rtt
	}

	return h.downlink
}

// withHints returns list as a request sending hints h carries them, or
// list itself when h changes nothing in it. A navigation's or frame's
// (nav) hints go as one run in ClientHints.Hints' order where its list has
// the low-entropy ones, or after RestartAfter on a Critical-CH restart.
// Another kind's go into its header table run: the list holds its table
// members (table.Own) in the order they enter the table (TableOrder, then
// user-agent), which headerorder.Build lays out (Plan.OwnRun). The
// returned table is that run's, nil when the request needs none.
func withHints(ch *profile.ClientHints, list []profile.Header, h *hintsOut, nav bool, table *profile.HeaderTable) ([]profile.Header, *profile.HeaderTable) {
	low := func(hd profile.Header) bool {
		return profile.LowEntropyHint(strings.ToLower(hd.Name))
	}

	at := slices.IndexFunc(list, low)
	if at < 0 {
		// No slot for hints (a non-secure list).
		return list, nil
	}

	if h.withheld {
		return slices.DeleteFunc(slices.Clone(list), low), nil
	}

	inList := func(name string) (profile.Header, bool) {
		i := slices.IndexFunc(list, func(hd profile.Header) bool {
			return strings.EqualFold(hd.Name, name)
		})
		if i < 0 {
			return profile.Header{}, false
		}

		return list[i], true
	}

	// The hints in order, navigations' (Hints) or the header table's
	// (TableOrder): the list's low-entropy ones with its values, and the
	// opted-in ones.
	order := ch.TableOrder
	if nav {
		order = ch.Hints
	}

	var run []profile.Header
	for _, name := range order {
		if hd, ok := inList(name); ok && profile.LowEntropyHint(name) {
			run = append(run, hd)
		} else if v, ok := h.values[name]; ok {
			run = append(run, profile.Header{
				Name:  name,
				Value: v,
			})
		}
	}

	if nav {
		out := slices.DeleteFunc(slices.Clone(list), low)
		if h.restart {
			if i := slices.IndexFunc(out, func(hd profile.Header) bool {
				return strings.EqualFold(hd.Name, ch.RestartAfter)
			}); i >= 0 {
				at = i + 1
			}
		}

		return slices.Insert(out, at, run...), nil
	}

	// The table's other members (user-agent) enter after the hints, in
	// the list's order.
	member := func(hd profile.Header) bool {
		return slices.Contains(table.Own, strings.ToLower(hd.Name))
	}

	at = slices.IndexFunc(list, member)
	for _, hd := range list {
		if member(hd) && !low(hd) {
			run = append(run, hd)
		}
	}

	own := make([]string, len(run))
	for i, hd := range run {
		own[i] = strings.ToLower(hd.Name)
	}

	runTable := *table

	runTable.Own = own

	return slices.Insert(slices.DeleteFunc(slices.Clone(list), member), at, run...), &runTable
}

// sentHints are the client hints a request sends with list: the
// lowercase names among ClientHints.Hints.
func sentHints(ch *profile.ClientHints, list []profile.Header) []string {
	var out []string
	for _, hd := range list {
		if name := strings.ToLower(hd.Name); slices.Contains(ch.Hints, name) {
			out = append(out, name)
		}
	}

	return out
}

// clearHints forgets the client hints of resp's origin when resp, the
// answer to any request, carries Clear-Site-Data "clientHints" or "*". A
// page already loaded keeps sending its own (pageHints).
func (t *Transport) clearHints(req *http.Request, resp *http.Response) {
	if t.profile.ClientHints == nil || !site.Trustworthy(req.URL) {
		return
	}

	for _, v := range resp.Header.Values("Clear-Site-Data") {
		for item := range strings.SplitSeq(v, ",") {
			if item = strings.TrimSpace(item); item == `"clientHints"` || item == `"*"` {
				t.hintStore.SetClientHints(origin(req.URL), nil)
			}
		}
	}
}

// learnHints records what navigation req's response resp, a redirect's
// included, says of its origin's client hints: Accept-CH replaces them
// (after clearHints). It reports whether the browser sends the navigation
// again: its Critical-CH names a hint the new Accept-CH asks for and a
// navigation sends, which s did not, and it is a GET.
func (t *Transport) learnHints(req *http.Request, resp *http.Response, s *sending) bool {
	ch := t.profile.ClientHints
	if ch == nil || !site.Trustworthy(req.URL) {
		return false
	}

	accept := resp.Header.Values("Accept-CH")
	if len(accept) == 0 {
		return false
	}

	asked := hintTokens(accept)

	var opted []string
	for _, name := range ch.Hints {
		if slices.Contains(asked, name) {
			opted = append(opted, name)
		}
	}

	t.hintStore.SetClientHints(origin(req.URL), opted)

	if restarted, _ := req.Context().Value(criticalRestartKey{}).(bool); restarted || req.Method != http.MethodGet || req.Body != nil && req.Body != http.NoBody {
		// One restart at most, and none for a form's POST (Chrome stores
		// its Accept-CH but sends it once).
		return false
	}

	for _, name := range hintTokens(resp.Header.Values("Critical-CH")) {
		if slices.Contains(opted, name) && !slices.Contains(s.hints, name) {
			return true
		}
	}

	return false
}

// hintTokens are the lowercase names a list of hints (Accept-CH,
// Critical-CH) gives.
func hintTokens(values []string) []string {
	var out []string
	for _, v := range values {
		for item := range strings.SplitSeq(v, ",") {
			if item = strings.ToLower(strings.TrimSpace(item)); item != "" {
				out = append(out, item)
			}
		}
	}

	return out
}
