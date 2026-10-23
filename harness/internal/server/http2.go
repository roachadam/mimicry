package server

import (
	"bytes"
	"encoding/hex"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/net/http2"
	"golang.org/x/net/http2/hpack"

	"github.com/roachadam/mimicry/harness/internal/capture"
	"github.com/roachadam/mimicry/harness/internal/h2trace"
	"github.com/roachadam/mimicry/profile"
)

// h2conn is the server side of one captured HTTP/2 connection.
type h2conn struct {
	fr       *http2.Framer
	start    time.Time
	scenario *Scenario

	// Read-loop state.
	dec     *hpack.Decoder
	c       *capture.Capture
	h       *profile.HTTP2
	trace   *h2trace.Trace
	tracing bool // false once the capture was handed over
	// The header block being assembled, with its HEADERS frame's flags.
	block         []byte
	blockID       uint32
	blockEnded    bool
	blockPriority *http2.PriorityParam
	streams       map[uint32]*h2stream
	// firstHeaders is set right after the first request's header block, so
	// a WINDOW_UPDATE that immediately follows it is recorded.
	firstHeaders bool

	// wmu serialises writes; enc is only used under it.
	wmu sync.Mutex
	enc *hpack.Encoder
	buf bytes.Buffer

	// mu guards the send windows and the DATA committed per stream.
	mu            sync.Mutex
	cond          *sync.Cond
	connWindow    int64
	initialWindow int64
	maxFrame      int
	sendWindows   map[uint32]*int64
	sent          map[uint32]uint64
	closed        bool
}

type h2stream struct {
	path     string
	uploaded int
}

// serveHTTP2 records the client's connection preface frames and first request
// into c's profile, traces every frame and header block into c.Trace, and
// answers requests as sc says until the client goes away. emit hands the
// capture over; it is called when the client asks for /done (a browser that
// ran the page's script) and, by the caller, when the connection ends.
func serveHTTP2(conn net.Conn, c *capture.Capture, sc *Scenario, emit func()) error {
	preface := make([]byte, len(http2.ClientPreface))
	if _, err := io.ReadFull(conn, preface); err != nil {
		return err
	}
	if string(preface) != http2.ClientPreface {
		return fmt.Errorf("bad HTTP/2 preface %q", preface)
	}
	s := &h2conn{
		fr:            http2.NewFramer(conn, conn),
		start:         time.Now(),
		scenario:      sc,
		dec:           hpack.NewDecoder(4096, nil),
		c:             c,
		h:             &profile.HTTP2{},
		trace:         &h2trace.Trace{},
		tracing:       true,
		streams:       map[uint32]*h2stream{},
		connWindow:    65535,
		initialWindow: 65535,
		maxFrame:      16384,
		sendWindows:   map[uint32]*int64{},
		sent:          map[uint32]uint64{},
	}
	// The client may move its encoder's table anywhere up to what we
	// advertise; until it says so, it uses the default 4096. Frames larger
	// than we advertise are rejected, as a real server would, so a browser
	// exceeding them shows up as an error rather than passing unnoticed.
	s.dec.SetAllowedMaxDynamicTableSize(max(4096, sc.tableSize()))
	s.fr.SetMaxReadFrameSize(sc.maxFrameSize())
	s.cond = sync.NewCond(&s.mu)
	s.enc = hpack.NewEncoder(&s.buf)
	c.Trace = s.trace
	defer func() {
		s.mu.Lock()
		s.closed = true
		s.cond.Broadcast()
		s.mu.Unlock()
	}()
	s.wmu.Lock()
	err := s.fr.WriteSettings(sc.Settings...)
	s.wmu.Unlock()
	if err != nil {
		return err
	}
	var senders sync.WaitGroup
	defer senders.Wait()
	gotSettings := false
	for {
		f, err := s.fr.ReadFrame()
		if err != nil {
			return err
		}
		s.record(f)
		afterFirstHeaders := s.firstHeaders
		s.firstHeaders = false
		switch f := f.(type) {
		case *http2.SettingsFrame:
			if f.IsAck() {
				continue
			}
			if err := s.applySettings(f, !gotSettings); err != nil {
				return err
			}
			gotSettings = true
		case *http2.WindowUpdateFrame:
			if f.StreamID == 0 && len(s.trace.Requests) == 0 && s.h.WindowUpdate == 0 {
				s.h.WindowUpdate = f.Increment
			}
			if afterFirstHeaders && f.StreamID == s.blockID {
				s.h.StreamWindowUpdate = f.Increment
			}
			s.mu.Lock()
			if f.StreamID == 0 {
				s.connWindow += int64(f.Increment)
			} else if w := s.sendWindows[f.StreamID]; w != nil {
				*w += int64(f.Increment)
			}
			s.cond.Broadcast()
			s.mu.Unlock()
		case *http2.PriorityFrame:
			if len(s.trace.Requests) == 0 {
				s.h.Priorities = append(s.h.Priorities, profile.Priority{
					StreamID:      f.StreamID,
					PriorityParam: priorityParam(f.PriorityParam),
				})
			}
		case *http2.HeadersFrame:
			s.block, s.blockID = append(s.block[:0], f.HeaderBlockFragment()...), f.StreamID
			s.blockEnded, s.blockPriority = f.StreamEnded(), nil
			if f.HasPriority() {
				p := f.Priority
				s.blockPriority = &p
			}
			if f.HeadersEnded() {
				if err := s.onHeaders(&senders, emit); err != nil {
					return err
				}
			}
		case *http2.ContinuationFrame:
			if f.StreamID != s.blockID {
				return fmt.Errorf("CONTINUATION for stream %d inside stream %d's block", f.StreamID, s.blockID)
			}
			s.block = append(s.block, f.HeaderBlockFragment()...)
			if f.HeadersEnded() {
				if err := s.onHeaders(&senders, emit); err != nil {
					return err
				}
			}
		case *http2.DataFrame:
			if err := s.onData(f, &senders, emit); err != nil {
				return err
			}
		case *http2.PingFrame:
			if !f.IsAck() {
				s.wmu.Lock()
				err := s.fr.WritePing(true, f.Data)
				s.wmu.Unlock()
				if err != nil {
					return err
				}
			}
		case *http2.GoAwayFrame:
			return nil
		}
	}
}

// record appends f to the trace while the capture is still being built.
func (s *h2conn) record(f http2.Frame) {
	if !s.tracing {
		return
	}
	hdr := f.Header()
	tf := h2trace.Frame{
		Millis: time.Since(s.start).Milliseconds(),
		Type:   hdr.Type.String(),
		Stream: hdr.StreamID,
		Flags:  uint8(hdr.Flags),
		Length: hdr.Length,
	}
	switch f := f.(type) {
	case *http2.WindowUpdateFrame:
		tf.Increment = f.Increment
		if f.StreamID != 0 {
			s.mu.Lock()
			tf.SentBefore = s.sent[f.StreamID]
			s.mu.Unlock()
		}
	case *http2.RSTStreamFrame:
		tf.ErrCode = f.ErrCode.String()
	case *http2.GoAwayFrame:
		tf.ErrCode = f.ErrCode.String()
	case *http2.PingFrame:
		tf.Data = hex.EncodeToString(f.Data[:])
	case *http2.PriorityFrame:
		tf.Priority = tracePriority(f.PriorityParam)
	case *http2.HeadersFrame:
		if f.HasPriority() {
			tf.Priority = tracePriority(f.Priority)
		}
	}
	s.trace.Frames = append(s.trace.Frames, tf)
}

func tracePriority(p http2.PriorityParam) *h2trace.Priority {
	return &h2trace.Priority{
		DependsOn: p.StreamDep,
		Exclusive: p.Exclusive,
		Weight:    p.Weight,
	}
}

func (s *h2conn) applySettings(f *http2.SettingsFrame, first bool) error {
	s.mu.Lock()
	err := f.ForeachSetting(func(st http2.Setting) error {
		if first {
			s.h.Settings = append(s.h.Settings, profile.Setting{
				ID:    uint16(st.ID),
				Value: st.Val,
			})
		}
		switch st.ID {
		case http2.SettingInitialWindowSize:
			delta := int64(st.Val) - s.initialWindow
			s.initialWindow = int64(st.Val)
			for _, w := range s.sendWindows {
				*w += delta
			}
		case http2.SettingMaxFrameSize:
			s.maxFrame = int(st.Val)
		}
		return nil
	})
	s.cond.Broadcast()
	s.mu.Unlock()
	if err != nil {
		return err
	}
	s.wmu.Lock()
	defer s.wmu.Unlock()
	return s.fr.WriteSettingsAck()
}

// onHeaders decodes the complete header block.
func (s *h2conn) onHeaders(senders *sync.WaitGroup, emit func()) error {
	id := s.blockID
	instructions, err := h2trace.Decode(s.dec, s.block)
	if err != nil {
		return err
	}
	st := &h2stream{}
	for _, ins := range instructions {
		if ins.Name == ":path" {
			st.path = ins.Value
		}
	}
	if s.tracing {
		s.trace.Requests = append(s.trace.Requests, h2trace.Request{
			Stream:       id,
			Path:         st.path,
			Block:        hex.EncodeToString(s.block),
			Instructions: instructions,
		})
	}
	if s.c.Profile.HTTP2 == nil {
		s.recordFirstRequest(instructions)
	}
	s.streams[id] = st
	if s.blockEnded {
		s.respond(id, st, senders, emit)
	}
	return nil
}

func (s *h2conn) recordFirstRequest(instructions []h2trace.Instruction) {
	if s.blockPriority != nil {
		p := priorityParam(*s.blockPriority)
		s.h.HeadersPriority = &p
	}
	if s.blockID != 1 {
		s.h.FirstStreamID = s.blockID
	}
	s.firstHeaders = true
	for _, ins := range instructions {
		if ins.Rep == h2trace.SizeUpdate {
			continue
		}
		if strings.HasPrefix(ins.Name, ":") {
			s.h.PseudoHeaderOrder = append(s.h.PseudoHeaderOrder, ins.Name)
			continue
		}
		s.c.Profile.Headers = append(s.c.Profile.Headers, profile.Header{
			Name:  ins.Name,
			Value: ins.Value,
		})
	}
	s.c.Profile.HTTP2 = s.h
}

func (s *h2conn) onData(f *http2.DataFrame, senders *sync.WaitGroup, emit func()) error {
	// Return the credit straight away, so the client's upload is paced by its
	// own behaviour rather than ours.
	if n := f.Header().Length; n > 0 {
		s.wmu.Lock()
		err := s.fr.WriteWindowUpdate(0, n)
		if err == nil && !f.StreamEnded() {
			err = s.fr.WriteWindowUpdate(f.StreamID, n)
		}
		s.wmu.Unlock()
		if err != nil {
			return err
		}
	}
	st := s.streams[f.StreamID]
	if st == nil {
		return nil
	}
	st.uploaded += len(f.Data())
	if f.StreamEnded() {
		s.respond(f.StreamID, st, senders, emit)
	}
	return nil
}

// respond answers a finished request. Bodies are sent from their own
// goroutine, since they wait for the client's flow-control credit.
func (s *h2conn) respond(id uint32, st *h2stream, senders *sync.WaitGroup, emit func()) {
	delete(s.streams, id)
	r := s.scenario.answer(st.path, st.uploaded)
	if st.path == "/done" && s.tracing {
		s.tracing = false
		s.c.Finished = true
		emit()
	}
	s.mu.Lock()
	w := s.initialWindow
	s.sendWindows[id] = &w
	s.mu.Unlock()
	senders.Go(func() {
		if err := s.send(id, r); err != nil {
			s.mu.Lock()
			s.closed = true
			s.cond.Broadcast()
			s.mu.Unlock()
		}
	})
}

func (s *h2conn) send(id uint32, r response) error {
	defer func() {
		s.mu.Lock()
		delete(s.sendWindows, id)
		s.mu.Unlock()
	}()
	s.wmu.Lock()
	s.buf.Reset()
	fields := []hpack.HeaderField{
		{
			Name:  ":status",
			Value: strconv.Itoa(r.status),
		},
		{
			Name:  "content-type",
			Value: r.contentType,
		},
		{
			Name:  "content-length",
			Value: strconv.Itoa(len(r.body)),
		},
		{
			Name:  "cache-control",
			Value: "no-store",
		},
	}
	for _, field := range append(fields, r.headers...) {
		_ = s.enc.WriteField(field)
	}
	err := s.fr.WriteHeaders(http2.HeadersFrameParam{
		StreamID:      id,
		BlockFragment: s.buf.Bytes(),
		EndHeaders:    true,
	})
	s.wmu.Unlock()
	if err != nil {
		return err
	}
	body := r.body
	for first := true; ; first = false {
		want := len(body)
		if r.dripChunk > 0 {
			want = min(want, r.dripChunk)
			if !first {
				time.Sleep(r.dripEvery)
			}
		}
		n, err := s.reserve(id, want)
		if err != nil {
			return err
		}
		s.wmu.Lock()
		err = s.fr.WriteData(id, n == len(body), body[:n])
		s.wmu.Unlock()
		if err != nil {
			return err
		}
		body = body[n:]
		if len(body) == 0 {
			return nil
		}
	}
}

// reserve waits for send window on stream id and the connection, and takes
// up to want bytes of it (at least one frame's worth when want is zero).
func (s *h2conn) reserve(id uint32, want int) (int, error) {
	if want == 0 {
		return 0, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for {
		if s.closed {
			return 0, net.ErrClosed
		}
		w := s.sendWindows[id]
		n := int64(min(want, s.maxFrame))
		n = min(n, *w, s.connWindow)
		if n > 0 {
			*w -= n
			s.connWindow -= n
			// Counted before the bytes are written, so a credit return can
			// never appear larger than the data it is for.
			s.sent[id] += uint64(n)
			return int(n), nil
		}
		s.cond.Wait()
	}
}

func priorityParam(p http2.PriorityParam) profile.PriorityParam {
	return profile.PriorityParam{
		DependsOn: p.StreamDep,
		Exclusive: p.Exclusive,
		Weight:    p.Weight,
	}
}
