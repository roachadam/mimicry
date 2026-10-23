package server

import (
	"bytes"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"

	"golang.org/x/net/http2"
	"golang.org/x/net/http2/hpack"

	"github.com/roachadam/mimicry/harness/internal/capture"
	"github.com/roachadam/mimicry/profile"
)

var responseBody = []byte("mimicry capture ok\n")

// serveHTTP2 records the client's connection preface frames and first request
// into c, calls recorded once that request is in, and answers every request
// until the client goes away.
func serveHTTP2(conn net.Conn, c *capture.Capture, recorded func()) error {
	preface := make([]byte, len(http2.ClientPreface))
	if _, err := io.ReadFull(conn, preface); err != nil {
		return err
	}
	if string(preface) != http2.ClientPreface {
		return fmt.Errorf("bad HTTP/2 preface %q", preface)
	}
	fr := http2.NewFramer(conn, conn)
	fr.ReadMetaHeaders = hpack.NewDecoder(4096, nil)
	if err := fr.WriteSettings(); err != nil {
		return err
	}
	var block bytes.Buffer
	enc := hpack.NewEncoder(&block)
	h := &profile.HTTP2{}
	gotSettings, gotRequest := false, false
	for {
		f, err := fr.ReadFrame()
		if err != nil {
			return err
		}
		switch f := f.(type) {
		case *http2.SettingsFrame:
			if f.IsAck() {
				continue
			}
			if !gotSettings {
				gotSettings = true
				if err := f.ForeachSetting(func(s http2.Setting) error {
					h.Settings = append(h.Settings, profile.Setting{
						ID:    uint16(s.ID),
						Value: s.Val,
					})
					return nil
				}); err != nil {
					return err
				}
			}
			if err := fr.WriteSettingsAck(); err != nil {
				return err
			}
		case *http2.WindowUpdateFrame:
			if f.StreamID == 0 && !gotRequest && h.WindowUpdate == 0 {
				h.WindowUpdate = f.Increment
			}
		case *http2.PriorityFrame:
			if !gotRequest {
				h.Priorities = append(h.Priorities, profile.Priority{
					StreamID:      f.StreamID,
					PriorityParam: priorityParam(f.PriorityParam),
				})
			}
		case *http2.MetaHeadersFrame:
			status := 404
			if !gotRequest {
				gotRequest = true
				status = 200
				recordRequest(c, h, f)
				recorded()
			}
			if err := respond(fr, enc, &block, f.StreamID, status); err != nil {
				return err
			}
		case *http2.PingFrame:
			if !f.IsAck() {
				if err := fr.WritePing(true, f.Data); err != nil {
					return err
				}
			}
		case *http2.GoAwayFrame:
			return nil
		}
	}
}

func recordRequest(c *capture.Capture, h *profile.HTTP2, f *http2.MetaHeadersFrame) {
	if f.HasPriority() {
		p := priorityParam(f.Priority)
		h.HeadersPriority = &p
	}
	for _, field := range f.Fields {
		if strings.HasPrefix(field.Name, ":") {
			h.PseudoHeaderOrder = append(h.PseudoHeaderOrder, field.Name)
			continue
		}
		c.Profile.Headers = append(c.Profile.Headers, profile.Header{
			Name:  field.Name,
			Value: field.Value,
		})
	}
	c.Profile.HTTP2 = h
}

func priorityParam(p http2.PriorityParam) profile.PriorityParam {
	return profile.PriorityParam{
		DependsOn: p.StreamDep,
		Exclusive: p.Exclusive,
		Weight:    p.Weight,
	}
}

func respond(fr *http2.Framer, enc *hpack.Encoder, block *bytes.Buffer, stream uint32, status int) error {
	block.Reset()
	fields := []hpack.HeaderField{
		{
			Name:  ":status",
			Value: strconv.Itoa(status),
		},
		{
			Name:  "content-type",
			Value: "text/plain; charset=utf-8",
		},
		{
			Name:  "content-length",
			Value: strconv.Itoa(len(responseBody)),
		},
	}
	for _, field := range fields {
		if err := enc.WriteField(field); err != nil {
			return err
		}
	}
	if err := fr.WriteHeaders(http2.HeadersFrameParam{
		StreamID:      stream,
		BlockFragment: block.Bytes(),
		EndHeaders:    true,
	}); err != nil {
		return err
	}
	return fr.WriteData(stream, true, responseBody)
}
