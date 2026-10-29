//go:build darwin || linux

package native

import (
	"net"
	"syscall"
	"testing"

	"github.com/roachadam/mimicry/profiles"
)

// Config.UDPReadBufferSize reaches a QUIC connection's socket: zero leaves
// the OS's default, a size gets what the OS grants a socket asking for it
// (a reference socket asks the same), one above the OS's cap still dials,
// and a negative size is refused.
func TestUDPReadBufferSize(t *testing.T) {
	def := readBuffer(t, listenUDP(t))

	for _, tc := range []struct {
		name string
		size int
		// more: the OS grants more than its default for this size (macOS:
		// up to kern.ipc.maxsockbuf, 8 MB; Linux: up to twice rmem_max,
		// which is at least its default).
		more    bool
		wantErr bool
	}{
		{
			name: "default",
		},
		{
			name: "1MiB",
			size: 1 << 20,
			more: true,
		},
		{
			name: "above the cap",
			size: 1 << 30,
		},
		{
			name:    "negative",
			size:    -1,
			wantErr: true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.wantErr {
				if _, err := New(Config{
					Profile:           profiles.Chrome(),
					UDPReadBufferSize: tc.size,
				}); err == nil {
					t.Fatal("New accepted a negative UDPReadBufferSize")
				}

				return
			}

			e := newTestEngine(t, options{
				udpReadBufferSize: tc.size,
			})

			pc, _, err := e.dialer.ListenUDP(t.Context(), "127.0.0.1", 443)
			if err != nil {
				t.Fatal(err)
			}

			t.Cleanup(func() {
				_ = pc.Close()
			})

			want := def
			if tc.size > 0 {
				ref := listenUDP(t)

				_ = ref.SetReadBuffer(tc.size)
				want = readBuffer(t, ref)
			}

			got := readBuffer(t, pc)
			if got != want {
				t.Fatalf("receive buffer %d, want %d (default %d)", got, want, def)
			}

			if tc.more && got <= def {
				t.Fatalf("receive buffer %d for %d asked, no more than the default %d", got, tc.size, def)
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
