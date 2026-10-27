package main

import (
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	"github.com/roachadam/mimicry/harness/internal/capture"
	"github.com/roachadam/mimicry/harness/internal/diff"
	"github.com/roachadam/mimicry/harness/internal/server"
	"github.com/roachadam/mimicry/profile"
)

// websocketHello derives tls.websocket_h1 from the websocket capture p is
// checked against (<source>.websocket.pagetrace.json.gz, where the server
// allows no extended CONNECT): the hello of every wss:// socket's own
// HTTP/1.1 connection, in every sample, must be p's, resumed as
// p.TLS.Resumption says when it offers a pre_shared_key, but with one ALPN
// list and without the same extensions.
func websocketHello(dir string, p *profile.Profile) (*profile.WebSocketHello, error) {
	t := &p.TLS
	source, _, err := capture.Inherited(p, server.WebSocket.Name)
	if err != nil {
		return nil, err
	}
	path := filepath.Join(dir, source+"."+server.WebSocket.Name+".pagetrace.json.gz")
	pages, err := capture.LoadPages(path)
	if err != nil {
		return nil, err
	}
	var out *profile.WebSocketHello
	var errs []error
	for si, page := range pages {
		for _, ws := range page.WebSockets {
			if ws.Plain || ws.Protocol != "http/1.1" || ws.Connection == 0 {
				continue
			}
			at := fmt.Sprintf("%s: sample %d, %s", path, si+1, ws.Path)
			h := page.Connections[ws.Connection-1].Hello
			if h == nil || h.LegacyVersion == 0 {
				errs = append(errs, fmt.Errorf("%s: its hello's shape was not recorded", at))
				continue
			}
			got, err := socketHello(t, h)
			if err != nil {
				errs = append(errs, fmt.Errorf("%s: %w", at, err))
				continue
			}
			if out != nil && (!slices.Equal(got.ALPN, out.ALPN) || !slices.Equal(got.Drop, out.Drop)) {
				errs = append(errs, fmt.Errorf("%s: ALPN %v without %v, another socket's %v without %v", at, got.ALPN, got.Drop, out.ALPN, out.Drop))
				continue
			}
			out = got
		}
	}
	if out == nil && len(errs) == 0 {
		errs = append(errs, fmt.Errorf("%s: no wss:// socket over HTTP/1.1", path))
	}
	return out, errors.Join(errs...)
}

// socketHello is how h, one socket connection's hello, differs from t: its
// ALPN protocols, and the extensions t sends that h leaves out beyond
// what resumption drops. Anything else that differs is an error.
func socketHello(t, h *profile.TLS) (*profile.WebSocketHello, error) {
	alpn := extension(h, profile.ExtALPN)
	if alpn == nil {
		return nil, errors.New("no ALPN extension")
	}
	resumed := extension(h, profile.ExtPreSharedKey) != nil
	if resumed && t.Resumption == nil {
		return nil, errors.New("resumed, but the profile does not resume sessions")
	}
	out := &profile.WebSocketHello{
		ALPN: alpn.ALPN,
	}
	for _, e := range t.Extensions {
		dropped := resumed && slices.Contains(t.Resumption.Drop, e.Type)
		if extension(h, e.Type) == nil && !dropped && !slices.Contains(out.Drop, e.Type) {
			out.Drop = append(out.Drop, e.Type)
		}
	}
	want := expectedHello(t, out, resumed)
	got := *h
	got.ExtensionOrder = want.ExtensionOrder
	if diffs := diff.Compare(&profile.Profile{TLS: *want}, &profile.Profile{TLS: got}); len(diffs) > 0 {
		lines := make([]string, len(diffs))
		for i, d := range diffs {
			lines[i] = d.String()
		}
		return nil, errors.New("its hello is not the profile's with another ALPN and fewer extensions:\n" + strings.Join(lines, "\n"))
	}
	return out, nil
}

// expectedHello is t as a socket's connection sends it: ws's ALPN, without
// ws.Drop, and, resumed, without t.Resumption.Drop, with pre_shared_key
// last and the resumed GREASE ECH payload lengths.
func expectedHello(t *profile.TLS, ws *profile.WebSocketHello, resumed bool) *profile.TLS {
	out := *t
	out.Resumption = nil
	out.WebSocketH1 = nil
	out.Extensions = nil
	for _, e := range t.Extensions {
		if slices.Contains(ws.Drop, e.Type) || resumed && slices.Contains(t.Resumption.Drop, e.Type) {
			continue
		}
		switch {
		case e.Type == profile.ExtALPN:
			e.ALPN = ws.ALPN
		case e.ECH != nil && resumed:
			ech := *e.ECH
			ech.PayloadLengths = t.Resumption.ECHPayloadLengths
			e.ECH = &ech
		}
		out.Extensions = append(out.Extensions, e)
	}
	if resumed {
		out.Extensions = append(out.Extensions, profile.Extension{
			Type: profile.ExtPreSharedKey,
		})
	}
	return &out
}
