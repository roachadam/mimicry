//go:build darwin || linux

package native

import (
	"net"
	"syscall"
	"testing"

	"github.com/roachadam/mimicry/internal/dial"
)

// Config.HTTP3ReadBufferSize reaches a QUIC connection's socket: zero asks
// for dial.DefaultHTTP3ReadBufferSize (Chrome's 1 MB), a size asks for it,
// each getting what the OS grants a socket asking the same (a reference
// socket), more than the OS's default; one above the OS's cap still
// dials, and a negative size leaves the OS's default.
func TestHTTP3ReadBufferSize(t *testing.T) {
	def := readBuffer(t, listenUDP(t))

	for _, tc := range []struct {
		name string
		size int
		// ask is what the socket asks for: zero for nothing (the OS's
		// default).
		ask int
		// more: the OS grants more than its default for this size (macOS:
		// up to kern.ipc.maxsockbuf, 8 MB, over a 768 KB default; Linux:
		// up to twice rmem_max, which is at least its default).
		more bool
	}{
		{
			name: "default",
			ask:  dial.DefaultHTTP3ReadBufferSize,
			more: true,
		},
		{
			name: "2MiB",
			size: 2 << 20,
			ask:  2 << 20,
			more: true,
		},
		{
			name: "above the cap",
			size: 1 << 30,
			ask:  1 << 30,
		},
		{
			name: "OS default",
			size: -1,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newTestEngine(t, options{
				http3ReadBufferSize: tc.size,
			})

			pc, _, err := e.dialer.ListenUDP(t.Context(), "127.0.0.1", 443)
			if err != nil {
				t.Fatal(err)
			}

			t.Cleanup(func() {
				_ = pc.Close()
			})

			want := def
			if tc.ask > 0 {
				ref := listenUDP(t)

				_ = ref.SetReadBuffer(tc.ask)
				want = readBuffer(t, ref)
			}

			got := readBuffer(t, pc)
			if got != want {
				t.Fatalf("receive buffer %d, want %d (default %d)", got, want, def)
			}

			if tc.more && got <= def {
				t.Fatalf("receive buffer %d for %d asked, no more than the default %d", got, tc.ask, def)
			}
		})
	}
}

func listenUDP(t *testing.T) *net.UDPConn {
	t.Helper()

	pc, err := (&net.ListenConfig{}).ListenPacket(t.Context(), "udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() {
		_ = pc.Close()
	})

	uc, ok := pc.(*net.UDPConn)
	if !ok {
		t.Fatalf("%T is not a UDP socket", pc)
	}

	return uc
}

// readBuffer is the socket's SO_RCVBUF.
func readBuffer(t *testing.T, pc net.PacketConn) int {
	t.Helper()

	sc, ok := pc.(syscall.Conn)
	if !ok {
		t.Fatalf("%T has no file descriptor", pc)
	}

	rc, err := sc.SyscallConn()
	if err != nil {
		t.Fatal(err)
	}

	var (
		n    int
		oerr error
	)

	if err := rc.Control(func(fd uintptr) {
		n, oerr = syscall.GetsockoptInt(int(fd), syscall.SOL_SOCKET, syscall.SO_RCVBUF)
	}); err != nil {
		t.Fatal(err)
	}

	if oerr != nil {
		t.Fatal(oerr)
	}

	return n
}
