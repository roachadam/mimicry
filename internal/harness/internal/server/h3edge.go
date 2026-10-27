package server

import (
	"math/rand/v2"
	"sync"
	"time"

	"github.com/quic-go/quic-go"
)

// h3PlainHost never advertises HTTP/3, so /done reaches the server over TCP
// whatever happened to QUIC.
const h3PlainHost = "www.h3-c.test"

// h3DownloadSize is the h3-download response's size: about 40,000 packets,
// enough for repeated credit growth and browser-initiated key updates.
const h3DownloadSize = 50 << 20

// incompressible returns h3DownloadSize pseudo-random bytes, the same in
// every sample.
var incompressible = sync.OnceValue(func() []byte {
	b := make([]byte, h3DownloadSize)
	r := rand.NewChaCha8([32]byte{'m', 'i', 'm', 'i', 'c', 'r', 'y'})

	_, _ = r.Read(b)

	return b
})

// H3Download is the h3-download scenario: a 50 MiB incompressible response
// over HTTP/3, then small requests on the same connection, to show credit
// growth, ACK frequency and browser-initiated key updates (quic-go itself
// updates after 100 packets, then not before 100,000). See H3DownloadSteps.
var H3Download = &Scenario{
	Name: "h3-download",
	Doc:  "a 50 MiB incompressible download over HTTP/3, then small requests on the connection: MAX_DATA and MAX_STREAM_DATA growth, ACK frequency and delay under bulk receive, key updates the browser starts",
	multi: h3Page{
		fixedHosts: fixedHosts{
			names: []string{
				h3Host,
			},
			runFor: 45 * time.Second,
		},
		steps: H3DownloadSteps,
	},
	h3: &h3Server{},
}

// H3DownloadSteps lists the h3-download page's requests: a fetch, a GOAWAY
// moving Safari to HTTP/3, a fetch, the download, then three fetches 1 s
// apart on the same connection.
func H3DownloadSteps() []H3Step {
	goAway := h3Fetch(h3Host, "goaway.so", 1000)

	goAway.GoAwayTCP = true

	download := h3Fetch(h3Host, "download.so", 1500)

	download.NoWait, download.Download = false, h3DownloadSize

	last := h3Fetch(h3Host, "after3.so", 1000)

	last.NoWait = false

	return []H3Step{
		h3Fetch(h3Host, "fetch1.so", 1000),
		goAway,
		h3Fetch(h3Host, "fetch2.so", 1500),
		download,
		h3Fetch(h3Host, "after1.so", 5000),
		h3Fetch(h3Host, "after2.so", 1000),
		last,
	}
}

// H3Blackhole is the h3-blackhole scenario: every response advertises
// HTTP/3 on a UDP port where the server records every datagram and
// answers none, as if UDP were blocked. See H3BlackholeSteps.
var H3Blackhole = &Scenario{
	Name: "h3-blackhole",
	Doc:  "HTTP/3 advertised on a UDP port that answers nothing: each browser's Initial retransmissions and when it gives up, whether requests wait for QUIC or race TCP (on the open connection and after two GOAWAYs), whether it tries QUIC again later in the page",
	multi: h3Page{
		fixedHosts: fixedHosts{
			names: []string{
				h3Host,
			},
			runFor: 60 * time.Second,
		},
		steps: H3BlackholeSteps,
	},
	h3: &h3Server{
		blackhole: true,
	},
}

// H3Broken is the h3-broken scenario: HTTP/3 works on both hosts, then
// (a) the server goes silent on h3-a's QUIC connection mid-request, and
// (b) it closes h3-b's (GOAWAY, CONNECTION_CLOSE) and answers no QUIC
// connection started after that. See H3BrokenSteps.
var H3Broken = &Scenario{
	Name: "h3-broken",
	Doc:  "HTTP/3 that breaks after use: a QUIC connection that goes silent mid-request, then one the server closes after which no new QUIC connection (0-RTT included) is answered: when each browser opens TCP, which requests go where, and how long the origin stays off HTTP/3",
	multi: h3Page{
		fixedHosts: fixedHosts{
			names: []string{
				h3Host,
				h3CrossSite,
				h3PlainHost,
			},
			runFor: 85 * time.Second,
		},
		steps:      H3BrokenSteps,
		plainHosts: []string{h3PlainHost},
		doneHost:   h3PlainHost,
	},
	h3: &h3Server{},
}

// H3V2 is the h3-v2 scenario: HTTP/3 from aioquic, which moves clients
// listing version 2 in version_information to QUIC v2 (RFC 9368). The
// server relays to aioquic and records both directions. See H3V2Steps.
var H3V2 = &Scenario{
	Name: "h3-v2",
	Doc:  "HTTP/3 from a server (aioquic) that upgrades compatibly to QUIC version 2: every packet's version and type both ways, version_information both ways, and the resumed connection after an idle spell",
	multi: h3Page{
		fixedHosts: fixedHosts{
			names: []string{
				h3Host,
				h3PlainHost,
			},
			runFor: 50 * time.Second,
		},
		steps:      H3V2Steps,
		plainHosts: []string{h3PlainHost},
		doneHost:   h3PlainHost,
	},
	h3: &h3Server{
		aioquic: true,
	},
}

// H3VN is the h3-vn scenario: a QUIC server that speaks only version 2,
// so a version 1 Initial is answered with Version Negotiation
// (incompatible version negotiation). See H3VNSteps.
var H3VN = &Scenario{
	Name: "h3-vn",
	Doc:  "HTTP/3 from a server speaking only QUIC version 2: what each browser does with a Version Negotiation packet, and the version and packets of any connection after it",
	multi: h3Page{
		fixedHosts: fixedHosts{
			names: []string{
				h3Host,
			},
			runFor: 30 * time.Second,
		},
		steps: H3VNSteps,
	},
	h3: &h3Server{
		versions: []quic.Version{
			quic.Version2,
		},
	},
}

// h3Fetch is a NoWait fetch step of the h3 edge pages, on h3-a (.so
// labels) or another site (no-cors with credentials).
func h3Fetch(host, label string, pause int) H3Step {
	s := H3Step{
		Label:       label,
		Kind:        "fetch",
		Host:        host,
		Page:        "/",
		PauseMillis: pause,
		NoWait:      true,
	}
	if host != h3Host {
		s.Mode, s.Credentials = "no-cors", "include"
	}

	return s
}

// H3BlackholeSteps lists the h3-blackhole page's requests on a fixed
// schedule (NoWait): fetches 1 s apart, a GOAWAY forcing a new connection
// while the first QUIC attempt may still run, more fetches, a second GOAWAY
// 22.5 s in after that attempt gave up, and a last request 20 s later so
// the second attempt's end shows. Labels say when each starts.
func H3BlackholeSteps() []H3Step {
	goAway := func(s H3Step) H3Step {
		s.GoAwayTCP = true
		return s
	}
	last := h3Fetch(h3Host, "last.so", 20000)

	last.NoWait = false

	return []H3Step{
		h3Fetch(h3Host, "fetch1.so", 1000),
		h3Fetch(h3Host, "fetch2.so", 1000),
		goAway(h3Fetch(h3Host, "goaway1.so", 1000)),
		h3Fetch(h3Host, "after-goaway1.so", 1500),
		h3Fetch(h3Host, "fetch3.so", 1000),
		h3Fetch(h3Host, "fetch4.so", 3000),
		h3Fetch(h3Host, "fetch5.so", 5000),
		goAway(h3Fetch(h3Host, "goaway2.so", 10000)),
		h3Fetch(h3Host, "after-goaway2.so", 1500),
		h3Fetch(h3Host, "fetch6.so", 5000),
		last,
	}
}

// H3BrokenSteps lists the h3-broken page's requests on a fixed schedule
// (NoWait). After warm-up and GOAWAYs (moving Safari to HTTP/3), both hosts
// carry two HTTP/3 fetches. Then:
//   - silence.so goes unanswered and h3-a's connection goes dark;
//     silent1–6 follow over 33 s (past the 30 s idle timeout) while keep1/2.cs
//     keep h3-b's connection busy;
//   - close.cs closes h3-b's connection and no new QUIC connection is
//     answered; closed1–6 follow over 30 s.
//
// /done goes to h3-c, which has no HTTP/3.
func H3BrokenSteps() []H3Step {
	with := func(s H3Step, f func(*H3Step)) H3Step {
		f(&s)

		return s
	}
	last := h3Fetch(h3CrossSite, "closed6.cs", 10000)

	last.NoWait = false

	return []H3Step{
		h3Fetch(h3CrossSite, "warm.cs", 0),
		with(h3Fetch(h3Host, "goaway.so", 1000), func(s *H3Step) {
			s.GoAwayTCP = true
		}),
		with(h3Fetch(h3CrossSite, "goaway.cs", 500), func(s *H3Step) {
			s.GoAwayTCP = true
		}),
		h3Fetch(h3Host, "a1.so", 2000),
		h3Fetch(h3CrossSite, "b1.cs", 500),
		h3Fetch(h3Host, "a2.so", 1000),
		h3Fetch(h3CrossSite, "b2.cs", 500),
		with(h3Fetch(h3Host, "silence.so", 1500), func(s *H3Step) {
			s.Silence = true
		}),
		h3Fetch(h3Host, "silent1.so", 1000),
		h3Fetch(h3Host, "silent2.so", 2000),
		h3Fetch(h3Host, "silent3.so", 3000),
		h3Fetch(h3CrossSite, "keep1.cs", 2000),
		h3Fetch(h3Host, "silent4.so", 3000),
		h3Fetch(h3Host, "silent5.so", 10000),
		h3Fetch(h3CrossSite, "keep2.cs", 2000),
		h3Fetch(h3Host, "silent6.so", 10000),
		with(h3Fetch(h3CrossSite, "close.cs", 2000), func(s *H3Step) {
			s.CloseAfter, s.Blackhole = true, true
		}),
		h3Fetch(h3CrossSite, "closed1.cs", 2000),
		h3Fetch(h3CrossSite, "closed2.cs", 2000),
		h3Fetch(h3CrossSite, "closed3.cs", 3000),
		h3Fetch(h3CrossSite, "closed4.cs", 5000),
		h3Fetch(h3CrossSite, "closed5.cs", 8000),
		last,
	}
}

// H3V2Steps lists the h3-v2 page's requests on a fixed schedule (NoWait):
// fetches, a GOAWAY moving Safari to HTTP/3, two more, then after 35 s (past
// the 30 s idle timeout) three on a resumed connection. /done goes to h3-c.
func H3V2Steps() []H3Step {
	goAway := h3Fetch(h3Host, "goaway.so", 1000)

	goAway.GoAwayTCP = true

	last := h3Fetch(h3Host, "after-idle3.so", 1000)

	last.NoWait = false

	return []H3Step{
		h3Fetch(h3Host, "v2-1.so", 1000),
		h3Fetch(h3Host, "v2-2.so", 1000),
		goAway,
		h3Fetch(h3Host, "v2-3.so", 2000),
		h3Fetch(h3Host, "v2-4.so", 1000),
		h3Fetch(h3Host, "after-idle1.so", 35000),
		h3Fetch(h3Host, "after-idle2.so", 1000),
		last,
	}
}

// H3VNSteps lists the h3-vn page's requests on a fixed schedule (NoWait):
// fetches, a GOAWAY moving Safari to HTTP/3, then fetches 2–18 s after it.
func H3VNSteps() []H3Step {
	goAway := h3Fetch(h3Host, "goaway.so", 1000)

	goAway.GoAwayTCP = true

	last := h3Fetch(h3Host, "vn-6.so", 10000)

	last.NoWait = false

	return []H3Step{
		h3Fetch(h3Host, "vn-1.so", 1000),
		h3Fetch(h3Host, "vn-2.so", 1000),
		goAway,
		h3Fetch(h3Host, "vn-3.so", 2000),
		h3Fetch(h3Host, "vn-4.so", 1000),
		h3Fetch(h3Host, "vn-5.so", 5000),
		last,
	}
}
