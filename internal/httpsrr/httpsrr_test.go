package httpsrr

import (
	"context"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"reflect"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/jonboulle/clockwork"
	"golang.org/x/net/dns/dnsmessage"
)

// testNameservers are what the system config names in these tests; the
// resolver's Dial sends every query to the loopback server whatever the
// address.
var testNameservers = []string{
	"192.0.2.1:53",
	"192.0.2.2:53",
}

// query is a query the loopback server got.
type query struct {
	header   dnsmessage.Header
	question dnsmessage.Question
	// udpSize is the payload size its OPT record advertises, 0 without.
	udpSize int
	tcp     bool
}

// dnsServer is a nameserver on loopback, UDP and TCP on one port, sending
// answer's replies to each query: every one as a datagram over UDP, the
// first over TCP.
type dnsServer struct {
	addr   string
	answer func(q query) [][]byte

	mu      sync.Mutex
	queries []query
	dials   []string
}

// startServer starts a dnsServer, closed when the test ends.
func startServer(t *testing.T, answer func(q query) [][]byte) *dnsServer {
	t.Helper()

	var (
		pc  net.PacketConn
		ln  net.Listener
		err error
	)

	for range 10 {
		pc, err = new(net.ListenConfig).ListenPacket(t.Context(), "udp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}

		ln, err = new(net.ListenConfig).Listen(t.Context(), "tcp", pc.LocalAddr().String())
		if err == nil {
			break
		}

		_ = pc.Close()
	}

	if err != nil {
		t.Fatal(err)
	}

	s := &dnsServer{
		addr:   pc.LocalAddr().String(),
		answer: answer,
	}

	var wg sync.WaitGroup

	t.Cleanup(func() {
		_ = pc.Close()
		_ = ln.Close()

		wg.Wait()
	})

	wg.Go(func() {
		s.serveUDP(pc)
	})
	wg.Go(func() {
		s.serveTCP(ln, &wg)
	})

	return s
}

func (s *dnsServer) serveUDP(pc net.PacketConn) {
	buf := make([]byte, 65535)

	for {
		n, from, err := pc.ReadFrom(buf)
		if err != nil {
			return
		}

		for _, msg := range s.handle(buf[:n], false) {
			_, _ = pc.WriteTo(msg, from)
		}
	}
}

func (s *dnsServer) serveTCP(ln net.Listener, wg *sync.WaitGroup) {
	for {
		conn, err := ln.Accept()
		if err != nil {
			return
		}

		wg.Go(func() {
			defer func() {
				_ = conn.Close()
			}()

			var length [2]byte

			_, err := io.ReadFull(conn, length[:])
			if err != nil {
				return
			}

			buf := make([]byte, binary.BigEndian.Uint16(length[:]))

			_, err = io.ReadFull(conn, buf)
			if err != nil {
				return
			}

			replies := s.handle(buf, true)
			if len(replies) == 0 {
				return
			}

			_, _ = conn.Write(binary.BigEndian.AppendUint16(nil, uint16(len(replies[0]))))
			_, _ = conn.Write(replies[0])
		})
	}
}

// handle records the query in msg and returns answer's replies.
func (s *dnsServer) handle(msg []byte, tcp bool) [][]byte {
	var m dnsmessage.Message

	err := m.Unpack(msg)
	if err != nil || len(m.Questions) != 1 {
		return nil
	}

	q := query{
		header:   m.Header,
		question: m.Questions[0],
		tcp:      tcp,
	}

	for _, rr := range m.Additionals {
		if rr.Header.Type == dnsmessage.TypeOPT {
			q.udpSize = int(rr.Header.Class)
		}
	}

	s.record(func() {
		s.queries = append(s.queries, q)
	})

	return s.answer(q)
}

// record runs f with s.mu held.
func (s *dnsServer) record(f func()) {
	s.mu.Lock()
	defer s.mu.Unlock()

	f()
}

func (s *dnsServer) got() ([]query, []string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	return slices.Clone(s.queries), slices.Clone(s.dials)
}

// newResolver returns a Resolver timed by clock whose net.Resolver dials
// s, recording the address it is given.
func newResolver(s *dnsServer, clock clockwork.Clock) *Resolver {
	r := New(Config{
		Resolver: &net.Resolver{
			PreferGo: true,
			Dial: func(ctx context.Context, network, address string) (net.Conn, error) {
				s.record(func() {
					s.dials = append(s.dials, address)
				})

				var d net.Dialer

				return d.DialContext(ctx, network, s.addr)
			},
		},
		Clock: clock,
	})

	r.nameservers = func() []string {
		return testNameservers
	}

	return r
}

// reply is a reply to q: id (q's when zero), rcode, the TC flag, and the
// answer and authority sections.
type reply struct {
	id        uint16
	rcode     dnsmessage.RCode
	truncated bool
	answers   []dnsmessage.Resource
	authority []dnsmessage.Resource
}

func (r reply) to(q query) []byte {
	id := r.id
	if id == 0 {
		id = q.header.ID
	}

	m := dnsmessage.Message{
		ID:                 id,
		Response:           true,
		RecursionDesired:   q.header.RecursionDesired,
		RecursionAvailable: true,
		Truncated:          r.truncated,
		RCode:              r.rcode,
		Questions: []dnsmessage.Question{
			q.question,
		},
		Answers:     r.answers,
		Authorities: r.authority,
	}

	msg, err := m.Pack()
	if err != nil {
		panic(err)
	}

	return msg
}

// answers returns an answer function sending r to every query.
func answers(r reply) func(q query) [][]byte {
	return func(q query) [][]byte {
		return [][]byte{
			r.to(q),
		}
	}
}

func header(name string, ttl uint32) dnsmessage.ResourceHeader {
	return dnsmessage.ResourceHeader{
		Name:  dnsmessage.MustNewName(name),
		Class: dnsmessage.ClassINET,
		TTL:   ttl,
	}
}

func httpsRR(name string, ttl uint32, priority uint16, target string, params ...dnsmessage.SVCParam) dnsmessage.Resource {
	return dnsmessage.Resource{
		Header: header(name, ttl),
		Body: &dnsmessage.HTTPSResource{
			Priority: priority,
			Target:   dnsmessage.MustNewName(target),
			Params:   params,
		},
	}
}

func cnameRR(name string, ttl uint32, target string) dnsmessage.Resource {
	return dnsmessage.Resource{
		Header: header(name, ttl),
		Body: &dnsmessage.CNAMEResource{
			CNAME: dnsmessage.MustNewName(target),
		},
	}
}

func soaRR(name string, ttl, minTTL uint32) dnsmessage.Resource {
	return dnsmessage.Resource{
		Header: header(name, ttl),
		Body: &dnsmessage.SOAResource{
			NS:     dnsmessage.MustNewName("ns." + name),
			MBox:   dnsmessage.MustNewName("hostmaster." + name),
			Serial: 1,
			MinTTL: minTTL,
		},
	}
}

func param(key dnsmessage.SVCParamKey, value ...byte) dnsmessage.SVCParam {
	return dnsmessage.SVCParam{
		Key:   key,
		Value: value,
	}
}

// alpnH2H3 is the alpn SvcParam value "h2,h3".
var alpnH2H3 = []byte{2, 'h', '2', 2, 'h', '3'}

// lookupStep is one Lookup of a TestLookup row.
type lookupStep struct {
	// advance moves the clock first.
	advance time.Duration
	want    Record
	wantOK  bool
	wantErr bool
	// queries is how many queries the server has had after it, in all.
	queries int
}

// TestLookup checks what no capture reaches: an answer parsed from a
// hostile or odd nameserver (spoofed IDs, truncation, CNAMEs, AliasMode,
// unknown mandatory keys) and the cache lifetimes that keep a broken
// resolver from stalling every connection.
func TestLookup(t *testing.T) {
	t.Parallel()

	rows := []struct {
		name string
		host string
		port int
		// wantName is every question's name; wantTCP how many came over TCP.
		wantName string
		wantTCP  int
		answer   func(q query) [][]byte
		steps    []lookupStep
	}{
		{
			name:     "ECH and ALPN, cached until the smallest TTL",
			host:     "example.com",
			port:     443,
			wantName: "example.com.",
			answer: answers(reply{
				answers: []dnsmessage.Resource{
					httpsRR("example.com.", 600, 2, ".",
						param(dnsmessage.SVCParamALPN, 2, 'h', '2'),
					),
					httpsRR("example.com.", 300, 1, ".",
						param(dnsmessage.SVCParamALPN, alpnH2H3...),
						param(dnsmessage.SVCParamNoDefaultALPN),
						param(dnsmessage.SVCParamECH, 0xfe, 0x0d, 0, 1),
					),
				},
			}),
			steps: []lookupStep{
				{
					want: Record{
						ECH:           []byte{0xfe, 0x0d, 0, 1},
						ALPN:          []string{"h2", "h3"},
						NoDefaultALPN: true,
					},
					wantOK:  true,
					queries: 1,
				},
				{
					advance: 299 * time.Second,
					want: Record{
						ECH:           []byte{0xfe, 0x0d, 0, 1},
						ALPN:          []string{"h2", "h3"},
						NoDefaultALPN: true,
					},
					wantOK:  true,
					queries: 1,
				},
				{
					advance: time.Second,
					want: Record{
						ECH:           []byte{0xfe, 0x0d, 0, 1},
						ALPN:          []string{"h2", "h3"},
						NoDefaultALPN: true,
					},
					wantOK:  true,
					queries: 2,
				},
			},
		},
		{
			name:     "port other than 443",
			host:     "Example.COM.",
			port:     8443,
			wantName: "_8443._https.example.com.",
			answer: answers(reply{
				answers: []dnsmessage.Resource{
					httpsRR("_8443._https.example.com.", 300, 1, ".",
						param(dnsmessage.SVCParamALPN, alpnH2H3...),
					),
				},
			}),
			steps: []lookupStep{
				{
					want: Record{
						ALPN: []string{"h2", "h3"},
					},
					wantOK:  true,
					queries: 1,
				},
			},
		},
		{
			name:     "truncated over UDP, retried over TCP",
			host:     "example.com",
			port:     443,
			wantName: "example.com.",
			wantTCP:  1,
			answer: func(q query) [][]byte {
				if !q.tcp {
					return answers(reply{
						truncated: true,
					})(q)
				}

				return answers(reply{
					answers: []dnsmessage.Resource{
						httpsRR("example.com.", 300, 1, ".",
							param(dnsmessage.SVCParamECH, 1, 2, 3),
						),
					},
				})(q)
			},
			steps: []lookupStep{
				{
					want: Record{
						ECH: []byte{1, 2, 3},
					},
					wantOK:  true,
					queries: 2,
				},
			},
		},
		{
			name:     "CNAME chain, cached for its smallest TTL",
			host:     "example.com",
			port:     443,
			wantName: "example.com.",
			answer: answers(reply{
				answers: []dnsmessage.Resource{
					cnameRR("example.com.", 60, "a.example.net."),
					httpsRR("other.example.", 300, 1, ".",
						param(dnsmessage.SVCParamECH, 9),
					),
					cnameRR("A.example.net.", 300, "b.example.net."),
					httpsRR("b.example.net.", 300, 1, "b.example.net.",
						param(dnsmessage.SVCParamECH, 1),
					),
				},
			}),
			steps: []lookupStep{
				{
					want: Record{
						ECH: []byte{1},
					},
					wantOK:  true,
					queries: 1,
				},
				{
					advance: 59 * time.Second,
					want: Record{
						ECH: []byte{1},
					},
					wantOK:  true,
					queries: 1,
				},
				{
					advance: time.Second,
					want: Record{
						ECH: []byte{1},
					},
					wantOK:  true,
					queries: 2,
				},
			},
		},
		{
			name:     "AliasMode only",
			host:     "example.com",
			port:     443,
			wantName: "example.com.",
			answer: answers(reply{
				answers: []dnsmessage.Resource{
					httpsRR("example.com.", 300, 0, "svc.example.net."),
				},
			}),
			steps: []lookupStep{
				{
					queries: 1,
				},
			},
		},
		{
			name:     "unknown mandatory key",
			host:     "example.com",
			port:     443,
			wantName: "example.com.",
			answer: answers(reply{
				answers: []dnsmessage.Resource{
					httpsRR("example.com.", 300, 1, ".",
						param(dnsmessage.SVCParamMandatory, 0xfd, 0xe8),
						param(dnsmessage.SVCParamECH, 1),
						param(65000, 1),
					),
					httpsRR("example.com.", 300, 2, ".",
						param(dnsmessage.SVCParamMandatory, 0, 1, 0, 5),
						param(dnsmessage.SVCParamALPN, alpnH2H3...),
						param(dnsmessage.SVCParamECH, 2),
					),
				},
			}),
			steps: []lookupStep{
				{
					want: Record{
						ECH:  []byte{2},
						ALPN: []string{"h2", "h3"},
					},
					wantOK:  true,
					queries: 1,
				},
			},
		},
		{
			name:     "NXDOMAIN cached for the SOA's negative TTL",
			host:     "example.com",
			port:     443,
			wantName: "example.com.",
			answer: answers(reply{
				rcode: dnsmessage.RCodeNameError,
				authority: []dnsmessage.Resource{
					soaRR("example.com.", 900, 120),
				},
			}),
			steps: []lookupStep{
				{
					queries: 1,
				},
				{
					advance: 119 * time.Second,
					queries: 1,
				},
				{
					advance: time.Second,
					queries: 2,
				},
			},
		},
		{
			name:     "SERVFAIL tries each nameserver, cached for 30 s",
			host:     "example.com",
			port:     443,
			wantName: "example.com.",
			answer: answers(reply{
				rcode: dnsmessage.RCodeServerFailure,
			}),
			steps: []lookupStep{
				{
					wantErr: true,
					queries: 2,
				},
				{
					advance: 29 * time.Second,
					wantErr: true,
					queries: 2,
				},
				{
					advance: time.Second,
					wantErr: true,
					queries: 4,
				},
			},
		},
		{
			name:     "answer with another ID ignored",
			host:     "example.com",
			port:     443,
			wantName: "example.com.",
			answer: func(q query) [][]byte {
				return [][]byte{
					reply{
						id: q.header.ID ^ 1,
						answers: []dnsmessage.Resource{
							httpsRR("example.com.", 300, 1, ".",
								param(dnsmessage.SVCParamECH, 6, 6, 6),
							),
						},
					}.to(q),
					reply{
						answers: []dnsmessage.Resource{
							httpsRR("example.com.", 300, 1, ".",
								param(dnsmessage.SVCParamECH, 1),
							),
						},
					}.to(q),
				}
			},
			steps: []lookupStep{
				{
					want: Record{
						ECH: []byte{1},
					},
					wantOK:  true,
					queries: 1,
				},
			},
		},
	}

	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()

			s := startServer(t, row.answer)
			clock := clockwork.NewFakeClockAt(time.Date(2026, 11, 9, 12, 0, 0, 0, time.UTC))
			r := newResolver(s, clock)

			for i, step := range row.steps {
				clock.Advance(step.advance)

				rec, ok, err := r.Lookup(t.Context(), row.host, row.port)
				if (err != nil) != step.wantErr {
					t.Fatalf("step %d: error %v, want error %v", i, err, step.wantErr)
				}

				if ok != step.wantOK || !reflect.DeepEqual(rec, step.want) {
					t.Errorf("step %d: %+v, %v; want %+v, %v", i, rec, ok, step.want, step.wantOK)
				}

				if queries, _ := s.got(); len(queries) != step.queries {
					t.Errorf("step %d: %d queries, want %d", i, len(queries), step.queries)
				}
			}

			queries, dials := s.got()
			tcp := 0

			for _, q := range queries {
				if q.question.Name.String() != row.wantName || q.question.Type != dnsmessage.TypeHTTPS || q.question.Class != dnsmessage.ClassINET {
					t.Errorf("question %v, want %s HTTPS IN", q.question, row.wantName)
				}

				if !q.header.RecursionDesired || q.udpSize != ednsSize {
					t.Errorf("query RD %v, EDNS(0) size %d; want RD, %d", q.header.RecursionDesired, q.udpSize, ednsSize)
				}

				if q.tcp {
					tcp++
				}
			}

			if tcp != row.wantTCP {
				t.Errorf("%d queries over TCP, want %d", tcp, row.wantTCP)
			}

			for _, d := range dials {
				if !slices.Contains(testNameservers, d) {
					t.Errorf("dialled %s, want one of %v", d, testNameservers)
				}
			}
		})
	}
}

// TestLookupSharesQuery checks that lookups of one key while its query is
// unanswered send no other, that a caller whose context ends returns at
// once, and that the query it started goes on for the rest.
func TestLookupSharesQuery(t *testing.T) {
	t.Parallel()

	const n = 8

	release := make(chan struct{})
	open := sync.OnceFunc(func() {
		close(release)
	})
	s := startServer(t, func(q query) [][]byte {
		<-release

		return answers(reply{
			answers: []dnsmessage.Resource{
				httpsRR("example.com.", 300, 1, ".",
					param(dnsmessage.SVCParamECH, 1),
				),
			},
		})(q)
	})
	t.Cleanup(open) // before the server's cleanup, which waits for it

	r := newResolver(s, clockwork.NewFakeClock())

	cancelled, cancel := context.WithCancel(t.Context())
	cancel()

	// Each starts or joins the query and returns, the server holding it.
	for range n {
		_, _, err := r.Lookup(cancelled, "example.com", 443)
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("cancelled lookup: %v, want context.Canceled", err)
		}
	}

	var wg sync.WaitGroup

	recs := make([]Record, n)
	errs := make([]error, n)

	for i := range n {
		wg.Go(func() {
			recs[i], _, errs[i] = r.Lookup(t.Context(), "example.com", 443)
		})
	}

	open()
	wg.Wait()

	for i := range n {
		if errs[i] != nil || !reflect.DeepEqual(recs[i].ECH, []byte{1}) {
			t.Errorf("lookup %d: %+v, %v; want ECH 01", i, recs[i], errs[i])
		}
	}

	if queries, _ := s.got(); len(queries) != 1 {
		t.Errorf("%d queries, want 1", len(queries))
	}
}
