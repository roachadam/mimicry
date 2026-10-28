package server

import "net"

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

	// header collects a record header across reads; left is what remains
	// of the current record's payload.
	header [recordHeaderLen]byte
	have   int
	left   int
	// finished: the client's Finished went by.
	finished bool
	// records are the application data records' content lengths, in order.
	records []int
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

		if !l.finished {
			l.finished = true

			continue
		}

		if len(l.records) < openingRecords {
			l.records = append(l.records, l.left-tls13Overhead)
		}
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
