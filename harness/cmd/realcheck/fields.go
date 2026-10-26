package main

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// field is one value an echo reported, under a name shared by every
// client's answer from the same endpoint.
type field struct {
	name  string
	value string
}

// fieldsOf extracts the comparable fields of an endpoint's answer. Values
// that differ on every connection (client random, session ID, key shares,
// GREASE values, ECH payloads, connection IDs, the client's address) are
// left out or reduced to their shape; everything else is kept as the echo
// reported it.
func fieldsOf(endpoint string, body json.RawMessage) ([]field, error) {
	var root any
	d := json.NewDecoder(bytes.NewReader(body))
	d.UseNumber()
	if err := d.Decode(&root); err != nil {
		return nil, err
	}
	var fs fieldList
	switch {
	case strings.HasPrefix(endpoint, "peet"):
		fs.peet(root)
	case endpoint == "browserleaks-tls":
		for _, k := range []string{"user_agent", "ja4", "ja4_r", "ja4_o", "ja4_ro", "ja3_hash", "ja3_text", "ja3n_hash", "ja3n_text", "akamai_hash", "akamai_text"} {
			fs.add(k, str(get(root, k)))
		}
	case endpoint == "browserleaks-h2":
		fs.add("akamai_text", str(get(root, "akamai_text")))
		fs.add("akamai_hash", str(get(root, "akamai_hash")))
		frames, _ := get(root, "http2").([]any)
		rawFrames := rawList(body, "http2")
		for i, f := range frames {
			m, _ := f.(map[string]any)
			if m["name"] == "HEADERS" && i < len(rawFrames) {
				fs.headers(headersPrefix(i, frames), orderedPairs(rawObject(rawFrames[i], "headers")))
				delete(m, "headers")
				delete(m, "length")
			}
			fs.add(fmt.Sprintf("frame %d", i), canon(m))
		}
	case strings.HasPrefix(endpoint, "impersonate"):
		fs.impersonate(root)
	default:
		return nil, fmt.Errorf("no fields for endpoint %q", endpoint)
	}
	return fs, nil
}

type fieldList []field

func (fs *fieldList) add(name, value string) {
	*fs = append(*fs, field{
		name:  name,
		value: value,
	})
}

// headers adds a header list: its order, then each header's value.
func (fs *fieldList) headers(prefix string, pairs [][2]string) {
	names := make([]string, len(pairs))
	values := map[string][]string{}
	for i, p := range pairs {
		names[i] = p[0]
		values[p[0]] = append(values[p[0]], p[1])
	}
	fs.add(prefix+"header order", strings.Join(names, ","))
	seen := map[string]bool{}
	for _, n := range names {
		if seen[n] {
			continue
		}
		seen[n] = true
		fs.add(prefix+"header "+n, strings.Join(values[n], " | "))
	}
}

// headersPrefix names the header fields of the HEADERS frame at index i
// of frames: "h2 " for the first, "h2 #n " for later ones (a reused
// connection's earlier requests).
func headersPrefix(i int, frames []any) string {
	n := 0
	for _, f := range frames[:i+1] {
		if m, _ := f.(map[string]any); str(m["name"]) == "HEADERS" || str(m["frame_type"]) == "HEADERS" {
			n++
		}
	}
	if n <= 1 {
		return "h2 "
	}
	return "h2 #" + strconv.Itoa(n) + " "
}

// peet adds tls.peet.ws's /api/all fields.
func (fs *fieldList) peet(root any) {
	fs.add("http_version", str(get(root, "http_version")))
	for _, k := range []string{"tls_version_negotiated", "ja3", "ja3_hash", "ja4", "ja4_r", "peetprint", "peetprint_hash"} {
		fs.add("tls."+k, str(get(root, "tls."+k)))
	}
	fs.add("tls.ja3 (extensions sorted)", sortedJA3(str(get(root, "tls.ja3"))))
	fs.add("tls.ciphers", canon(get(root, "tls.ciphers")))
	exts, _ := get(root, "tls.extensions").([]any)
	fs.extensions(exts)
	for _, k := range []string{"akamai_fingerprint", "akamai_fingerprint_hash"} {
		fs.add("http2."+k, str(get(root, "http2."+k)))
	}
	frames, _ := get(root, "http2.sent_frames").([]any)
	for i, f := range frames {
		m, _ := f.(map[string]any)
		if hs, ok := m["headers"].([]any); ok {
			pairs := make([][2]string, 0, len(hs))
			for _, h := range hs {
				n, v, _ := strings.Cut(str(h), ": ")
				// Pseudo-headers come as ":method: GET".
				if strings.HasPrefix(str(h), ":") {
					n2, v2, _ := strings.Cut(str(h)[1:], ": ")
					n, v = ":"+n2, v2
				}
				pairs = append(pairs, [2]string{n, v})
			}
			fs.headers(headersPrefix(i, frames), pairs)
			delete(m, "headers")
			delete(m, "length")
		}
		fs.add(fmt.Sprintf("frame %d", i), canon(m))
	}
	if h3 := get(root, "http3"); h3 != nil {
		fs.add("http3", canon(h3))
	}
	for _, k := range []string{"p0f", "tcp_syn.window", "tcp_syn.mss", "tcp_syn.window_scale", "tcp_syn.option_order", "tcp_syn.ttl", "tcp_syn.df", "tcp_syn.ecn"} {
		if v := get(root, "tcpip."+k); v != nil {
			fs.add("tcpip."+k, str(v))
		}
	}
}

// impersonate adds fp.impersonate.pro's /api/http2 and /api/http3 fields.
func (fs *fieldList) impersonate(root any) {
	fs.add("protocol", str(get(root, "protocol")))
	for _, sec := range []string{"http2", "http3"} {
		m, _ := get(root, sec).(map[string]any)
		if m == nil {
			continue
		}
		for _, k := range sortedKeys(m) {
			switch k {
			case "headers", "header_order":
				continue
			}
			fs.add(sec+"."+k, canon(m[k]))
		}
		hs, _ := m["headers"].([]any)
		pairs := make([][2]string, 0, len(hs))
		for _, h := range hs {
			hm, _ := h.(map[string]any)
			pairs = append(pairs, [2]string{str(hm["name"]), str(hm["value"])})
		}
		fs.headers(sec+" ", pairs)
	}
	if q, _ := get(root, "quic").(map[string]any); q != nil {
		for _, k := range sortedKeys(q) {
			fs.add("quic."+k, canon(q[k]))
		}
	}
	for _, k := range []string{"ja3.text", "ja3.hash", "ja3n.text", "ja3n.hash", "version", "alpn"} {
		if v := get(root, "tls."+k); v != nil {
			fs.add("tls."+k, str(v))
		}
	}
	fs.add("tls.cipher_suites", canon(get(root, "tls.cipher_suites")))
	exts, _ := get(root, "tls.extensions").([]any)
	fs.extensions(exts)
}

// extensions adds each extension under its name, in name order (so a
// shuffled order compares equal; the order is in JA3), its contents
// reduced to their shape where they are random. ECH (GREASE: random
// config ID, a payload length and cipher suite drawn per connection) is
// split into its parts; QUIC transport parameters into the set and the
// order (shuffled per connection by some browsers).
func (fs *fieldList) extensions(exts []any) {
	type ext struct {
		name  string
		value string
	}
	list := make([]ext, 0, len(exts))
	count := map[string]int{}
	for _, e := range exts {
		m, _ := e.(map[string]any)
		n := greaseText(str(m["name"]))
		switch {
		case echExtension.MatchString(n):
			raw := str(m["data"])
			if d, ok := m["data"].(map[string]any); ok {
				raw = str(d["raw"])
			}
			fs.ech(raw)
			continue
		case n == "quic_transport_parameters":
			fs.transportParameters(m["data"])
			continue
		case randomExtension.MatchString(n):
			m = shapeOf(m).(map[string]any)
		}
		delete(m, "name")
		count[n]++
		if count[n] > 1 {
			n += " #" + strconv.Itoa(count[n])
		}
		list = append(list, ext{
			name:  n,
			value: canon(m),
		})
	}
	slices.SortStableFunc(list, func(a, b ext) int {
		return strings.Compare(a.name, b.name)
	})
	for _, e := range list {
		fs.add("ext "+e.name, e.value)
	}
}

// ech adds an ECH extension's parts from its bytes (hex), an outer
// ClientHello's: cipher suite, enc and payload lengths. The config
// ID is random and left out.
func (fs *fieldList) ech(raw string) {
	b, err := hex.DecodeString(raw)
	if err != nil || len(b) < 10 || b[0] != 0 {
		fs.add("ext ECH", fmt.Sprintf("unparsed (%d bytes)", len(raw)/2))
		return
	}
	enc := int(binary.BigEndian.Uint16(b[6:8]))
	if len(b) < 10+enc {
		fs.add("ext ECH", fmt.Sprintf("unparsed (%d bytes)", len(b)))
		return
	}
	fs.add("ext ECH cipher suite", fmt.Sprintf("0x%04x/0x%04x", binary.BigEndian.Uint16(b[1:3]), binary.BigEndian.Uint16(b[3:5])))
	fs.add("ext ECH enc length", strconv.Itoa(enc))
	fs.add("ext ECH payload length", strconv.Itoa(int(binary.BigEndian.Uint16(b[8+enc:10+enc]))))
}

// transportParameters adds QUIC transport parameters (fp.impersonate.pro
// lists them in the order sent): the set, sorted by name, and the order
// of their IDs.
func (fs *fieldList) transportParameters(v any) {
	params, _ := v.([]any)
	var order []string
	set := make([]string, 0, len(params))
	for _, p := range params {
		m, _ := p.(map[string]any)
		n := str(m["name"])
		if strings.Contains(n, "GREASE") {
			order = append(order, "GREASE")
		} else {
			order = append(order, str(m["id"]))
		}
		if vi, ok := m["value"].(map[string]any); ok {
			// Chrome shuffles its available versions too.
			if av, ok := vi["available_versions"].([]any); ok {
				slices.SortFunc(av, func(a, b any) int {
					return strings.Compare(canon(a), canon(b))
				})
			}
		}
		set = append(set, canon(m))
	}
	slices.Sort(set)
	fs.add("ext quic_transport_parameters", "["+strings.Join(set, ",")+"]")
	fs.add("ext quic_transport_parameters order", strings.Join(order, ","))
}

// echExtension matches the ECH extension's names; randomExtension those
// whose contents are random per connection (keys, padding, PSK
// identities and binders).
var (
	echExtension    = regexp.MustCompile(`(?i)encrypted_?client_?hello`)
	randomExtension = regexp.MustCompile(`(?i)key_share|padding|pre_shared_key`)
)

// hexRun is a long run of hex digits: random bytes.
var hexRun = regexp.MustCompile(`^(0x)?[0-9a-fA-F]{16,}$`)

// shapeOf replaces random bytes in v with their length.
func shapeOf(v any) any {
	switch t := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, x := range t {
			out[k] = shapeOf(x)
		}
		return out
	case []any:
		out := make([]any, len(t))
		for i, x := range t {
			out[i] = shapeOf(x)
		}
		return out
	case string:
		if hexRun.MatchString(t) {
			return fmt.Sprintf("<%d bytes>", len(strings.TrimPrefix(t, "0x"))/2)
		}
	}
	return v
}

// GREASE spellings: TLS's 0x?a?a values (16 bits), QUIC's reserved
// versions 0x?a?a?a?a, and the echoes' names for them.
var (
	greaseHex   = regexp.MustCompile(`(?i)\b0x([0-9a-f])a([0-9a-f])a(([0-9a-f])a([0-9a-f])a)?\b`)
	greaseNamed = regexp.MustCompile(`TLS_GREASE \(0x[0-9a-fA-F]+\)`)
)

func greaseText(s string) string {
	s = greaseNamed.ReplaceAllString(s, "TLS_GREASE")
	return greaseHex.ReplaceAllStringFunc(s, func(m string) string {
		v, err := strconv.ParseUint(m[2:], 16, 32)
		if err != nil {
			return m
		}
		if isGREASE(v) {
			return "GREASE"
		}
		return m
	})
}

// isGREASE reports whether v is a TLS GREASE value (RFC 8701) or a QUIC
// reserved version (RFC 9000 15).
func isGREASE(v uint64) bool {
	if v <= 0xffff {
		return v&0x0f0f == 0x0a0a && v>>12 == (v>>4)&0xf
	}
	return v&0x0f0f0f0f == 0x0a0a0a0a
}

// greaseValues rewrites GREASE in a decoded value: names, and the id or
// value next to a "GREASE" name (random per connection).
func greaseValues(v any) any {
	switch t := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(t))
		named := strings.Contains(str(t["name"]), "GREASE")
		for k, x := range t {
			if named && (k == "id" || k == "value") {
				continue
			}
			nk := greaseText(k)
			out[nk] = greaseValues(x)
		}
		// fp.impersonate.pro: a QUIC connection ID's value is random.
		if n := str(t["name"]); strings.HasSuffix(n, "connection_id") {
			out["value"] = fmt.Sprintf("<%d bytes>", len(strings.TrimPrefix(str(t["value"]), "0x"))/2)
		}
		return out
	case []any:
		out := make([]any, len(t))
		for i, x := range t {
			out[i] = greaseValues(x)
		}
		return out
	case string:
		return greaseText(t)
	case json.Number:
		if n, err := strconv.ParseUint(t.String(), 10, 64); err == nil && n > 0xff && isGREASE(n) {
			return "GREASE"
		}
	}
	return v
}

// canon is v with GREASE rewritten, as compact JSON with sorted keys.
func canon(v any) string {
	if v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return greaseText(s)
	}
	b, err := json.Marshal(greaseValues(v))
	if err != nil {
		return fmt.Sprint(v)
	}
	return string(b)
}

// str is a scalar as text.
func str(v any) string {
	switch t := v.(type) {
	case nil:
		return ""
	case string:
		return t
	case json.Number:
		return t.String()
	case bool:
		return strconv.FormatBool(t)
	}
	return canon(v)
}

// get follows a dotted path of object keys.
func get(v any, path string) any {
	for _, k := range strings.Split(path, ".") {
		m, ok := v.(map[string]any)
		if !ok {
			return nil
		}
		v = m[k]
	}
	return v
}

func sortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}

// sortedJA3 is a JA3 string with its extension list sorted, which a
// browser that shuffles its extensions sends the same on every
// connection.
func sortedJA3(ja3 string) string {
	parts := strings.Split(ja3, ",")
	if len(parts) != 5 {
		return ja3
	}
	exts := strings.Split(parts[2], "-")
	slices.SortFunc(exts, func(a, b string) int {
		x, _ := strconv.Atoi(a)
		y, _ := strconv.Atoi(b)
		return x - y
	})
	parts[2] = strings.Join(exts, "-")
	return strings.Join(parts, ",")
}

// rawList returns the raw elements of the array under key in a JSON
// object.
func rawList(body json.RawMessage, key string) []json.RawMessage {
	var m map[string]json.RawMessage
	if json.Unmarshal(body, &m) != nil {
		return nil
	}
	var list []json.RawMessage
	_ = json.Unmarshal(m[key], &list)
	return list
}

// rawObject returns the raw value under key in a JSON object.
func rawObject(body json.RawMessage, key string) json.RawMessage {
	var m map[string]json.RawMessage
	if json.Unmarshal(body, &m) != nil {
		return nil
	}
	return m[key]
}

// orderedPairs returns a JSON object's string members in their order,
// which a map would lose (browserleaks reports headers as an object).
func orderedPairs(obj json.RawMessage) [][2]string {
	d := json.NewDecoder(bytes.NewReader(obj))
	if t, err := d.Token(); err != nil || t != json.Delim('{') {
		return nil
	}
	var pairs [][2]string
	for d.More() {
		k, err := d.Token()
		if err != nil {
			return pairs
		}
		var v any
		if err := d.Decode(&v); err != nil {
			return pairs
		}
		pairs = append(pairs, [2]string{fmt.Sprint(k), str(v)})
	}
	return pairs
}
