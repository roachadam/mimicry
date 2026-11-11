package server

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"

	"github.com/roachadam/mimicry"
	"github.com/roachadam/mimicry/cookies"
	"github.com/roachadam/mimicry/internal/rpc/convert"
	"github.com/roachadam/mimicry/internal/rpc/pb"
	"github.com/roachadam/mimicry/internal/rpc/wire"
	"github.com/roachadam/mimicry/profile"
	"github.com/roachadam/mimicry/profiles"
)

// profileOf returns the profile ref names.
func (s *session) profileOf(ref *pb.ProfileRef) (profile.Profile, error) {
	switch p := ref.GetProfile().(type) {
	case *pb.ProfileRef_Name:
		return profiles.ByName(p.Name)
	case *pb.ProfileRef_Id:
		pp, err := lookup[*parsedProfile](&s.objects, p.Id, "profile")
		if err != nil {
			return profile.Profile{}, err
		}

		return pp.p, nil

	default:
		return profile.Profile{}, convert.Invalid("no profile named")
	}
}

// config returns c as a mimicry.Config, its objects resolved, and the
// transport object its callbacks belong to (verify, keyLog; t and client
// unset). On error nothing stays open.
func (s *session) config(c *pb.Config) (mimicry.Config, *transport, error) {
	cfg, err := convert.Config(c)
	if err != nil {
		return mimicry.Config{}, nil, err
	}

	if cfg.Profile, err = s.profileOf(c.GetProfile()); err != nil {
		return mimicry.Config{}, nil, err
	}

	if id := c.GetCookies(); id != 0 {
		if cfg.Cookies, err = lookup[*cookies.Jar](&s.objects, id, "jar"); err != nil {
			return mimicry.Config{}, nil, err
		}
	}

	if id := c.GetClientHintStore(); id != 0 {
		if cfg.ClientHintStore, err = lookup[*mimicry.MemoryClientHintStore](&s.objects, id, "client hint store"); err != nil {
			return mimicry.Config{}, nil, err
		}
	}

	if id := c.GetAltSvc(); id != 0 {
		if cfg.AltSvc, err = lookup[*mimicry.AltSvcCache](&s.objects, id, "Alt-Svc cache"); err != nil {
			return mimicry.Config{}, nil, err
		}
	}

	if id := c.GetTickets(); id != 0 {
		if cfg.Tickets, err = lookup[*mimicry.TicketCache](&s.objects, id, "ticket cache"); err != nil {
			return mimicry.Config{}, nil, err
		}
	}

	tr := &transport{}

	if c.GetVerifyConnection() {
		tr.verify = newVerifier(s, cfg.DialTimeout)
		cfg.VerifyConnection = tr.verify.verify
	}

	if path := c.GetKeyLogFile(); path != "" {
		f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o600)
		if err != nil {
			return mimicry.Config{}, nil, fmt.Errorf("mimicry-core: config.key_log_file: %w", err)
		}

		cfg.KeyLogWriter = f
		tr.keyLog = f
	}

	return cfg, tr, nil
}

// newClient is NewClient.
func (s *session) newClient(c *pb.NewClient) (*pb.Reply, error) {
	cfg, tr, err := s.config(c.GetConfig())
	if err != nil {
		return nil, err
	}

	client, err := mimicry.NewClient(cfg)
	if err != nil {
		free(tr)

		return nil, err
	}

	if r := c.GetRedirects(); r != nil {
		switch limit := int(r.GetMax()); {
		case r.Follow != nil && !r.GetFollow():
			client.CheckRedirect = func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			}

		case limit > 0:
			client.CheckRedirect = func(_ *http.Request, via []*http.Request) error {
				if len(via) > limit {
					return fmt.Errorf("mimicry-core: stopped after %d redirects", limit)
				}

				return nil
			}
		}
	}

	t, ok := client.Transport.(*mimicry.Transport)
	if !ok {
		free(tr)

		return nil, fmt.Errorf("mimicry-core: NewClient's transport is a %T", client.Transport)
	}

	tr.t, tr.client = t, client

	id := s.add(tr)

	jarID := c.GetConfig().GetCookies()
	if jarID == 0 {
		jarID = s.objects.add(t.Cookies())
	}

	return &pb.Reply{
		Result: &pb.Reply_NewClient{
			NewClient: &pb.NewClientReply{
				Client:  id,
				Cookies: jarID,
			},
		},
	}, nil
}

// newTransport is NewTransport.
func (s *session) newTransport(c *pb.NewTransport) (*pb.Reply, error) {
	cfg, tr, err := s.config(c.GetConfig())
	if err != nil {
		return nil, err
	}

	t, err := mimicry.NewTransport(cfg)
	if err != nil {
		free(tr)

		return nil, err
	}

	tr.t = t

	return object(s.add(tr)), nil
}

// add stores the transport tr, its verifier given the id before any call
// can look tr up.
func (s *session) add(tr *transport) uint64 {
	return s.objects.addNamed(tr, func(id uint64) {
		if tr.verify != nil {
			tr.verify.transport = id
		}
	})
}

// object is a reply naming a new object.
func object(id uint64) *pb.Reply {
	return &pb.Reply{
		Result: &pb.Reply_Object{
			Object: &pb.Object{
				Id: id,
			},
		},
	}
}

// do answers Do: the response's head as the REPLY, its body as DATA.
func (s *session) do(st *wire.Stream, c *pb.Do) {
	tr, err := lookup[*transport](&s.objects, c.GetClient(), "client or transport")
	if err != nil {
		_ = s.replyErr(st, err)

		return
	}

	ctx := st.Context()

	if d := c.GetRequest().GetTimeout(); d != nil {
		var cancel context.CancelFunc

		ctx, cancel = context.WithTimeout(ctx, d.AsDuration())
		defer cancel()
	}

	if tr.verify != nil {
		defer tr.verify.waiting(ctx)()
	}

	var body io.Reader
	if c.GetRequest().GetStreamBody() {
		body = st
	}

	req, err := convert.Request(ctx, c.GetRequest(), body)
	if err != nil {
		_ = s.replyErr(st, err)

		return
	}

	resp, err := tr.roundTrip(req)
	if err != nil {
		_ = s.replyErr(st, err)

		return
	}

	defer func() {
		_ = resp.Body.Close()
	}()

	s.respond(st, nil, resp)
}

// roundTrip sends req through the client, redirects followed, or the
// transport.
func (tr *transport) roundTrip(req *http.Request) (*http.Response, error) {
	if tr.client != nil {
		return tr.client.Do(req)
	}

	return tr.t.RoundTrip(req)
}

// respond sends resp's head as the REPLY, with err if the call failed
// (a WebSocket handshake), then its body as DATA.
func (s *session) respond(st *wire.Stream, err error, resp *http.Response) {
	r := &pb.Reply{
		Result: &pb.Reply_Response{
			Response: convert.Response(resp),
		},
	}
	if err != nil {
		r.Error = convert.Error(err)
	}

	noBody := resp.Body == nil || resp.Body == http.NoBody
	if s.reply(st, r, noBody) != nil || noBody {
		return
	}

	buf := make([]byte, wire.MaxPayload)

	for {
		n, err := resp.Body.Read(buf)
		if n > 0 {
			if _, werr := st.Write(buf[:n]); werr != nil {
				return
			}
		}

		switch {
		case errors.Is(err, io.EOF):
			// Closed before the stream ends: ending it cancels the
			// request's context, and a body still open then costs the
			// connection (HTTP/1.1 closes it, so the next request
			// opens another).
			_ = resp.Body.Close()
			_ = st.CloseWrite()

			return

		case err != nil:
			if st.Context().Err() == nil {
				s.failStream(st, err)
			}

			return
		}
	}
}

// plan is Plan.
func (s *session) plan(st *wire.Stream, c *pb.Plan) (*pb.Reply, error) {
	tr, err := lookup[*transport](&s.objects, c.GetTransport(), "client or transport")
	if err != nil {
		return nil, err
	}

	req, err := convert.Request(st.Context(), c.GetRequest(), nil)
	if err != nil {
		return nil, err
	}

	p, err := tr.t.Plan(req)
	if err != nil {
		return nil, err
	}

	return &pb.Reply{
		Result: &pb.Reply_Plan{
			Plan: convert.Plan(p),
		},
	}, nil
}

// fetchPins is FetchPins.
func (s *session) fetchPins(st *wire.Stream, c *pb.FetchPins) (*pb.Reply, error) {
	cfg, tr, err := s.config(c.GetConfig())
	if err != nil {
		return nil, err
	}

	defer free(tr)

	ctx := st.Context()

	if d := c.GetTimeout(); d != nil {
		var cancel context.CancelFunc

		ctx, cancel = context.WithTimeout(ctx, d.AsDuration())
		defer cancel()
	}

	pins, err := mimicry.FetchPins(ctx, cfg, c.GetUrl())
	if err != nil {
		return nil, err
	}

	return &pb.Reply{
		Result: &pb.Reply_Pins{
			Pins: convert.CertPins(pins),
		},
	}, nil
}
