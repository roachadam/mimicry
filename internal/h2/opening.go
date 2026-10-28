package h2

import (
	"bytes"
	"net"

	"golang.org/x/net/http2"

	"github.com/roachadam/mimicry/profile"
)

// connWriter is where a connection's frames go. Each write goes to the
// connection at once, one TLS record, except while hold gathers writes
// into buf, which flush sends as one write: how the browser cuts its
// opening into records (profile http2.openingRecords), and a request's
// HEADERS, CONTINUATION and stream WINDOW_UPDATE, which every captured
// browser sends together.
type connWriter struct {
	conn net.Conn
	buf  []byte
	hold bool
	// pending: buf holds the end of the opening, which the first
	// request's frames join (a browser whose first HEADERS shares the
	// opening's record). acks counts SETTINGS ACKs written meanwhile,
	// sent right after it.
	pending bool
	acks    int
	// scratch backs buf: an opening and a request's frames fit.
	scratch [512]byte
}

func (w *connWriter) Write(p []byte) (int, error) {
	if w.hold {
		if w.buf == nil {
			w.buf = w.scratch[:0]
		}

		w.buf = append(w.buf, p...)

		return len(p), nil
	}

	if w.pending {
		// The read loop's ACK of the server's SETTINGS waits for the
		// request instead of cutting the opening off from it.
		if bytes.Equal(p, settingsAckFrame[:]) {
			w.acks++

			return len(p), nil
		}

		// Anything else (GOAWAY) means no request joins the opening.
		if err := w.flush(); err != nil {
			return 0, err
		}
	}

	return w.conn.Write(p)
}

// flush sends what hold gathered as one write, then the SETTINGS ACKs
// held back for it.
func (w *connWriter) flush() error {
	w.hold, w.pending = false, false

	if len(w.buf) > 0 {
		_, err := w.conn.Write(w.buf)

		w.buf = w.buf[:0]
		if cap(w.buf) > len(w.scratch) {
			// A long header block grew it: let it go.
			w.buf = w.scratch[:0]
		}

		if err != nil {
			return err
		}
	}

	for ; w.acks > 0; w.acks-- {
		if _, err := w.conn.Write(settingsAckFrame[:]); err != nil {
			return err
		}
	}

	return nil
}

// settingsAckFrame is a SETTINGS ACK as the framer writes it.
var settingsAckFrame = [...]byte{0, 0, 0, byte(http2.FrameSettings), byte(http2.FlagSettingsAck), 0, 0, 0, 0}

// openingItems is how many of record's entries are the opening's: the
// preface and frames written before the first request's HEADERS.
func openingItems(record []string) int {
	for i, e := range record {
		if e == profile.OpeningHeaders {
			return i
		}
	}

	return len(record)
}

// writePreface writes the client preface and the profile's SETTINGS,
// connection WINDOW_UPDATE and PRIORITY frames, in that order, cut into
// records as the profile's openingRecords says: without it, each in a
// record of its own. When the first request's frames join the last
// record, that record is left pending in w for writeHeaders.
func writePreface(w *connWriter, fr *http2.Framer, h2 *profile.HTTP2) error {
	records := h2.OpeningRecords
	// rec is the record being filled and n how many items it holds.
	rec, n := 0, 0

	w.hold = true

	// next ends an item: the record goes out once it holds its share, and
	// the next one gathers.
	next := func() error {
		if records != nil {
			n++

			k := openingItems(records[rec])
			if n < k {
				return nil
			}

			if k < len(records[rec]) {
				// The first request joins it.
				w.hold, w.pending = false, true

				return nil
			}

			rec, n = rec+1, 0
		}

		err := w.flush()

		w.hold = true

		return err
	}

	w.buf = append(w.scratch[:0], http2.ClientPreface...)

	if err := next(); err != nil {
		return err
	}

	settings := make([]http2.Setting, len(h2.Settings))
	for i, s := range h2.Settings {
		settings[i] = http2.Setting{
			ID:  http2.SettingID(s.ID),
			Val: s.Value,
		}
	}

	if err := fr.WriteSettings(settings...); err != nil {
		return err
	}

	if err := next(); err != nil {
		return err
	}

	if h2.WindowUpdate > 0 {
		if err := fr.WriteWindowUpdate(0, h2.WindowUpdate); err != nil {
			return err
		}

		if err := next(); err != nil {
			return err
		}
	}

	for _, p := range h2.Priorities {
		if err := fr.WritePriority(p.StreamID, priorityParam(p.PriorityParam)); err != nil {
			return err
		}

		if err := next(); err != nil {
			return err
		}
	}

	if w.pending {
		return nil
	}

	// Every record went out; the first request's frames make their own.
	return w.flush()
}
