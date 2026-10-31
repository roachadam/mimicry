package main

import (
	"fmt"
	"strings"

	"github.com/roachadam/mimicry/internal/harness/internal/capture"
	"github.com/roachadam/mimicry/internal/harness/internal/h2trace"
	"github.com/roachadam/mimicry/internal/harness/internal/server"
	"github.com/roachadam/mimicry/profile"
)

// goAwayReply derives http2.goawayReply: the error code of the GOAWAY the
// browser sent after the server's, on every HTTP/2 connection of p's
// captures the server sent GOAWAY on (after answering a request its
// scenario closes the connection on), or "" when it sent none on any.
// The connections must agree. Replay pages are left out: their scenarios
// come from page shapes, not by name.
func goAwayReply(dir string, p *profile.Profile) (string, error) {
	var (
		reply, replyAt string
		seen           bool
	)

	for _, suffix := range []string{".h2trace.json", ".pagetrace.json.gz"} {
		files, err := capture.Files(dir, p, suffix)
		if err != nil {
			return "", err
		}

		for _, f := range files {
			if strings.HasPrefix(f.Scenario, "replay-") {
				continue
			}

			sc, err := server.ScenarioByName(f.Scenario)
			if err != nil {
				return "", fmt.Errorf("%s: %w", f.Path, err)
			}

			samples, err := traces(f.Path, suffix)
			if err != nil {
				return "", err
			}

			for i, conns := range samples {
				for j, tr := range conns {
					at := fmt.Sprintf("%s: sample %d, HTTP/2 connection %d", f.Path, i+1, j+1)

					code, ok, err := replied(sc, tr)
					if err != nil {
						return "", fmt.Errorf("%s: %w", at, err)
					}

					if !ok {
						continue
					}

					if !seen {
						reply, replyAt, seen = code, at, true

						continue
					}

					if code != reply {
						return "", fmt.Errorf("%s: GOAWAY reply %q, %s's %q", at, code, replyAt, reply)
					}
				}
			}
		}
	}

	return reply, nil
}

// traces are a capture file's HTTP/2 connections, per sample.
func traces(path, suffix string) ([][]*h2trace.Trace, error) {
	if suffix == ".h2trace.json" {
		loaded, err := h2trace.Load(path)
		if err != nil {
			return nil, err
		}

		out := make([][]*h2trace.Trace, len(loaded))
		for i, tr := range loaded {
			out[i] = []*h2trace.Trace{tr}
		}

		return out, nil
	}

	pages, err := capture.LoadPages(path)
	if err != nil {
		return nil, err
	}

	out := make([][]*h2trace.Trace, len(pages))
	for i, page := range pages {
		out[i] = page.H2
	}

	return out, nil
}

// replied is the error code of the client's GOAWAY on the server's ("" for
// none); ok is false when the server sent none on tr. Traces hold the
// client's frames only, so the server's GOAWAY, which follows its answer
// to the request it closes the connection on, is placed by that request's
// HEADERS: the client's first GOAWAY after them is the reply, and one
// before them is an error. Firefox's own GOAWAY once HTTP/3 is up
// (goawayTcp) cannot be told apart from a reply on the h3 pages; resume,
// resume-tls12 and proxy-auth have no HTTP/3.
func replied(sc *server.Scenario, tr *h2trace.Trace) (code string, ok bool, err error) {
	var stream uint32

	for _, r := range tr.Requests {
		if sc.GoesAway(tr.Host, r.Path) {
			stream = r.Stream

			break
		}
	}

	if stream == 0 {
		return "", false, nil
	}

	asked := false
	for _, f := range tr.Frames {
		switch {
		case f.Type == "HEADERS" && f.Stream == stream:
			asked = true
		case f.Type == "GOAWAY" && asked:
			return f.ErrCode, true, nil
		case f.Type == "GOAWAY":
			return "", false, fmt.Errorf("GOAWAY at %d ms, before stream %d, whose answer the server's follows", f.Millis, stream)
		}
	}

	return "", true, nil
}
