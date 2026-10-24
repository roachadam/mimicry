package profiles

import (
	"bytes"
	"fmt"
	"io/fs"
	"slices"
	"strings"
	"testing"

	"github.com/roachadam/mimicry/profile"
)

// Every embedded file must parse and be reachable by name; a file the
// generator did not see would ship without an accessor.
func TestEveryEmbeddedProfileParses(t *testing.T) {
	files, err := fs.Glob(data, "data/*.json")
	if err != nil {
		t.Fatal(err)
	}
	if len(files) == 0 {
		t.Fatal("no embedded profiles")
	}
	for _, f := range files {
		name := strings.TrimSuffix(strings.TrimPrefix(f, "data/"), ".json")
		if !slices.Contains(Names(), name) {
			t.Errorf("%s is embedded but has no accessor; run go generate ./profiles", f)
		}
		p, err := ByName(name)
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		if p.Name != name {
			t.Errorf("%s: profile is named %q", f, p.Name)
		}
	}
}

// A caller changing its profile must not change anyone else's.
func TestAccessorsReturnFreshCopies(t *testing.T) {
	a := Chrome155MacOS()
	a.Headers[0].Value = "changed"
	a.TLS.CipherSuites[1] = 0
	b := Chrome155MacOS()
	if b.Headers[0].Value == "changed" || b.TLS.CipherSuites[1] == 0 {
		t.Fatal("a change to one copy reached the next")
	}
}

func TestByNameUnknown(t *testing.T) {
	if _, err := ByName("netscape-4-windows"); err == nil {
		t.Fatal("unknown profile accepted")
	}
}

// The data files are in profile.Encode's format, so a profile edited by a
// tool (kindsgen) and one edited by hand cannot drift apart in layout.
func TestBuiltinsRoundTrip(t *testing.T) {
	for _, name := range Names() {
		t.Run(name, func(t *testing.T) {
			in, err := data.ReadFile("data/" + name + ".json")
			if err != nil {
				t.Fatal(err)
			}
			p, err := profile.Parse(in)
			if err != nil {
				t.Fatal(err)
			}
			out, err := profile.Encode(p)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(in, out) {
				t.Fatalf("re-encoding data/%s.json changes it; rewrite it with profile.Encode", name)
			}
		})
	}
}

// A new version starts as a copy of the previous one, which repeats the user
// agent and languages in every list: each copy must be the navigation's, or
// an edit missed one.
func TestIdentityHeadersMatchNavigation(t *testing.T) {
	for _, name := range Names() {
		t.Run(name, func(t *testing.T) {
			p, err := ByName(name)
			if err != nil {
				t.Fatal(err)
			}
			want := map[string]string{}
			for _, h := range p.Headers {
				if h.Name == "user-agent" || h.Name == "accept-language" {
					want[h.Name] = h.Value
				}
			}
			if want["user-agent"] == "" || want["accept-language"] == "" {
				t.Fatalf("navigation headers lack user-agent or accept-language: %v", want)
			}
			for path, headers := range headerLists(p) {
				for i, h := range headers {
					w, ok := want[strings.ToLower(h.Name)]
					if ok && h.Value != "" && h.Value != w {
						t.Errorf("%s[%d] %s = %q, want the navigation's %q", path, i, h.Name, h.Value, w)
					}
				}
			}
		})
	}
}

// headerLists returns every header list in p by its JSON path.
func headerLists(p *profile.Profile) map[string][]profile.Header {
	lists := map[string][]profile.Header{
		"headers": p.Headers,
	}
	if p.HTTP1 != nil {
		lists["http1.headers"] = p.HTTP1.Headers
	}
	addVariants := func(path string, vs []profile.Variant) {
		for i, v := range vs {
			lists[fmt.Sprintf("%s[%d].headers", path, i)] = v.Headers
			lists[fmt.Sprintf("%s[%d].http1", path, i)] = v.HTTP1
		}
	}
	addVariants("variants", p.Variants)
	for name, k := range p.Kinds {
		lists["kinds."+name+".headers"] = k.Headers
		lists["kinds."+name+".http1"] = k.HTTP1
		addVariants("kinds."+name+".variants", k.Variants)
	}
	if p.Proxy != nil {
		lists["proxy.connect"] = p.Proxy.Connect
		lists["proxy.connect_auth"] = p.Proxy.ConnectAuth
	}
	return lists
}
