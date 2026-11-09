package server

import (
	"net"
	"time"

	"github.com/roachadam/mimicry/internal/harness/internal/capture"
)

const (
	recordTypeApplicationData = 23
	// tls13Overhead is what TLS 1.3 adds to a record's content: its inner
	// content type and the AEAD tag, 16 bytes for every TLS 1.3 suite.
	// Browsers do not pad, so content is the record's length less this.
	tls13Overhead = 1 + 16
	// openingRecords bounds how many of the client's application data
	// records are kept: the opening flight and the first request fit.
	openingRecords = 16
	// http2FrameHeaderLen is an HTTP/2 frame's header length (RFC 9113,
	// section 4.1).
	http2FrameHeaderLen = 9
)

// recordLog reads the client's TLS records under the TLS server, after its
// ClientHello, and keeps the content length of its first application data
// records: how the client cut its first flight into records, which the TLS
// server hides. TLS 1.3 only (its first application data record is the
// client's Finished, skipped). Only the connection's reading goroutine
// uses it.
type recordLog struct {
	net.Conn

	// all keeps every application data record, with when its header
	// arrived (at), not only the opening's: how the client cuts request
	// bodies (Scenario.recordUploads).
	all bool
	// header collects a record header across reads; left is what remains
	// of the current record's payload.
	header [recordHeaderLen]byte
	have   int
	left   int
	// finished: the client's Finished went by.
	finished bool
	// flight: the client's handshake flight may take several records (a
	// client certificate's, Scenario.clientCert), so application data
	// records wait in early, at most flightRecords, until
	// handshakeRecords says how many were the flight's.
	flight  bool
	early   []int
	earlyAt []time.Time
	// records are the application data records' content lengths, in order.
	records []int
	at      []time.Time
}

func (l *recordLog) Read(p []byte) (int, error) {
	n, err := l.Conn.Read(p)
	l.scan(p[:n])

	return n, err
}

// scan follows the record stream through b.
func (l *recordLog) scan(b []byte) {
	for len(b) > 0 {
		if l.left > 0 {
			k := min(l.left, len(b))

			l.left, b = l.left-k, b[k:]

			continue
		}

		k := copy(l.header[l.have:], b)

		l.have, b = l.have+k, b[k:]

		if l.have < recordHeaderLen {
			return
		}

		l.have = 0
		l.left = int(l.header[3])<<8 | int(l.header[4])

		if l.header[0] != recordTypeApplicationData {
			continue
		}

		if l.flight {
			if len(l.early) < flightRecords {
				l.early = append(l.early, l.left-tls13Overhead)
				l.earlyAt = append(l.earlyAt, time.Now())
			}

			continue
		}

		if !l.finished {
			l.finished = true

			continue
		}

		l.keep(l.left-tls13Overhead, time.Now())
	}
}

// flightRecords bounds the records a flight-mode recordLog holds before
// handshakeRecords: a client's handshake flight and its opening.
const flightRecords = 64

// handshakeRecords tells a flight-mode recordLog that the client's
// encrypted handshake flight took the first n application data records:
// the rest are its application data.
func (l *recordLog) handshakeRecords(n int) {
	early, at := l.early, l.earlyAt

	l.flight, l.finished = false, true
	l.early, l.earlyAt = nil, nil

	for i := min(n, len(early)); i < len(early); i++ {
		l.keep(early[i], at[i])
	}
}

// keep records an application data record of size bytes whose header
// arrived at at.
func (l *recordLog) keep(size int, at time.Time) {
	if l.all {
		l.records = append(l.records, size)
		l.at = append(l.at, at)
	} else if len(l.records) < openingRecords {
		l.records = append(l.records, size)
	}
}

// place returns the index of the record the client's application data
// byte at offset (the HTTP/2 preface's first is 0) arrived in, or -1 past
// the kept records.
func (l *recordLog) place(offset int) int {
	for i, n := range l.records {
		if offset < n {
			return i
		}

		offset -= n
	}

	return -1
}

// upload returns how the records carried the client's application data
// from offset (the connection's first byte after the handshake is 0) for
// head and then body bytes, all of which have arrived: the records from
// the one holding the first byte to the one holding the last, as runs of
// equal size. Nil when they are not all kept.
func (l *recordLog) upload(offset, head, body int) *capture.H1Upload {
	first := l.place(offset)
	last := l.place(offset + head + body - 1)

	if !l.all || first < 0 || last < 0 {
		return nil
	}

	u := &capture.H1Upload{
		Offset: offset,
		Head:   head,
		Body:   body,
	}

	start := 0
	for _, n := range l.records[:first] {
		start += n
	}

	end := start
	for _, n := range l.records[first : last+1] {
		end += n
	}

	u.Lead = offset - start
	u.Trail = end - (offset + head + body)

	for i := first; i <= last; i++ {
		ms := float64(l.at[i].Sub(l.at[first]).Microseconds()) / 1000
		if n := len(u.Records); n > 0 && u.Records[n-1].Size == l.records[i] {
			u.Records[n-1].Count++

			u.Records[n-1].Millis = ms

			continue
		}

		u.Records = append(u.Records, capture.RecordRun{
			Size:   l.records[i],
			Count:  1,
			Millis: ms,
		})
	}

	return u
}
