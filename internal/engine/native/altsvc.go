package native

import (
	"net"
	"strconv"
	"strings"
	"time"
)

// altSvcMaxAge is an Alt-Svc entry's lifetime without ma (RFC 7838,
// 3.1).
const altSvcMaxAge = 24 * time.Hour

// altSvc is an origin's HTTP/3 alternative: the same host, at port.
type altSvc struct {
	port    uint16
	expires time.Time
}

// parseAltSvc returns the first same-host h3 alternative in Alt-Svc values
// (RFC 7838) and whether the value is "clear".
func parseAltSvc(values []string, host string, now time.Time) (alt altSvc, found, cleared bool) {
	for _, v := range values {
		for _, entry := range splitQuoted(v, ',') {
			entry = strings.TrimSpace(entry)
			if entry == "clear" {
				return altSvc{}, false, true
			}

			params := splitQuoted(entry, ';')
			proto, authority, ok := strings.Cut(strings.TrimSpace(params[0]), "=")

			if !ok || strings.TrimSpace(proto) != "h3" {
				continue
			}

			authority = strings.Trim(strings.TrimSpace(authority), `"`)

			h, p, err := net.SplitHostPort(authority)

			if err != nil || (h != "" && !strings.EqualFold(h, host)) {
				continue
			}

			port, err := strconv.ParseUint(p, 10, 16)
			if err != nil || port == 0 {
				continue
			}

			maxAge := altSvcMaxAge
			for _, param := range params[1:] {
				name, value, _ := strings.Cut(strings.TrimSpace(param), "=")
				if strings.TrimSpace(name) != "ma" {
					continue
				}

				if s, err := strconv.ParseUint(strings.Trim(strings.TrimSpace(value), `"`), 10, 32); err == nil {
					maxAge = time.Duration(s) * time.Second
				}
			}

			return altSvc{
				port:    uint16(port),
				expires: now.Add(maxAge),
			}, true, false
		}
	}

	return altSvc{}, false, false
}

// splitQuoted splits s at each sep outside double quotes.
func splitQuoted(s string, sep byte) []string {
	var out []string

	start, quoted := 0, false

	for i := 0; i < len(s); i++ {
		switch {
		case s[i] == '"':
			quoted = !quoted
		case s[i] == '\\' && quoted:
			// The escaped byte is skipped.
			i++
		case s[i] == sep && !quoted:
			out = append(out, s[start:i])
			start = i + 1
		}
	}

	return append(out, s[start:])
}
