// Command mimicry-core serves mimicry to the Python and Node SDKs
// (decision 0069): each host process starts it as a child and speaks the
// protocol of internal/rpc/wire and proto/ over its stdin and stdout,
// reading stdin and writing stdout: one inherited socket as both (one end
// of a socketpair) or a socket each (Node's 'pipe' stdio) on Linux and
// macOS, pipes on Windows. It exits when the SDK closes its end.
//
// Build it with CGO_ENABLED=0. It writes nothing to stdout but the
// protocol, and to stderr at most one line, why the connection failed:
// never a cookie, header, ticket, proxy credential or key.
//
//	mimicry-core            serve stdin and stdout
//	mimicry-core -version   print the version
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"runtime/debug"

	"github.com/roachadam/mimicry/internal/rpc/server"
)

// version is set at release with -ldflags "-X main.version=v1.2.3";
// otherwise the module's version from the build.
var version string

// socketBuffer is the send and receive buffer asked for the inherited
// socket: macOS's 8 KiB default caps bodies near 1 GB/s (decision 0069).
const socketBuffer = 4 << 20

func main() {
	showVersion := flag.Bool("version", false, "print the version and exit")

	flag.Parse()

	if *showVersion {
		fmt.Println(coreVersion())

		return
	}

	if err := server.Serve(context.Background(), stdio(), server.Config{
		Version: coreVersion(),
	}); err != nil {
		fmt.Fprintf(os.Stderr, "mimicry-core: %v\n", err)
		os.Exit(1)
	}
}

// coreVersion is version, or the build's module version, or "devel".
func coreVersion() string {
	if version != "" {
		return version
	}

	if info, ok := debug.ReadBuildInfo(); ok && info.Main.Version != "" && info.Main.Version != "(devel)" {
		return info.Main.Version
	}

	return "devel"
}

// stdio is the connection to the SDK: it reads stdin and writes stdout,
// each as the socket it is (with socketBuffer), else as the file (pipes).
// Python and Go's client pass one socket as both; Node's 'pipe' stdio
// passes one socket each way, its parent's end of stdin write-only.
func stdio() io.ReadWriteCloser {
	return halves{
		r: endpoint(os.Stdin),
		w: endpoint(os.Stdout),
	}
}

// endpoint is f as a net.Conn when it is a socket, else f.
func endpoint(f *os.File) io.ReadWriteCloser {
	c, err := net.FileConn(f)
	if err != nil {
		return f
	}

	if u, ok := c.(*net.UnixConn); ok {
		_ = u.SetReadBuffer(socketBuffer)
		_ = u.SetWriteBuffer(socketBuffer)
	}

	return c
}

// halves reads r and writes w.
type halves struct {
	r io.ReadCloser
	w io.WriteCloser
}

func (h halves) Read(p []byte) (int, error) {
	return h.r.Read(p)
}

func (h halves) Write(p []byte) (int, error) {
	return h.w.Write(p)
}

func (h halves) Close() error {
	err := h.w.Close()

	_ = h.r.Close()

	return err
}
