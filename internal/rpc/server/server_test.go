package server

import (
	"bytes"
	"context"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"runtime"
	"runtime/debug"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/durationpb"

	"github.com/roachadam/mimicry/internal/rpc/client"
	"github.com/roachadam/mimicry/internal/rpc/pb"
	"github.com/roachadam/mimicry/internal/rpc/wire"
	"github.com/roachadam/mimicry/internal/ws"
	"github.com/roachadam/mimicry/profiles"
)

// serve runs Serve on one end of a pipe and returns the other, and a
// channel with Serve's result.
func serve(t *testing.T) (net.Conn, <-chan error) {
	t.Helper()

	core, sdk := net.Pipe()
	done := make(chan error, 1)

	go func() {
		done <- Serve(context.Background(), core, Config{
			Version: "test",
		})
	}()

	return sdk, done
}

// connect serves a connection and returns its client, closed (and Serve
// waited for) when the test ends.
func connect(t *testing.T, cfg client.Config) *client.Client {
	t.Helper()

	sdk, done := serve(t)

	c, err := client.New(t.Context(), sdk, cfg)
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() {
		c.Close()

		if err := <-done; err != nil {
			t.Errorf("Serve: %v", err)
		}
	})

	return c
}

// tlsServer serves h over TLS (HTTP/2 and HTTP/1.1) and returns its URL
// and its certificate as PEM, for Config.root_cas.
func tlsServer(t *testing.T, h http.Handler, connState func(net.Conn, http.ConnState)) (*httptest.Server, []byte) {
	t.Helper()

	srv := httptest.NewUnstartedServer(h)

	srv.EnableHTTP2 = true
	srv.Config.ConnState = connState
	srv.StartTLS()
	t.Cleanup(srv.Close)

	return srv, pem.EncodeToMemory(&pem.Block{
		Type:  "CERTIFICATE",
		Bytes: srv.Certificate().Raw,
	})
}

// newClient makes a client of the newest Chrome trusting roots, with cfg
// changed by edit when not nil.
func newClient(t *testing.T, c *client.Client, roots []byte, edit func(*pb.Config)) *pb.NewClientReply {
	t.Helper()

	cfg := &pb.Config{
		Profile: &pb.ProfileRef{
			Profile: &pb.ProfileRef_Name{
				Name: profiles.ChromeMacOS().Name(),
			},
		},
		RootCas:      roots,
		DisableHttp3: true,
	}
	if edit != nil {
		edit(cfg)
	}

	r, err := c.Call(t.Context(), &pb.Call{
		Method: &pb.Call_NewClient{
			NewClient: &pb.NewClient{
				Config: cfg,
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	return r.GetNewClient()
}

// codeOf is err's code, or a note that it is not the core's.
func codeOf(err error) pb.Code {
	if e, ok := errors.AsType[*client.Error](err); ok {
		return e.Proto.GetCode()
	}

	return -1
}

// VerifyConnection is a reverse call the request waits for: the SDK's
// acceptance lets the request through, its error fails the request with
// that error (CODE_VERIFY, its message), and an SDK that never answers
// holds the request no longer than the request's own deadline, after
// which the core gives the call up (the SDK's side of it ends).
func TestVerifyConnection(t *testing.T) {
	srv, roots := tlsServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "ok")
	}), nil)

	for _, tc := range []struct {
		name    string
		answer  func(ctx context.Context) *pb.Error
		timeout time.Duration
		code    pb.Code
		message string
	}{
		{
			name: "accepted",
			answer: func(context.Context) *pb.Error {
				return nil
			},
			code: -1,
		},
		{
			name: "rejected",
			answer: func(context.Context) *pb.Error {
				return &pb.Error{
					Message: "the host refuses this certificate",
				}
			},
			code:    pb.Code_CODE_VERIFY,
			message: "the host refuses this certificate",
		},
		{
			name: "never answered",
			answer: func(ctx context.Context) *pb.Error {
				<-ctx.Done()

				return nil
			},
			timeout: 300 * time.Millisecond,
			code:    pb.Code_CODE_DEADLINE,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			asked := make(chan string, 4)
			gaveUp := make(chan struct{}, 4)
			c := connect(t, client.Config{
				Verify: func(ctx context.Context, v *pb.VerifyConnection) *pb.Error {
					asked <- v.GetState().GetServerName()

					e := tc.answer(ctx)
					if ctx.Err() != nil {
						gaveUp <- struct{}{}
					}

					return e
				},
			})
			nc := newClient(t, c, roots, func(cfg *pb.Config) {
				cfg.VerifyConnection = true
			})

			req := &pb.Request{
				Url: srv.URL,
			}
			if tc.timeout > 0 {
				req.Timeout = durationpb.New(tc.timeout)
			}

			start := time.Now()
			resp, body, err := c.Do(t.Context(), nc.GetClient(), req, nil)
			took := time.Since(start)

			if got := codeOf(err); got != tc.code {
				t.Fatalf("error %v, code %v; want %v", err, got, tc.code)
			}

			if err == nil && (resp.GetStatusCode() != http.StatusOK || string(body) != "ok") {
				t.Errorf("response %d %q", resp.GetStatusCode(), body)
			}

			if !strings.Contains(fmt.Sprint(err), tc.message) {
				t.Errorf("error %q, want it to hold %q", err, tc.message)
			}

			select {
			case name := <-asked:
				if name != "127.0.0.1" {
					t.Errorf("asked about %q", name)
				}

			default:
				t.Error("VerifyConnection never asked")
			}

			if tc.timeout > 0 {
				if took > tc.timeout+2*time.Second {
					t.Errorf("the request took %v, its deadline %v", took, tc.timeout)
				}

				select {
				case <-gaveUp:
				case <-time.After(5 * time.Second):
					t.Error("the core never gave the unanswered call up")
				}
			}
		})
	}
}

// Disconnecting while a request's body streams, another request waits
// on the server, a WebSocket is open and another client holds an idle
// connection frees everything the connection owned: Serve returns, the
// server sees both requests canceled and every connection closed (the
// WebSocket's and the idle one too), and no goroutine of mimicry or of
// the core is left.
func TestDisconnectFreesEverything(t *testing.T) {
	// Without the collector, a Transport left unreachable keeps its
	// connections (it closes them when collected): only freeing counts.
	defer debug.SetGCPercent(debug.SetGCPercent(-1))

	before := goroutines()

	var (
		mu     sync.Mutex
		opened int
		closed int
	)

	canceled := make(chan string, 4)
	streaming := make(chan struct{}, 1)
	waiting := make(chan struct{}, 1)
	socket := make(chan struct{}, 1)
	socketClosed := make(chan struct{})

	srv, roots := tlsServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/stream":
			for i := 0; r.Context().Err() == nil; i++ {
				_, _ = w.Write(bytes.Repeat([]byte("x"), 1<<10))
				_ = http.NewResponseController(w).Flush()

				if i == 0 {
					streaming <- struct{}{}
				}

				time.Sleep(time.Millisecond)
			}

			canceled <- r.URL.Path

		case "/ok":
			_, _ = io.WriteString(w, "ok")
		case "/wait":
			waiting <- struct{}{}

			<-r.Context().Done()

			canceled <- r.URL.Path

		case "/ws":
			conn, brw, err := http.NewResponseController(w).Hijack()
			if err != nil {
				return
			}

			_, _ = fmt.Fprintf(brw, "HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Accept: %s\r\n\r\n", ws.AcceptKey(r.Header.Get("Sec-WebSocket-Key")))
			_ = brw.Flush()

			socket <- struct{}{}

			// The socket's close frame, answered with the server's.
			_, _ = brw.Read(make([]byte, 512))
			_, _ = conn.Write([]byte{0x88, 0x02, 0x03, 0xe8})
			_ = conn.Close()

			close(socketClosed)
		}
	}), func(_ net.Conn, s http.ConnState) {
		mu.Lock()
		defer mu.Unlock()

		switch s {
		case http.StateNew:
			opened++
		case http.StateClosed, http.StateHijacked:
			closed++
		case http.StateActive, http.StateIdle:
		}
	})

	sdk, done := serve(t)

	c, err := client.New(t.Context(), sdk, client.Config{})
	if err != nil {
		t.Fatal(err)
	}

	nc := newClient(t, c, roots, nil)

	// Its connection stays idle: only freeing the client closes it.
	if _, _, err := c.Do(t.Context(), newClient(t, c, roots, nil).GetClient(), &pb.Request{
		Url: srv.URL + "/ok",
	}, nil); err != nil {
		t.Fatal(err)
	}

	for _, call := range []*pb.Call{
		{
			Method: &pb.Call_NewTicketCache{
				NewTicketCache: &pb.NewTicketCache{},
			},
		},
		{
			Method: &pb.Call_NewJar{
				NewJar: &pb.NewJar{
					OnSetCookie: true,
				},
			},
		},
	} {
		if _, err := c.Call(t.Context(), call); err != nil {
			t.Fatal(err)
		}
	}

	if _, _, err := c.DialWebSocket(t.Context(), &pb.DialWebSocket{
		Transport: nc.GetClient(),
		Url:       "wss" + strings.TrimPrefix(srv.URL, "https") + "/ws",
	}); err != nil {
		t.Fatal(err)
	}

	<-socket

	stream, err := c.Open(t.Context(), &pb.Call{
		Method: &pb.Call_Do{
			Do: &pb.Do{
				Client: nc.GetClient(),
				Request: &pb.Request{
					Url: srv.URL + "/stream",
				},
			},
		},
	}, false)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := stream.Reply(); err != nil {
		t.Fatal(err)
	}

	<-streaming

	go func() {
		_, _, _ = c.Do(context.Background(), nc.GetClient(), &pb.Request{
			Url: srv.URL + "/wait",
		}, nil)
	}()

	<-waiting

	c.Close()

	select {
	case err := <-done:
		if err != nil {
			t.Errorf("Serve: %v", err)
		}

	case <-time.After(15 * time.Second):
		t.Fatal("Serve did not return after the disconnect")
	}

	var paths []string
	for range 2 {
		select {
		case p := <-canceled:
			paths = append(paths, p)
		case <-time.After(5 * time.Second):
			t.Fatalf("requests canceled: %v; want /stream and /wait", paths)
		}
	}

	select {
	case <-socketClosed:
	case <-time.After(5 * time.Second):
		t.Error("the WebSocket's connection stayed open")
	}

	deadline := time.Now().Add(5 * time.Second)
	for {
		mu.Lock()

		o, cl := opened, closed
		mu.Unlock()

		var left []string

		for id, g := range goroutines() {
			if _, ok := before[id]; !ok {
				left = append(left, g)
			}
		}

		if o == cl && len(left) == 0 {
			break
		}

		if time.Now().After(deadline) {
			t.Fatalf("%d of %d connections open; goroutines left:\n%s", o-cl, o, strings.Join(left, "\n\n"))
		}

		time.Sleep(20 * time.Millisecond)
	}
}

// A RESET of a WebSocket's stream closes the socket, before the SDK has
// ended its side (its messages) and after: the server gets the socket's
// close frame at once, though nothing else would end the socket.
func TestWebSocketReset(t *testing.T) {
	for _, tc := range []struct {
		name string
		end  bool
	}{
		{
			name: "before the SDK's END",
		},
		{
			name: "after the SDK's END",
			end:  true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			socket := make(chan struct{}, 1)
			closed := make(chan struct{})

			srv, roots := tlsServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				conn, brw, err := http.NewResponseController(w).Hijack()
				if err != nil {
					return
				}

				_, _ = fmt.Fprintf(brw, "HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Accept: %s\r\n\r\n", ws.AcceptKey(r.Header.Get("Sec-WebSocket-Key")))
				_ = brw.Flush()

				socket <- struct{}{}

				// The socket's close frame, answered with the server's.
				_, _ = brw.Read(make([]byte, 512))
				_, _ = conn.Write([]byte{0x88, 0x02, 0x03, 0xe8})
				_ = conn.Close()

				close(closed)
			}), nil)

			c := connect(t, client.Config{})

			sock, _, err := c.DialWebSocket(t.Context(), &pb.DialWebSocket{
				Transport: newClient(t, c, roots, nil).GetClient(),
				Url:       "wss" + strings.TrimPrefix(srv.URL, "https") + "/",
			})
			if err != nil {
				t.Fatal(err)
			}

			<-socket

			if tc.end {
				if err := sock.CloseWrite(); err != nil {
					t.Fatal(err)
				}

				// Until the core has read the END, the RESET would
				// fail its read of the SDK's messages instead.
				deadline := time.Now().Add(5 * time.Second)
				for !endRead() {
					if time.Now().After(deadline) {
						t.Fatal("the core did not read the SDK's END")
					}

					time.Sleep(time.Millisecond)
				}
			}

			sock.Abort()

			select {
			case <-closed:
			case <-time.After(5 * time.Second):
				t.Error("the WebSocket stayed open after its stream's RESET")
			}
		})
	}
}

// A panic in the core's code, on every kind of goroutine it runs its code
// on, fails only what it was doing, with CODE_INTERNAL and a fixed
// message, and never the process: nothing of the panic's value (which may
// hold a call's data) reaches the SDK or stderr. A panic in Hello ends
// the connection as a refused Hello does; anywhere else the connection
// goes on.
func TestPanicContained(t *testing.T) {
	const callData = "the call's own data"

	closed := make(chan struct{}, 1)

	srv, roots := tlsServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/ws" {
			_, _ = io.WriteString(w, "ok")

			return
		}

		conn, brw, err := http.NewResponseController(w).Hijack()
		if err != nil {
			return
		}

		_, _ = fmt.Fprintf(brw, "HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Accept: %s\r\n\r\n", ws.AcceptKey(r.Header.Get("Sec-WebSocket-Key")))
		_ = brw.Flush()

		// The socket's close frame, answered with the server's; a
		// socket left open gives up after 5 s.
		_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
		if _, err := brw.Read(make([]byte, 512)); err == nil {
			_, _ = conn.Write([]byte{0x88, 0x02, 0x03, 0xe8})

			closed <- struct{}{}
		}

		_ = conn.Close()
	}), nil)

	wsURL := "wss" + strings.TrimPrefix(srv.URL, "https") + "/ws"

	// internal checks err is the core's errInternal, the call's data nowhere.
	internal := func(t *testing.T, err error) {
		t.Helper()

		if code := codeOf(err); code != pb.Code_CODE_INTERNAL || !strings.Contains(err.Error(), errInternal.Error()) || strings.Contains(err.Error(), callData) {
			t.Errorf("error %v (code %v), want CODE_INTERNAL %q", err, code, errInternal)
		}
	}

	// alive checks the connection still answers calls.
	alive := func(t *testing.T, c *client.Client) {
		t.Helper()

		if _, err := c.Call(t.Context(), &pb.Call{
			Method: &pb.Call_ListProfiles{
				ListProfiles: &pb.ListProfiles{},
			},
		}); err != nil {
			t.Errorf("a call after the panic: %v", err)
		}
	}

	// socketClosed waits for the test server to see the socket closed.
	socketClosed := func(t *testing.T) {
		t.Helper()

		select {
		case <-closed:
		case <-time.After(5 * time.Second):
			t.Error("the WebSocket stayed open")
		}
	}

	for _, tc := range []struct {
		at  string
		run func(t *testing.T, arm func() <-chan struct{})
	}{
		{
			at: "hello",
			run: func(t *testing.T, arm func() <-chan struct{}) {
				t.Helper()

				arm()

				sdk, done := serve(t)

				wc, err := wire.NewConn(sdk, wire.Config{
					Role: wire.RoleSDK,
				})
				if err != nil {
					t.Fatal(err)
				}

				defer func() {
					_ = wc.Close()
				}()

				r := call(t, wc, &pb.Call{
					Method: &pb.Call_Hello{
						Hello: &pb.Hello{
							ProtocolMajor: pb.ProtocolMajor,
						},
					},
				})
				if e := r.GetError(); e.GetCode() != pb.Code_CODE_INTERNAL || e.GetMessage() != errInternal.Error() {
					t.Errorf("reply %v, want CODE_INTERNAL %q", r, errInternal)
				}

				if err := <-done; !errors.Is(err, errInternal) {
					t.Errorf("Serve: %v, want errInternal", err)
				}
			},
		},
		{
			at: "call",
			run: func(t *testing.T, arm func() <-chan struct{}) {
				t.Helper()

				c := connect(t, client.Config{})

				arm()

				_, err := c.Call(t.Context(), &pb.Call{
					Method: &pb.Call_ListProfiles{
						ListProfiles: &pb.ListProfiles{},
					},
				})
				internal(t, err)
				alive(t, c)
			},
		},
		{
			at: "response body",
			run: func(t *testing.T, arm func() <-chan struct{}) {
				t.Helper()

				c := connect(t, client.Config{})
				nc := newClient(t, c, roots, nil)

				arm()

				_, _, err := c.Do(t.Context(), nc.GetClient(), &pb.Request{
					Url: srv.URL,
				}, nil)
				internal(t, err)
				alive(t, c)
			},
		},
		{
			at: "verify",
			run: func(t *testing.T, arm func() <-chan struct{}) {
				t.Helper()

				c := connect(t, client.Config{
					Verify: func(context.Context, *pb.VerifyConnection) *pb.Error {
						return nil
					},
				})
				nc := newClient(t, c, roots, func(cfg *pb.Config) {
					cfg.VerifyConnection = true
				})

				arm()

				req := &pb.Request{
					Url: srv.URL,
				}

				_, _, err := c.Do(t.Context(), nc.GetClient(), req, nil)
				if err == nil || !strings.Contains(err.Error(), errInternal.Error()) || strings.Contains(err.Error(), callData) {
					t.Errorf("error %v, want one holding %q", err, errInternal)
				}

				if _, _, err := c.Do(t.Context(), nc.GetClient(), req, nil); err != nil {
					t.Errorf("a request after the panic: %v", err)
				}
			},
		},
		{
			at: "websocket read",
			run: func(t *testing.T, arm func() <-chan struct{}) {
				t.Helper()

				c := connect(t, client.Config{})
				nc := newClient(t, c, roots, nil)

				arm()

				sock, _, err := c.DialWebSocket(t.Context(), &pb.DialWebSocket{
					Transport: nc.GetClient(),
					Url:       wsURL,
				})
				if err != nil {
					t.Fatal(err)
				}

				_, _, err = sock.ReadMessage()
				internal(t, err)
				socketClosed(t)
				alive(t, c)
			},
		},
		{
			at: "websocket write",
			run: func(t *testing.T, arm func() <-chan struct{}) {
				t.Helper()

				c := connect(t, client.Config{})
				nc := newClient(t, c, roots, nil)

				arm()

				sock, _, err := c.DialWebSocket(t.Context(), &pb.DialWebSocket{
					Transport: nc.GetClient(),
					Url:       wsURL,
				})
				if err != nil {
					t.Fatal(err)
				}

				_, _, err = sock.ReadMessage()
				internal(t, err)
				socketClosed(t)
				alive(t, c)
			},
		},
		{
			at: "websocket close",
			run: func(t *testing.T, arm func() <-chan struct{}) {
				t.Helper()

				c := connect(t, client.Config{})
				nc := newClient(t, c, roots, nil)

				sock, _, err := c.DialWebSocket(t.Context(), &pb.DialWebSocket{
					Transport: nc.GetClient(),
					Url:       wsURL,
				})
				if err != nil {
					t.Fatal(err)
				}

				fired := arm()

				sock.Abort()

				// The close panics on its own goroutine (the stream's
				// AfterFunc), before its close frame: the socket stays
				// open, the core runs on.
				select {
				case <-fired:
				case <-time.After(5 * time.Second):
					t.Fatal("the socket's close never ran")
				}

				alive(t, c)
			},
		},
		{
			at: "free",
			run: func(t *testing.T, arm func() <-chan struct{}) {
				t.Helper()

				sdk, done := serve(t)

				c, err := client.New(t.Context(), sdk, client.Config{})
				if err != nil {
					t.Fatal(err)
				}

				newClient(t, c, roots, nil)
				arm()
				c.Close()

				if err := <-done; err != nil {
					t.Errorf("Serve: %v", err)
				}
			},
		},
	} {
		t.Run(tc.at, func(t *testing.T) {
			var armed atomic.Bool

			fired := make(chan struct{})
			hook := func(at string) {
				if at == tc.at && armed.CompareAndSwap(true, false) {
					close(fired)
					panic(callData)
				}
			}

			panicHook.Store(&hook)
			t.Cleanup(func() {
				panicHook.Store(nil)
			})

			tc.run(t, func() <-chan struct{} {
				armed.Store(true)

				return fired
			})

			select {
			case <-fired:
			default:
				t.Errorf("nothing panicked at %q", tc.at)
			}
		})
	}
}

// endRead reports the core's WebSocket call having read the SDK's END: it
// waits only for its reading of the socket.
func endRead() bool {
	for _, g := range goroutines() {
		if strings.Contains(g, "server.(*session).dialWebSocket") && strings.Contains(g, "sync.(*WaitGroup).Wait") {
			return true
		}
	}

	return false
}

// goroutines returns the stacks of the goroutines running mimicry's code
// (the core's, the client's, the library's) by goroutine id, each test's
// own and the test server's left out.
func goroutines() map[string]string {
	buf := make([]byte, 1<<20)

	buf = buf[:runtime.Stack(buf, true)]

	out := map[string]string{}

	for g := range strings.SplitSeq(string(buf), "\n\n") {
		if !strings.Contains(g, "github.com/roachadam/mimicry") ||
			strings.Contains(g, "testing.tRunner") || strings.Contains(g, "net/http.(*conn).serve") {
			continue
		}

		id, _, _ := strings.Cut(g, " [")

		out[id] = g
	}

	return out
}

// Hello is the first call, and its major version must be the core's: a
// mismatch, or another call first, is refused with both versions named
// and ends the connection. A call the core does not know, after Hello,
// is refused alone.
func TestHello(t *testing.T) {
	for _, tc := range []struct {
		name    string
		first   *pb.Call
		code    pb.Code
		message string
		ends    bool
	}{
		{
			name: "another major version",
			first: &pb.Call{
				Method: &pb.Call_Hello{
					Hello: &pb.Hello{
						ProtocolMajor: pb.ProtocolMajor + 1,
						SdkVersion:    "sdk-9",
					},
				},
			},
			code:    pb.Code_CODE_INVALID,
			message: fmt.Sprintf("the SDK (sdk-9) speaks protocol %d.0, mimicry-core test speaks %d.%d", pb.ProtocolMajor+1, pb.ProtocolMajor, pb.ProtocolMinor),
			ends:    true,
		},
		{
			name: "another call first",
			first: &pb.Call{
				Method: &pb.Call_ListProfiles{
					ListProfiles: &pb.ListProfiles{},
				},
			},
			code:    pb.Code_CODE_INVALID,
			message: "the first call must be Hello",
			ends:    true,
		},
		{
			name: "an unknown call after Hello",
			first: &pb.Call{
				Method: &pb.Call_Hello{
					Hello: &pb.Hello{
						ProtocolMajor: pb.ProtocolMajor,
						ProtocolMinor: pb.ProtocolMinor + 1,
					},
				},
			},
			code: -1,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sdk, done := serve(t)

			wc, err := wire.NewConn(sdk, wire.Config{
				Role: wire.RoleSDK,
			})
			if err != nil {
				t.Fatal(err)
			}

			defer func() {
				_ = wc.Close()
			}()

			r := call(t, wc, tc.first)
			if got := r.GetError().GetCode(); r.GetError() == nil && tc.code != -1 || r.GetError() != nil && got != tc.code {
				t.Fatalf("reply %v; want code %v", r, tc.code)
			}

			if !strings.Contains(r.GetError().GetMessage(), tc.message) {
				t.Errorf("error %q, want it to hold %q", r.GetError().GetMessage(), tc.message)
			}

			if tc.ends {
				if err := <-done; err == nil {
					t.Error("Serve returned nil")
				}

				return
			}

			if r.GetHello().GetProtocolMajor() != pb.ProtocolMajor || r.GetHello().GetCoreVersion() != "test" {
				t.Errorf("HelloReply %v", r.GetHello())
			}

			if r := call(t, wc, &pb.Call{}); r.GetError().GetCode() != pb.Code_CODE_INVALID {
				t.Errorf("an unknown call: %v", r)
			}

			_ = wc.Close()

			if err := <-done; err != nil {
				t.Errorf("Serve: %v", err)
			}
		})
	}
}

// call makes c on wc and returns the reply.
func call(t *testing.T, wc *wire.Conn, c *pb.Call) *pb.Reply {
	t.Helper()

	b, err := proto.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}

	st, err := wc.Open(t.Context(), b, true)
	if err != nil {
		t.Fatal(err)
	}

	payload, err := st.Reply()
	if err != nil {
		t.Fatal(err)
	}

	r := &pb.Reply{}
	if err := proto.Unmarshal(payload, r); err != nil {
		t.Fatal(err)
	}

	return r
}
