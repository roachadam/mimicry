package server

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"google.golang.org/protobuf/proto"

	"github.com/roachadam/mimicry"
	"github.com/roachadam/mimicry/cookies"
	"github.com/roachadam/mimicry/internal/rpc/convert"
	"github.com/roachadam/mimicry/internal/rpc/pb"
	"github.com/roachadam/mimicry/internal/rpc/wire"
	"github.com/roachadam/mimicry/profile"
	"github.com/roachadam/mimicry/profiles"
)

// maxBody bounds the DATA a call reads whole (a profile's JSON).
const maxBody = 64 << 20

// call answers a call whose reply ends its stream.
func (s *session) call(st *wire.Stream, c *pb.Call) (*pb.Reply, error) {
	switch m := c.GetMethod().(type) {
	case *pb.Call_Hello:
		return nil, convert.Invalid("Hello is the first call only")
	case *pb.Call_Close:
		v, ok := s.objects.remove(m.Close.GetId())
		if !ok {
			return nil, convert.Invalid("no object %d", m.Close.GetId())
		}

		free(v)

		return &pb.Reply{}, nil

	case *pb.Call_ListProfiles:
		return &pb.Reply{
			Result: &pb.Reply_Profiles{
				Profiles: &pb.ProfileList{
					Names:     profiles.Names(),
					Supported: profiles.Supported(),
				},
			},
		}, nil

	case *pb.Call_ParseProfile:
		return s.parseProfile(st)
	case *pb.Call_Machines:
		return machines(m.Machines.GetPlatform())
	case *pb.Call_NewClient:
		return s.newClient(m.NewClient)
	case *pb.Call_NewTransport:
		return s.newTransport(m.NewTransport)
	case *pb.Call_NewJar:
		return s.newJar(m.NewJar)
	case *pb.Call_NewTicketCache:
		return object(s.objects.add(mimicry.NewTicketCache())), nil
	case *pb.Call_NewAltSvcCache:
		return object(s.objects.add(mimicry.NewAltSvcCache())), nil
	case *pb.Call_NewClientHintStore:
		return object(s.objects.add(mimicry.NewClientHintStore())), nil
	case *pb.Call_CloseIdleConnections:
		tr, err := lookup[*transport](&s.objects, m.CloseIdleConnections.GetTransport(), "client or transport")
		if err != nil {
			return nil, err
		}

		tr.t.CloseIdleConnections()

		return &pb.Reply{}, nil

	case *pb.Call_Plan:
		return s.plan(st, m.Plan)
	case *pb.Call_CloseWebsocket:
		w, err := lookup[*webSocket](&s.objects, m.CloseWebsocket.GetWebsocket(), "WebSocket")
		if err != nil {
			return nil, err
		}

		return &pb.Reply{}, w.ws.Close(int(m.CloseWebsocket.GetCode()), m.CloseWebsocket.GetReason())

	case *pb.Call_FetchPins:
		return s.fetchPins(st, m.FetchPins)
	case *pb.Call_ReadJson:
		r, err := lookup[interface{ ReadJSON(io.Reader) error }](&s.objects, m.ReadJson.GetId(), "jar, ticket cache, Alt-Svc cache or client hint store")
		if err != nil {
			return nil, err
		}

		return &pb.Reply{}, readAll(st, r.ReadJSON)

	case *pb.Call_ReadNetscape:
		j, err := lookup[*cookies.Jar](&s.objects, m.ReadNetscape.GetJar(), "jar")
		if err != nil {
			return nil, err
		}

		return &pb.Reply{}, readAll(st, j.ReadNetscape)

	case *pb.Call_Clear:
		c, err := lookup[interface{ Clear() }](&s.objects, m.Clear.GetId(), "jar, ticket cache or Alt-Svc cache")
		if err != nil {
			return nil, err
		}

		c.Clear()

		return &pb.Reply{}, nil

	case *pb.Call_ClientHints:
		h, err := lookup[*mimicry.MemoryClientHintStore](&s.objects, m.ClientHints.GetStore(), "client hint store")
		if err != nil {
			return nil, err
		}

		hints := h.ClientHints(m.ClientHints.GetOrigin())
		for i, v := range hints {
			hints[i] = convert.Text(v)
		}

		return &pb.Reply{
			Result: &pb.Reply_Strings{
				Strings: &pb.Strings{
					Values: hints,
				},
			},
		}, nil

	case *pb.Call_SetClientHints:
		h, err := lookup[*mimicry.MemoryClientHintStore](&s.objects, m.SetClientHints.GetStore(), "client hint store")
		if err != nil {
			return nil, err
		}

		h.SetClientHints(m.SetClientHints.GetOrigin(), m.SetClientHints.GetHints())

		return &pb.Reply{}, nil

	case nil:
		return nil, convert.Invalid("a call mimicry-core %s does not know", s.cfg.Version)
	default:
		return s.jarCall(c)
	}
}

// bodyCall answers a call whose reply is followed by a body.
func (s *session) bodyCall(c *pb.Call) (*pb.Reply, []byte, error) {
	var buf bytes.Buffer

	switch m := c.GetMethod().(type) {
	case *pb.Call_WriteJson:
		w, err := lookup[interface{ WriteJSON(io.Writer) error }](&s.objects, m.WriteJson.GetId(), "jar, ticket cache, Alt-Svc cache or client hint store")
		if err != nil {
			return nil, nil, err
		}

		if err := w.WriteJSON(&buf); err != nil {
			return nil, nil, err
		}

		return &pb.Reply{}, buf.Bytes(), nil

	case *pb.Call_WriteNetscape:
		j, err := lookup[*cookies.Jar](&s.objects, m.WriteNetscape.GetJar(), "jar")
		if err != nil {
			return nil, nil, err
		}

		if err := j.WriteNetscape(&buf); err != nil {
			return nil, nil, err
		}

		return &pb.Reply{}, buf.Bytes(), nil

	case *pb.Call_ProfileSpec:
		p, err := s.profileOf(m.ProfileSpec.GetProfile())
		if err != nil {
			return nil, nil, err
		}

		data, err := profile.Encode(p.Spec())
		if err != nil {
			return nil, nil, err
		}

		info := profileInfo(p)
		if ref, ok := m.ProfileSpec.GetProfile().GetProfile().(*pb.ProfileRef_Id); ok {
			info.Id = ref.Id
		}

		return &pb.Reply{
			Result: &pb.Reply_Profile{
				Profile: info,
			},
		}, data, nil

	default:
		return nil, nil, fmt.Errorf("mimicry-core: %T has no body", m)
	}
}

func profileInfo(p profile.Profile) *pb.ProfileInfo {
	return &pb.ProfileInfo{
		Name:      p.Name(),
		UserAgent: convert.Text(p.UserAgent()),
	}
}

// parseProfile is ParseProfile: the profile's JSON is the stream's DATA.
func (s *session) parseProfile(st *wire.Stream) (*pb.Reply, error) {
	data, err := io.ReadAll(io.LimitReader(st, maxBody+1))
	if err != nil {
		return nil, err
	}

	if len(data) > maxBody {
		return nil, convert.Invalid("a profile over %d bytes", maxBody)
	}

	p, err := profile.Parse(data)
	if err != nil {
		if _, ok := errors.AsType[*profile.ValidationError](err); ok {
			return nil, err
		}

		return nil, convert.Invalid("profile: %v", err)
	}

	info := profileInfo(p)

	info.Id = s.objects.add(&parsedProfile{
		p: p,
	})

	return &pb.Reply{
		Result: &pb.Reply_Profile{
			Profile: info,
		},
	}, nil
}

// machines is Machines.
func machines(platform string) (*pb.Reply, error) {
	out := &pb.MachineList{}

	for _, m := range profiles.Machines(platform) {
		b, err := json.Marshal(m)
		if err != nil {
			return nil, err
		}

		out.Machines = append(out.Machines, b)
	}

	return &pb.Reply{
		Result: &pb.Reply_Machines{
			Machines: out,
		},
	}, nil
}

// setCookieLine is how much of a Set-Cookie line a SetCookie notice
// carries when the whole would not fit a frame.
const setCookieLine = 4 << 10

// newJar is NewJar.
func (s *session) newJar(c *pb.NewJar) (*pb.Reply, error) {
	cfg := cookies.Config{}

	if c.GetRules() != nil {
		p, err := s.profileOf(c.GetRules())
		if err != nil {
			return nil, err
		}

		cfg.Rules = p.Spec().Cookies
	}

	var id uint64

	if c.GetOnSetCookie() {
		cfg.OnSetCookie = func(e cookies.Event) {
			n := convert.SetCookie(id, e)
			ev := &pb.Event{
				Event: &pb.Event_SetCookie{
					SetCookie: n,
				},
			}

			if proto.Size(ev) > wire.MaxPayload {
				n.Line = n.GetLine()[:min(len(n.GetLine()), setCookieLine)]
				if n.GetCookie() != nil {
					n.Cookie.Raw = n.Cookie.GetRaw()[:min(len(n.Cookie.GetRaw()), setCookieLine)]
				}
			}

			s.notify(ev)
		}
	}

	j, err := cookies.New(cfg)
	if err != nil {
		return nil, err
	}

	id = s.objects.add(j)

	return object(id), nil
}

// jarCall answers the Jar methods.
func (s *session) jarCall(c *pb.Call) (*pb.Reply, error) {
	id, err := jarOf(c)
	if err != nil {
		return nil, err
	}

	j, err := lookup[*cookies.Jar](&s.objects, id, "jar")
	if err != nil {
		return nil, err
	}

	switch m := c.GetMethod().(type) {
	case *pb.Call_JarAll:
		return cookieList(j.All()), nil
	case *pb.Call_JarCookies:
		r, err := convert.CookieRequest(m.JarCookies.GetRequest())
		if err != nil {
			return nil, err
		}

		return cookieList(j.Cookies(r)), nil

	case *pb.Call_JarPeek:
		r, err := convert.CookieRequest(m.JarPeek.GetRequest())
		if err != nil {
			return nil, err
		}

		return cookieList(j.Peek(r)), nil

	case *pb.Call_JarSet:
		return &pb.Reply{}, j.Set(convert.FromCookie(m.JarSet.GetCookie()))
	case *pb.Call_JarSetString:
		return &pb.Reply{}, j.SetString(m.JarSetString.GetDomain(), string(m.JarSetString.GetCookies()))
	case *pb.Call_JarSetHostString:
		return &pb.Reply{}, j.SetHostString(m.JarSetHostString.GetHost(), string(m.JarSetHostString.GetCookies()))
	case *pb.Call_JarSetCookies:
		r, err := convert.CookieRequest(m.JarSetCookies.GetRequest())
		if err != nil {
			return nil, err
		}

		j.SetCookies(r, convert.Strings(m.JarSetCookies.GetLines()))

		return &pb.Reply{}, nil

	case *pb.Call_JarClearExpired:
		return count(j.ClearExpired()), nil
	case *pb.Call_JarClearSession:
		return count(j.ClearSession()), nil
	case *pb.Call_JarString:
		return &pb.Reply{
			Result: &pb.Reply_Value{
				Value: []byte(j.String(m.JarString.GetName())),
			},
		}, nil

	default:
		return object(s.objects.add(j.Clone())), nil
	}
}

// jarOf returns the jar a Jar method names.
func jarOf(c *pb.Call) (uint64, error) {
	switch m := c.GetMethod().(type) {
	case *pb.Call_JarAll:
		return m.JarAll.GetJar(), nil
	case *pb.Call_JarCookies:
		return m.JarCookies.GetJar(), nil
	case *pb.Call_JarPeek:
		return m.JarPeek.GetJar(), nil
	case *pb.Call_JarSet:
		return m.JarSet.GetJar(), nil
	case *pb.Call_JarSetString:
		return m.JarSetString.GetJar(), nil
	case *pb.Call_JarSetHostString:
		return m.JarSetHostString.GetJar(), nil
	case *pb.Call_JarSetCookies:
		return m.JarSetCookies.GetJar(), nil
	case *pb.Call_JarClearExpired:
		return m.JarClearExpired.GetJar(), nil
	case *pb.Call_JarClearSession:
		return m.JarClearSession.GetJar(), nil
	case *pb.Call_JarString:
		return m.JarString.GetJar(), nil
	case *pb.Call_JarClone:
		return m.JarClone.GetJar(), nil
	default:
		return 0, convert.Invalid("%T is not a call here", m)
	}
}

func cookieList(cs []*cookies.Cookie) *pb.Reply {
	return &pb.Reply{
		Result: &pb.Reply_Cookies{
			Cookies: convert.Cookies(cs),
		},
	}
}

func count(n int) *pb.Reply {
	return &pb.Reply{
		Result: &pb.Reply_Count{
			Count: int64(n),
		},
	}
}

// readAll reads the stream's DATA with read, then what read left of it
// up to the SDK's END, so the call ends without a RESET.
func readAll(st *wire.Stream, read func(io.Reader) error) error {
	if err := read(st); err != nil {
		return err
	}

	_, err := io.Copy(io.Discard, st)

	return err
}
