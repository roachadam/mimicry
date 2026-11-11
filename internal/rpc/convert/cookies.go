package convert

import (
	"net/url"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/roachadam/mimicry/cookies"
	"github.com/roachadam/mimicry/internal/rpc/pb"
)

// Cookie returns c as data.
func Cookie(c *cookies.Cookie) *pb.Cookie {
	return &pb.Cookie{
		Name:        []byte(c.Name),
		Value:       []byte(c.Value),
		Domain:      Text(c.Domain),
		HostOnly:    c.HostOnly,
		Path:        Text(c.Path),
		Expires:     timestamp(c.Expires),
		Secure:      c.Secure,
		HttpOnly:    c.HTTPOnly,
		SameSite:    pb.SameSite(c.SameSite),
		Partitioned: c.Partitioned,
		Partition:   c.Partition,
		Created:     timestamp(c.Created),
		LastAccess:  timestamp(c.LastAccess),
		Raw:         []byte(c.Raw),
	}
}

// Cookies returns cs as data.
func Cookies(cs []*cookies.Cookie) *pb.Cookies {
	out := &pb.Cookies{
		Cookies: make([]*pb.Cookie, 0, len(cs)),
	}
	for _, c := range cs {
		out.Cookies = append(out.Cookies, Cookie(c))
	}

	return out
}

// FromCookie returns c as a cookies.Cookie.
func FromCookie(c *pb.Cookie) cookies.Cookie {
	return cookies.Cookie{
		Name:        string(c.GetName()),
		Value:       string(c.GetValue()),
		Domain:      c.GetDomain(),
		HostOnly:    c.GetHostOnly(),
		Path:        c.GetPath(),
		Expires:     fromTimestamp(c.GetExpires()),
		Secure:      c.GetSecure(),
		HTTPOnly:    c.GetHttpOnly(),
		SameSite:    cookies.SameSite(c.GetSameSite()),
		Partitioned: c.GetPartitioned(),
		Partition:   c.GetPartition(),
		Created:     fromTimestamp(c.GetCreated()),
		LastAccess:  fromTimestamp(c.GetLastAccess()),
		Raw:         string(c.GetRaw()),
	}
}

// CookieRequest returns r as a cookies.Request.
func CookieRequest(r *pb.CookieRequest) (cookies.Request, error) {
	u, err := url.Parse(r.GetUrl())
	if err != nil {
		return cookies.Request{}, Invalid("request.url: %v", urlCause(err))
	}

	page, err := optionalURL("request.page", r.GetPage())
	if err != nil {
		return cookies.Request{}, err
	}

	out := cookies.Request{
		URL:        u,
		Method:     r.GetMethod(),
		Navigation: r.GetNavigation(),
		Page:       page,
		WebSocket:  r.GetWebsocket(),
	}

	for _, raw := range r.GetRedirects() {
		u, err := url.Parse(raw)
		if err != nil {
			return cookies.Request{}, Invalid("request.redirects: %v", urlCause(err))
		}

		out.Redirects = append(out.Redirects, u)
	}

	return out, nil
}

// SetCookie returns jar's event e as data.
func SetCookie(jar uint64, e cookies.Event) *pb.SetCookie {
	out := &pb.SetCookie{
		Jar:      jar,
		Line:     []byte(e.Line),
		Rejected: Text(e.Rejected),
		Deleted:  e.Deleted,
	}

	if e.URL != nil {
		out.Url = e.URL.String()
	}

	if e.Cookie != nil {
		out.Cookie = Cookie(e.Cookie)
	}

	return out
}

// timestamp returns t as data; nil for the zero time.
func timestamp(t time.Time) *timestamppb.Timestamp {
	if t.IsZero() {
		return nil
	}

	return timestamppb.New(t)
}

// fromTimestamp returns ts as a time; the zero time for nil.
func fromTimestamp(ts *timestamppb.Timestamp) time.Time {
	if ts == nil {
		return time.Time{}
	}

	return ts.AsTime()
}
