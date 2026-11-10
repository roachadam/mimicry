package httpsrr

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"slices"
	"strconv"
	"strings"
	"time"

	"golang.org/x/net/dns/dnsmessage"
)

const (
	// ednsSize is the UDP payload size the query advertises (EDNS(0)), the
	// DNS flag day 2020 value Go's resolver uses too.
	ednsSize = 1232
	// maxDatagram is the largest DNS message a datagram can carry: an
	// answer larger than the query advertised is read whole, not cut short
	// into one that never parses.
	maxDatagram = 65535
	// minTryTimeout is the least time one nameserver is given.
	minTryTimeout = time.Second
	// maxCNAMEs bounds the CNAME chain followed within an answer.
	maxCNAMEs = 16
)

// defaultNameservers are queried when the system names none, as Go's
// resolver does.
var defaultNameservers = []string{
	"127.0.0.1:53",
	"[::1]:53",
}

// mandatoryKnown are the SvcParamKeys a record may make mandatory and
// still be used: those this package or the dial it informs understands
// (RFC 9460, section 8).
var mandatoryKnown = []dnsmessage.SVCParamKey{
	dnsmessage.SVCParamALPN,
	dnsmessage.SVCParamNoDefaultALPN,
	dnsmessage.SVCParamPort,
	dnsmessage.SVCParamIPv4Hint,
	dnsmessage.SVCParamECH,
	dnsmessage.SVCParamIPv6Hint,
}

var (
	errMismatch = errors.New("answer does not match the query")
	errCNAMEs   = errors.New("CNAME chain too long")
)

// response is a nameserver's answer to the query, its parser at the
// answer section.
type response struct {
	header dnsmessage.Header
	parser dnsmessage.Parser
}

// lookup queries the nameservers in turn for k's HTTPS RRset, moving on
// from one that fails, and returns the result and how long to cache it.
func (r *Resolver) lookup(ctx context.Context, k key) (result, time.Duration) {
	name := k.host + "."
	if k.port != 443 {
		name = "_" + strconv.Itoa(k.port) + "._https." + name
	}

	res, ttl, err := r.query(ctx, name)
	if err != nil {
		return result{
			err: fmt.Errorf("httpsrr: lookup %s: %w", name, err),
		}, failedTTL
	}

	return res, ttl
}

// query asks the nameservers in turn for name's HTTPS RRset.
func (r *Resolver) query(ctx context.Context, name string) (result, time.Duration, error) {
	qname, err := dnsmessage.NewName(name)
	if err != nil {
		return result{}, 0, err
	}

	q := dnsmessage.Question{
		Name:  qname,
		Type:  dnsmessage.TypeHTTPS,
		Class: dnsmessage.ClassINET,
	}

	id := newID()

	msg, err := buildQuery(id, q)
	if err != nil {
		return result{}, 0, err
	}

	servers := r.nameservers()
	try := max(r.timeout/time.Duration(len(servers)), minTryTimeout)

	for _, server := range servers {
		resp, tryErr := r.ask(ctx, server, msg, id, q, try)
		if tryErr == nil {
			return interpret(resp, name)
		}

		err = fmt.Errorf("%s: %w", server, tryErr)

		if ctx.Err() != nil {
			break
		}
	}

	return result{}, 0, err
}

// newID returns a random DNS message ID.
func newID() uint16 {
	var b [2]byte

	_, _ = rand.Read(b[:]) // never fails (crypto/rand.Read's documentation)

	return binary.BigEndian.Uint16(b[:])
}

// buildQuery returns the query for q, with ID id, recursion desired and
// an EDNS(0) OPT record, after two bytes left for its length over TCP.
func buildQuery(id uint16, q dnsmessage.Question) ([]byte, error) {
	b := dnsmessage.NewBuilder(make([]byte, 2, 514), dnsmessage.Header{
		ID:               id,
		RecursionDesired: true,
	})
	b.EnableCompression()

	err := b.StartQuestions()
	if err != nil {
		return nil, err
	}

	err = b.Question(q)
	if err != nil {
		return nil, err
	}

	var opt dnsmessage.ResourceHeader

	err = opt.SetEDNS0(ednsSize, dnsmessage.RCodeSuccess, false)
	if err != nil {
		return nil, err
	}

	err = b.StartAdditionals()
	if err != nil {
		return nil, err
	}

	err = b.OPTResource(opt, dnsmessage.OPTResource{})
	if err != nil {
		return nil, err
	}

	msg, err := b.Finish()
	if err != nil {
		return nil, err
	}

	binary.BigEndian.PutUint16(msg, uint16(len(msg)-2))

	return msg, nil
}

// ask sends msg to server within try, over UDP and again over TCP when the
// answer is truncated, and returns the answer of an RCODE Lookup reports
// on (NOERROR, NXDOMAIN).
func (r *Resolver) ask(ctx context.Context, server string, msg []byte, id uint16, q dnsmessage.Question, try time.Duration) (response, error) {
	ctx, cancel := context.WithTimeout(ctx, try)
	defer cancel()

	resp, err := r.roundTrip(ctx, "udp", server, msg, id, q)
	if err == nil && resp.header.Truncated {
		resp, err = r.roundTrip(ctx, "tcp", server, msg, id, q)
	}

	if err != nil {
		return response{}, err
	}

	switch resp.header.RCode {
	case dnsmessage.RCodeSuccess, dnsmessage.RCodeNameError:
		return resp, nil
	default:
		return response{}, fmt.Errorf("answered %v", resp.header.RCode)
	}
}

// roundTrip dials server on network and exchanges msg for its answer: as
// datagrams when the connection is a net.PacketConn, length-prefixed
// otherwise, as Go's resolver decides.
func (r *Resolver) roundTrip(ctx context.Context, network, server string, msg []byte, id uint16, q dnsmessage.Question) (response, error) {
	conn, err := r.dial(ctx, network, server)
	if err != nil {
		return response{}, err
	}

	defer func() {
		_ = conn.Close()
	}()

	if deadline, ok := ctx.Deadline(); ok {
		err = conn.SetDeadline(deadline)
		if err != nil {
			return response{}, err
		}
	}

	if _, ok := conn.(net.PacketConn); ok {
		return packetRoundTrip(conn, msg[2:], id, q)
	}

	return streamRoundTrip(conn, msg, id, q)
}

// dial connects to server through the caller's resolver's Dial, if set.
func (r *Resolver) dial(ctx context.Context, network, server string) (net.Conn, error) {
	if r.resolver != nil && r.resolver.Dial != nil {
		return r.resolver.Dial(ctx, network, server)
	}

	var d net.Dialer

	return d.DialContext(ctx, network, server)
}

// packetRoundTrip sends msg as a datagram and returns the first answer to
// it, ignoring datagrams that are not one (another ID or question,
// unparsable), as a spoofer's.
func packetRoundTrip(conn net.Conn, msg []byte, id uint16, q dnsmessage.Question) (response, error) {
	_, err := conn.Write(msg)
	if err != nil {
		return response{}, err
	}

	buf := make([]byte, maxDatagram)

	for {
		n, err := conn.Read(buf)
		if err != nil {
			return response{}, err
		}

		resp, err := parse(buf[:n], id, q)
		if err == nil {
			return resp, nil
		}
	}
}

// streamRoundTrip sends msg with its length prefix and reads the answer.
func streamRoundTrip(conn net.Conn, msg []byte, id uint16, q dnsmessage.Question) (response, error) {
	_, err := conn.Write(msg)
	if err != nil {
		return response{}, err
	}

	var length [2]byte

	_, err = io.ReadFull(conn, length[:])
	if err != nil {
		return response{}, err
	}

	buf := make([]byte, binary.BigEndian.Uint16(length[:]))

	_, err = io.ReadFull(conn, buf)
	if err != nil {
		return response{}, err
	}

	return parse(buf, id, q)
}

// parse starts parsing msg and checks that it answers the query: ID id,
// the response flag, and q as its one question.
func parse(msg []byte, id uint16, q dnsmessage.Question) (response, error) {
	var p dnsmessage.Parser

	h, err := p.Start(msg)
	if err != nil {
		return response{}, err
	}

	if h.ID != id || !h.Response {
		return response{}, errMismatch
	}

	qs, err := p.AllQuestions()
	if err != nil {
		return response{}, err
	}

	if len(qs) != 1 || qs[0].Type != q.Type || qs[0].Class != q.Class || !strings.EqualFold(qs[0].Name.String(), q.Name.String()) {
		return response{}, errMismatch
	}

	return response{
		header: h,
		parser: p,
	}, nil
}

// cname is a CNAME record of the answer.
type cname struct {
	target string
	ttl    uint32
}

// https is an HTTPS record of the answer.
type https struct {
	owner string
	ttl   uint32
	rr    dnsmessage.HTTPSResource
}

// interpret reads the answer to name's query: the HTTPS RRs owned by the
// end of the CNAME chain from name, or, with none, the negative TTL.
func interpret(resp response, name string) (result, time.Duration, error) {
	p := &resp.parser
	cnames := map[string]cname{}

	var rrs []https

	for {
		h, err := p.AnswerHeader()
		if errors.Is(err, dnsmessage.ErrSectionDone) {
			break
		}

		if err != nil {
			return result{}, 0, err
		}

		switch {
		case h.Class == dnsmessage.ClassINET && h.Type == dnsmessage.TypeCNAME:
			c, err := p.CNAMEResource()
			if err != nil {
				return result{}, 0, err
			}

			cnames[strings.ToLower(h.Name.String())] = cname{
				target: strings.ToLower(c.CNAME.String()),
				ttl:    h.TTL,
			}

		case h.Class == dnsmessage.ClassINET && h.Type == dnsmessage.TypeHTTPS:
			rr, err := p.HTTPSResource()
			if err != nil {
				return result{}, 0, err
			}

			rrs = append(rrs, https{
				owner: strings.ToLower(h.Name.String()),
				ttl:   h.TTL,
				rr:    rr,
			})

		default:
			err = p.SkipAnswer()
			if err != nil {
				return result{}, 0, err
			}
		}
	}

	owner := strings.ToLower(name)
	ttl := uint32(maxTTL / time.Second)

	for hops := 0; ; hops++ {
		c, ok := cnames[owner]
		if !ok {
			break
		}

		if hops == maxCNAMEs {
			return result{}, 0, errCNAMEs
		}

		owner = c.target
		ttl = min(ttl, c.ttl)
	}

	var owned []dnsmessage.HTTPSResource

	for _, rr := range rrs {
		if rr.owner == owner {
			owned = append(owned, rr.rr)
			ttl = min(ttl, rr.ttl)
		}
	}

	if resp.header.RCode == dnsmessage.RCodeNameError || len(owned) == 0 {
		negative, err := negativeTTL(p)
		if err != nil {
			return result{}, 0, err
		}

		return result{}, seconds(min(ttl, negative)), nil
	}

	rec, ok := choose(owned, owner)

	return result{
		rec: rec,
		ok:  ok,
	}, seconds(ttl), nil
}

// seconds is ttl as a duration.
func seconds(ttl uint32) time.Duration {
	return time.Duration(ttl) * time.Second
}

// negativeTTL returns how long NXDOMAIN or NODATA may be cached: the
// smaller of the authority section's SOA's TTL and its MINIMUM, zero
// without a SOA (RFC 2308, section 5).
func negativeTTL(p *dnsmessage.Parser) (uint32, error) {
	for {
		h, err := p.AuthorityHeader()
		if errors.Is(err, dnsmessage.ErrSectionDone) {
			return 0, nil
		}

		if err != nil {
			return 0, err
		}

		if h.Type != dnsmessage.TypeSOA || h.Class != dnsmessage.ClassINET {
			err = p.SkipAuthority()
			if err != nil {
				return 0, err
			}

			continue
		}

		soa, err := p.SOAResource()
		if err != nil {
			return 0, err
		}

		return min(h.TTL, soa.MinTTL), nil
	}
}

// choose returns the usable ServiceMode record of lowest SvcPriority in
// owner's RRset, if any. A set with an AliasMode record has none: its
// ServiceMode records are ignored (RFC 9460, section 2.4.2) and the alias
// is not followed. A usable record's TargetName is "." or the owner (an
// alternative endpoint elsewhere is not what mimicry dials), and its
// mandatory keys are all ones it understands.
func choose(rrs []dnsmessage.HTTPSResource, owner string) (Record, bool) {
	var (
		best     Record
		priority uint16
		found    bool
	)

	for _, rr := range rrs {
		if rr.Priority == 0 {
			return Record{}, false
		}

		if found && rr.Priority >= priority {
			continue
		}

		target := strings.ToLower(rr.Target.String())
		if target != "." && target != owner {
			continue
		}

		rec, ok := usable(rr.SVCBResource)
		if ok {
			best, priority, found = rec, rr.Priority, true
		}
	}

	return best, found
}

// usable returns what mimicry uses of rr, and whether it may be used: its
// mandatory keys are known and its alpn well-formed.
func usable(rr dnsmessage.SVCBResource) (Record, bool) {
	var rec Record

	for _, param := range rr.Params {
		switch param.Key {
		case dnsmessage.SVCParamMandatory:
			if !mandatoryOK(param.Value) {
				return Record{}, false
			}

		case dnsmessage.SVCParamALPN:
			alpn, ok := parseALPN(param.Value)
			if !ok {
				return Record{}, false
			}

			rec.ALPN = alpn

		case dnsmessage.SVCParamNoDefaultALPN:
			rec.NoDefaultALPN = true
		case dnsmessage.SVCParamECH:
			rec.ECH = param.Value
		default:
			// port, ipv4hint and ipv6hint are not used; other keys are
			// ignored unless mandatory.
		}
	}

	return rec, true
}

// mandatoryOK reports whether the mandatory SvcParam's value is a list of
// keys all in mandatoryKnown.
func mandatoryOK(value []byte) bool {
	if len(value) == 0 || len(value)%2 != 0 {
		return false
	}

	for i := 0; i < len(value); i += 2 {
		k := dnsmessage.SVCParamKey(binary.BigEndian.Uint16(value[i:]))
		if !slices.Contains(mandatoryKnown, k) {
			return false
		}
	}

	return true
}

// parseALPN returns the protocols of an alpn SvcParam's value, a
// non-empty list of non-empty length-prefixed IDs (RFC 9460, section
// 7.1.1).
func parseALPN(value []byte) ([]string, bool) {
	if len(value) == 0 {
		return nil, false
	}

	var ids []string

	for len(value) > 0 {
		n := int(value[0])
		if n == 0 || len(value) < 1+n {
			return nil, false
		}

		ids = append(ids, string(value[1:1+n]))
		value = value[1+n:]
	}

	return ids, true
}
