package cookies

import (
	"errors"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/roachadam/mimicry/profile"
)

// Limits of RFC 6265bis.
const (
	maxPairSize      = 4096 // name plus value
	maxAttributeSize = 1024
)

// parsed is a Set-Cookie line before the jar's checks.
type parsed struct {
	name, value string
	// expires is the Max-Age or Expires time, capped at 400 days from
	// now; zero when neither.
	expires time.Time
	// domain is the last Domain attribute without its leading dot; empty
	// when absent or empty (host-only).
	domain string
	// pathAttr is the last Path attribute as sent, valid or not; empty
	// when absent. path is pathAttr when valid (it starts with "/"), else
	// empty: the default path.
	pathAttr    string
	path        string
	secure      bool
	httpOnly    bool
	sameSite    SameSite
	partitioned bool
}

// maxExpiry is how far ahead a cookie may expire (RFC 6265bis 5.6.1,
// 5.6.2): 400 days.
const maxExpiry = 400 * 24 * time.Hour

// parse reads a Set-Cookie line by RFC 6265bis section 5.6, with rules'
// policies for a pair without "=" and one with nothing before it; now is
// the time Max-Age counts from.
func parse(line string, rules *profile.Cookies, now time.Time) (*parsed, error) {
	for i := range len(line) {
		if b := line[i]; (b < 0x20 && b != '\t') || b == 0x7f {
			return nil, errors.New("control character in the line")
		}
	}
	pairText, attrs, _ := strings.Cut(line, ";")
	p := &parsed{}
	if name, value, ok := strings.Cut(pairText, "="); ok {
		p.name, p.value = trimWSP(name), trimWSP(value)
		if p.name == "" && p.value != "" && rules.EmptyName == profile.EmptyNameDrop {
			return nil, errors.New("an empty name before \"=\", which this browser drops")
		}
	} else {
		switch rules.Nameless {
		case profile.NamelessName:
			p.name = trimWSP(pairText)
		case profile.NamelessDrop:
			return nil, errors.New("a pair without \"=\", which this browser drops")
		default:
			p.value = trimWSP(pairText)
		}
	}
	if p.name == "" && p.value == "" {
		return nil, errors.New("empty name and value")
	}
	if len(p.name)+len(p.value) > maxPairSize {
		return nil, errors.New("name and value longer than 4096 bytes")
	}
	var maxAge *time.Time
	for attr := range strings.SplitSeq(attrs, ";") {
		key, val, _ := strings.Cut(attr, "=")
		key, val = trimWSP(key), trimWSP(val)
		if len(val) > maxAttributeSize {
			continue
		}
		switch strings.ToLower(key) {
		case "expires":
			if t, ok := parseDate(val); ok {
				p.expires = t
			}
		case "max-age":
			if t, ok := parseMaxAge(val, now); ok {
				maxAge = &t
			}
		case "domain":
			// The last Domain wins; an empty one makes the cookie host-only.
			p.domain = strings.ToLower(strings.TrimPrefix(val, "."))
		case "path":
			// The last Path wins, even when invalid: then the cookie gets
			// the default path.
			p.pathAttr = val
			p.path = ""
			if strings.HasPrefix(val, "/") {
				p.path = val
			}
		case "secure":
			p.secure = true
		case "httponly":
			p.httpOnly = true
		case "samesite":
			switch strings.ToLower(val) {
			case "none":
				p.sameSite = SameSiteNone
			case "lax":
				p.sameSite = SameSiteLax
			case "strict":
				p.sameSite = SameSiteStrict
			default:
				p.sameSite = SameSiteUnset
			}
		case "partitioned":
			p.partitioned = true
		}
	}
	if maxAge != nil {
		p.expires = *maxAge // Max-Age wins over Expires
	}
	if limit := now.Add(maxExpiry); p.expires.After(limit) {
		p.expires = limit
	}

	return p, nil
}

func trimWSP(s string) string {
	return strings.Trim(s, " \t")
}

// parseMaxAge reads Max-Age: a delta in seconds, "-" and digits or digits
// only, else ignored; zero or below is the earliest time, which expires
// the cookie.
func parseMaxAge(v string, now time.Time) (time.Time, bool) {
	digits := strings.TrimPrefix(v, "-")
	if digits == "" || strings.ContainsFunc(digits, func(r rune) bool {
		return r < '0' || r > '9'
	}) {
		return time.Time{}, false
	}
	if digits != v {
		return time.Unix(0, 0), true
	}
	n, err := strconv.ParseInt(digits, 10, 64)
	if err != nil {
		n = math.MaxInt64 // overflow: far future, capped below
	}
	if n == 0 {
		return time.Unix(0, 0), true
	}

	return now.Add(time.Duration(min(n, int64(maxExpiry/time.Second))) * time.Second), true
}

// parseDate reads a cookie date by RFC 6265bis section 5.1.1.
func parseDate(s string) (time.Time, bool) {
	isDelim := func(r rune) bool {
		return r == 0x09 || (r >= 0x20 && r <= 0x2f) || (r >= 0x3b && r <= 0x40) || (r >= 0x5b && r <= 0x60) || (r >= 0x7b && r <= 0x7e)
	}
	var (
		hour, minute, sec, day, month, year = -1, -1, -1, -1, -1, -1
		foundTime, foundDay, foundMonth     bool
		foundYear                           bool
	)
	digits := func(tok string, lo, hi int) (int, string, bool) {
		n := 0
		for n < len(tok) && tok[n] >= '0' && tok[n] <= '9' {
			n++
		}
		if n < lo || n > hi {
			return 0, "", false
		}
		v, _ := strconv.Atoi(tok[:n])

		return v, tok[n:], true
	}
	for _, tok := range strings.FieldsFunc(s, isDelim) {
		if !foundTime {
			if h, rest, ok := digits(tok, 1, 2); ok && strings.HasPrefix(rest, ":") {
				if m, rest, ok := digits(rest[1:], 1, 2); ok && strings.HasPrefix(rest, ":") {
					if sc, _, ok := digits(rest[1:], 1, 2); ok {
						hour, minute, sec, foundTime = h, m, sc, true
						continue
					}
				}
			}
		}
		if !foundDay {
			if d, _, ok := digits(tok, 1, 2); ok {
				day, foundDay = d, true
				continue
			}
		}
		if !foundMonth && len(tok) >= 3 {
			if m := strings.Index("janfebmaraprmayjunjulaugsepoctnovdec", strings.ToLower(tok[:3])); m >= 0 && m%3 == 0 {
				month, foundMonth = m/3+1, true
				continue
			}
		}
		if !foundYear {
			if y, _, ok := digits(tok, 2, 4); ok {
				year, foundYear = y, true
				continue
			}
		}
	}
	if !foundTime || !foundDay || !foundMonth || !foundYear {
		return time.Time{}, false
	}
	switch {
	case year >= 70 && year <= 99:
		year += 1900
	case year >= 0 && year <= 69:
		year += 2000
	}
	if day < 1 || day > 31 || year < 1601 || hour > 23 || minute > 59 || sec > 59 {
		return time.Time{}, false
	}
	t := time.Date(year, time.Month(month), day, hour, minute, sec, 0, time.UTC)
	if t.Day() != day {
		return time.Time{}, false // 31 February and the like
	}

	return t, true
}
