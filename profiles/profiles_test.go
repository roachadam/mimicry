package profiles

import (
	"io/fs"
	"slices"
	"strings"
	"testing"
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
