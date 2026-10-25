package main

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/roachadam/mimicry/harness/internal/capture"
	"github.com/roachadam/mimicry/harness/internal/server"
	"github.com/roachadam/mimicry/internal/site"
	"github.com/roachadam/mimicry/profile"
)

// wsBase is the base of every WebSocket handshake: the websocket kind's
// requests.
const wsBase = "ws"

// websocketRequests returns the websocket pages' handshakes
// (server.WebSocketSteps): each wss:// socket over HTTP/2 (an extended
// CONNECT, websocket-h2) and HTTP/1.1 (an Upgrade, websocket) as one
// request per sample, and each ws:// socket (plain-so, in either capture)
// as a plain request. An extended CONNECT is the Upgrade's GET over HTTP/2,
// so its method is GET. A socket is never CORS-checked: Origin is how
// every handshake names its page. The transport is the one the scenario
// leaves the browser (extended CONNECT once the server allows it, HTTP/1.1
// otherwise); a socket on another, or captured twice, is an error.
func websocketRequests(h1Pages, h2Pages []*capture.Page) ([]*request, error) {
	steps := map[string]server.WebSocketStep{}
	for _, st := range server.WebSocketSteps() {
		steps[st.Label] = st
	}
	pageHosts := map[string]string{}
	for _, st := range server.WebSocketPageSteps() {
		pageHosts[st.Label] = st.Host
	}
	var out []*request
	var errs []error
	// relate sets q's step fields: its base and kind, and its site
	// relation to the page that opened it.
	relate := func(q *request, scenario string, si int) bool {
		st, ok := steps[q.label]
		if !ok {
			errs = append(errs, fmt.Errorf("%s sample %d: %s is no socket step", scenario, si+1, q.label))
			return false
		}
		if st.Plain != q.plain {
			errs = append(errs, fmt.Errorf("%s sample %d: %s arrived on the other port", scenario, si+1, q.label))
			return false
		}
		q.base = wsBase
		q.other = true
		q.websocket = true
		q.method = "GET"
		page := pageHosts[st.Page]
		switch {
		case st.Host == page:
			q.site = profile.SiteSameOrigin
		case site.Registrable(st.Host) == site.Registrable(page):
			q.site = profile.SiteSameSite
		default:
			q.site = profile.SiteCrossSite
		}
		return true
	}
	plain := func(page *capture.Page, ws capture.WebSocket, scenario string, si int) {
		q := &request{
			label:  strings.TrimPrefix(ws.Path, "/ws/"),
			host:   ws.Host,
			path:   ws.Path,
			plain:  true,
			sample: si,
			h1:     h1Fields(page.H1[ws.Request].Headers),
		}
		if relate(q, scenario, si) {
			out = append(out, q)
		}
	}
	type streamKey struct {
		conn   int
		stream uint32
	}
	byLabel := map[string]*request{}
	for si, page := range h2Pages {
		byStream := map[streamKey]*request{}
		for _, q := range h2Requests(page, si, "/ws/") {
			byStream[streamKey{q.conn, q.stream}] = q
		}
		for _, ws := range page.WebSockets {
			switch {
			case ws.Plain:
				plain(page, ws, "websocket-h2", si)
				continue
			case ws.Protocol != "h2":
				errs = append(errs, fmt.Errorf("websocket-h2 sample %d: %s over %s, not an extended CONNECT", si+1, ws.Path, ws.Protocol))
				continue
			}
			q := byStream[streamKey{ws.Trace, ws.Stream}]
			if q == nil {
				errs = append(errs, fmt.Errorf("websocket-h2 sample %d: %s's CONNECT is not in its trace", si+1, ws.Path))
				continue
			}
			if !relate(q, "websocket-h2", si) {
				continue
			}
			k := fmt.Sprint(si, " ", q.label)
			if byLabel[k] != nil {
				errs = append(errs, fmt.Errorf("websocket-h2 sample %d: %s captured twice", si+1, q.label))
				continue
			}
			byLabel[k] = q
			out = append(out, q)
		}
	}
	for si, page := range h1Pages {
		for _, ws := range page.WebSockets {
			switch {
			case ws.Plain:
				plain(page, ws, "websocket", si)
				continue
			case ws.Protocol != "http/1.1":
				errs = append(errs, fmt.Errorf("websocket sample %d: %s over %s, though the server allows no extended CONNECT", si+1, ws.Path, ws.Protocol))
				continue
			}
			label := strings.TrimPrefix(ws.Path, "/ws/")
			q := byLabel[fmt.Sprint(si, " ", label)]
			switch {
			case q == nil:
				errs = append(errs, fmt.Errorf("websocket sample %d: %s captured over HTTP/1.1 only", si+1, label))
				continue
			case q.h1 != nil:
				errs = append(errs, fmt.Errorf("websocket sample %d: %s captured twice", si+1, label))
				continue
			}
			q.h1 = h1Fields(page.H1[ws.Request].Headers)
		}
	}
	for _, q := range out {
		if !q.plain && q.h1 == nil {
			errs = append(errs, fmt.Errorf("websocket-h2 sample %d: %s captured over HTTP/2 only", q.sample+1, q.label))
		}
	}
	return out, errors.Join(errs...)
}

// checkProtocolPlace checks that every extended CONNECT in the
// websocket-h2 capture sends the profile's pseudo-header order with
// :protocol after it: mimicry's rule, which no field states.
func checkProtocolPlace(p *profile.Profile, pages []*capture.Page) error {
	if p.HTTP2 == nil {
		return errors.New("websocket: the profile has no http2 section")
	}
	want := append(slices.Clone(p.HTTP2.PseudoHeaderOrder), ":protocol")
	var errs []error
	for si, page := range pages {
		for _, ws := range page.WebSockets {
			if ws.Protocol != "h2" || ws.Request < 0 {
				continue
			}
			var got []string
			for _, ins := range page.H2[ws.Trace].Requests[ws.Request].Instructions {
				if strings.HasPrefix(ins.Name, ":") {
					got = append(got, ins.Name)
				}
			}
			if !slices.Equal(got, want) {
				errs = append(errs, fmt.Errorf("websocket-h2 sample %d: %s sends pseudo-headers %v, not %v", si+1, ws.Path, got, want))
			}
		}
	}
	return errors.Join(errs...)
}
