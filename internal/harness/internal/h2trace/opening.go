package h2trace

import "fmt"

// settingsAck is a SETTINGS frame's ACK flag.
const settingsAck = 0x1

// frameHeaderLen and prefaceLen are what a frame's header and the client
// preface add to a record's content.
const (
	frameHeaderLen = 9
	prefaceLen     = 24
)

// Opening returns how the client cut the start of the connection into TLS
// records, as profile http2.openingRecords lists it: per record, the
// preface and each frame starting in it (a SETTINGS ACK as "SETTINGS_ACK"),
// through the record carrying the first request's HEADERS and the
// WINDOW_UPDATE for its stream right after it. Nil for a trace without
// records.
//
// ready reports whether that request was waiting as the connection opened:
// its HEADERS came before the client acknowledged the server's SETTINGS,
// which takes a round trip. A browser that opened the connection ahead of
// the request (Safari and Firefox for the page the capture tool opens)
// writes the opening alone.
func Opening(tr *Trace) (records [][]string, ready bool, err error) {
	if len(tr.Records) == 0 {
		return nil, false, nil
	}

	last, ready := -1, true

	for i, f := range tr.Frames {
		if f.Type == "SETTINGS" && f.Flags&settingsAck != 0 && last < 0 {
			ready = false
		}

		if f.Type == "HEADERS" {
			last = i
			if i+1 < len(tr.Frames) && tr.Frames[i+1].Type == "WINDOW_UPDATE" && tr.Frames[i+1].Stream == f.Stream {
				last = i + 1
			}

			break
		}
	}

	if last < 0 {
		return nil, false, nil
	}

	for i, r := range tr.Records {
		var names []string

		size := 0

		if r.Preface {
			// profile.OpeningPreface
			names = append(names, "preface")

			size += prefaceLen
		}

		end := false

		for _, k := range r.Frames {
			f := tr.Frames[k]
			name := f.Type

			if f.Type == "SETTINGS" && f.Flags&settingsAck != 0 {
				name = "SETTINGS_ACK"
			}

			names = append(names, name)

			size += frameHeaderLen + int(f.Length)

			end = end || k == last
		}

		if size != r.Size {
			if size < r.Size && len(r.Frames) > 0 && r.Frames[len(r.Frames)-1] == len(tr.Frames)-1 {
				// The trace stopped (at the page's /done) before the
				// record's last frames: nothing to tell.
				return nil, false, nil
			}

			return nil, false, fmt.Errorf("record %d: %d bytes of content, its frames %d: padded, or a frame split across records", i+1, r.Size, size)
		}

		records = append(records, names)

		if end {
			return records, ready, nil
		}
	}

	return nil, false, fmt.Errorf("the first request's frames are past the %d records kept", len(tr.Records))
}
