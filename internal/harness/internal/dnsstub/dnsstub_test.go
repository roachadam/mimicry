package dnsstub

import (
	"net"
	"net/netip"
	"testing"
	"time"

	"golang.org/x/net/dns/dnsmessage"
)

// TestServeClients: a stub with Clients answers a query from inside the
// prefix and refuses one from outside, under .test and beyond.
func TestServeClients(t *testing.T) {
	answer := netip.MustParseAddr("192.0.2.1")

	for _, tc := range []struct {
		name    string
		clients netip.Prefix
		host    string
		want    dnsmessage.RCode
	}{
		{
			name:    "inside",
			clients: netip.MustParsePrefix("127.0.0.0/8"),
			host:    "www.shop.test.",
			want:    dnsmessage.RCodeSuccess,
		},
		{
			name:    "outside",
			clients: netip.MustParsePrefix("192.0.2.0/24"),
			host:    "www.shop.test.",
			want:    dnsmessage.RCodeRefused,
		},
		{
			name:    "outside, forwarded name",
			clients: netip.MustParsePrefix("192.0.2.0/24"),
			host:    "example.com.",
			want:    dnsmessage.RCodeRefused,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pc, err := new(net.ListenConfig).ListenPacket(t.Context(), "udp4", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}

			go func() {
				_ = Serve(t.Context(), pc, Config{
					Answer: answer,
					// Never reached: an outside query is refused first.
					Upstream: "127.0.0.1:1",
					Clients:  tc.clients,
				})
			}()

			got := query(t, pc.LocalAddr().String(), tc.host)
			if got.RCode != tc.want {
				t.Fatalf("rcode %v, want %v", got.RCode, tc.want)
			}

			if tc.want != dnsmessage.RCodeSuccess {
				return
			}

			if a, ok := oneA(got); !ok || a != answer.As4() {
				t.Errorf("answers %v, want one A %s", got.Answers, answer)
			}
		})
	}
}

// query asks the stub at addr for host's A record.
func query(t *testing.T, addr, host string) dnsmessage.Message {
	t.Helper()

	q := dnsmessage.Message{
		Questions: []dnsmessage.Question{
			{
				Name:  dnsmessage.MustNewName(host),
				Type:  dnsmessage.TypeA,
				Class: dnsmessage.ClassINET,
			},
		},
	}

	q.ID, q.RecursionDesired = 7, true

	packed, err := q.Pack()
	if err != nil {
		t.Fatal(err)
	}

	c, err := new(net.Dialer).DialContext(t.Context(), "udp", addr)
	if err != nil {
		t.Fatal(err)
	}

	defer func() {
		_ = c.Close()
	}()

	_ = c.SetDeadline(time.Now().Add(5 * time.Second))

	if _, err := c.Write(packed); err != nil {
		t.Fatal(err)
	}

	buf := make([]byte, 1500)

	n, err := c.Read(buf)
	if err != nil {
		t.Fatal(err)
	}

	var m dnsmessage.Message
	if err := m.Unpack(buf[:n]); err != nil {
		t.Fatal(err)
	}

	return m
}

// oneA is m's only answer, an A record.
func oneA(m dnsmessage.Message) ([4]byte, bool) {
	if len(m.Answers) != 1 {
		return [4]byte{}, false
	}

	a, ok := m.Answers[0].Body.(*dnsmessage.AResource)
	if !ok {
		return [4]byte{}, false
	}

	return a.A, true
}
