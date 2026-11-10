package proxy

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"net/netip"
	"strconv"
)

// SOCKS4 values (SOCKS4 and the SOCKS4A extension).
const (
	socks4Version = 4
	socks4Granted = 0x5a
)

// SOCKS4 sends req, a SOCKS4Request, to a SOCKS4 proxy over conn and reads
// its answer: nil once the proxy grants the connection.
func SOCKS4(ctx context.Context, conn net.Conn, req []byte) error {
	defer watch(ctx, conn)()

	if _, err := conn.Write(req); err != nil {
		return contextError(ctx, err)
	}

	// VN (0), CD, DSTPORT and DSTIP, which a CONNECT reply leaves unused.
	reply := make([]byte, 8)
	if _, err := io.ReadFull(conn, reply); err != nil {
		return contextError(ctx, fmt.Errorf("SOCKS4 reply: %w", err))
	}

	switch {
	case reply[0] != 0:
		return fmt.Errorf("SOCKS4 proxy answered version %d", reply[0])
	case reply[1] != socks4Granted:
		reason, ok := socks4Replies[reply[1]]
		if !ok {
			reason = fmt.Sprintf("reply %#02x", reply[1])
		}

		return fmt.Errorf("SOCKS4 proxy refused the connection: %s", reason)
	}

	return nil
}

// SOCKS4Request is the CONNECT request for target ("host:port") with
// userID in USERID: VN, CD, DSTPORT, DSTIP, USERID and NUL. An IPv4
// address goes out as DSTIP; a host name as SOCKS4A, DSTIP hostnameIP
// (0.0.0.x, the browser's) and the name and NUL after USERID, for the
// proxy to resolve. SOCKS4 carries no IPv6 address, and NUL ends its
// fields: either is an error, found before anything is dialled.
func SOCKS4Request(target, userID string, hostnameIP [4]byte) ([]byte, error) {
	host, portStr, err := net.SplitHostPort(target)
	if err != nil {
		return nil, err
	}

	port, err := strconv.ParseUint(portStr, 10, 16)
	if err != nil {
		return nil, fmt.Errorf("SOCKS4 target port %q: %w", portStr, err)
	}

	if err := noNUL(userID, "user ID"); err != nil {
		return nil, err
	}

	req := binary.BigEndian.AppendUint16([]byte{socks4Version, socksConnect}, uint16(port))

	addr, err := netip.ParseAddr(host)

	switch {
	case err != nil:
		if err := noNUL(host, "host name"); err != nil {
			return nil, err
		}

		if hostnameIP[0] != 0 || hostnameIP[1] != 0 || hostnameIP[2] != 0 || hostnameIP[3] == 0 {
			return nil, fmt.Errorf("SOCKS4A address %v is not 0.0.0.x with x not 0", netip.AddrFrom4(hostnameIP))
		}

		req = append(req, hostnameIP[:]...)
		req = append(append(req, userID...), 0)

		return append(append(req, host...), 0), nil

	case !addr.Unmap().Is4():
		return nil, fmt.Errorf("SOCKS4 carries IPv4 addresses only, not %s", host)
	}

	ip := addr.Unmap().As4()

	req = append(req, ip[:]...)

	return append(append(req, userID...), 0), nil
}

// noNUL refuses s, a NUL-terminated field, if it holds a NUL.
func noNUL(s, what string) error {
	for i := range len(s) {
		if s[i] == 0 {
			return errors.New("SOCKS4 " + what + " contains a NUL byte")
		}
	}

	return nil
}

var socks4Replies = map[byte]string{
	0x5b: "rejected or failed",
	0x5c: "no identd on the client",
	0x5d: "identd could not confirm the user ID",
}
