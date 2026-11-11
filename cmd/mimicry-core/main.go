// Command mimicry-core serves mimicry to the Python and Node SDKs
// (decision 0069): each host process starts it as a child and speaks the
// protocol of internal/rpc/wire and proto/ over its stdin and stdout, an
// inherited socket (one end of a socketpair) on Linux and macOS, two
// anonymous pipes on Windows. It exits when the SDK closes its end.
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

// stdio is the connection to the SDK: the socket stdin is, as one
// net.Conn, or else stdin and stdout as they are (pipes).
func stdio() io.ReadWriteCloser {
	if c, err := net.FileConn(os.Stdin); err == nil {
		if u, ok := c.(*net.UnixConn); ok {
			_ = u.SetReadBuffer(socketBuffer)
			_ = u.SetWriteBuffer(socketBuffer)
		}

		return c
	}

	return pipes{}
}

// pipes is stdin and stdout.
type pipes struct{}

func (pipes) Read(p []byte) (int, error) {
	return os.Stdin.Read(p)
}

func (pipes) Write(p []byte) (int, error) {
	return os.Stdout.Write(p)
}

func (pipes) Close() error {
	_ = os.Stdin.Close()

	return os.Stdout.Close()
}
