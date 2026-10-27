// Package testprofiles chooses the profiles the harness's tests check
// (decision 0035) and builds the synthetic profile that proves capture
// inheritance.
//
// By default it picks the newest profile of each browser;
// MIMICRY_PROFILES=all picks every supported one, and a comma-separated list
// picks built-in names or paths to .json profile files.
package testprofiles

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"

	"github.com/roachadam/mimicry/internal/harness/internal/capture"
	"github.com/roachadam/mimicry/profile"
	"github.com/roachadam/mimicry/profiles"
)

// Env is the environment variable that chooses the profiles.
const Env = "MIMICRY_PROFILES"

type selection struct {
	names []string
	// files are the profile files named, by profile name.
	files map[string][]byte
}

var selected = sync.OnceValues(func() (*selection, error) {
	return choose(os.Getenv(Env))
})

func choose(env string) (*selection, error) {
	s := &selection{
		files: map[string][]byte{},
	}
	switch env = strings.TrimSpace(env); env {
	case "":
		for _, p := range []*profile.Profile{profiles.Chrome(), profiles.Firefox(), profiles.Safari()} {
			s.names = append(s.names, p.Name)
		}

		slices.Sort(s.names)

		return s, nil

	case "all":
		s.names = profiles.Supported()
		return s, nil
	}

	for item := range strings.SplitSeq(env, ",") {
		item = strings.TrimSpace(item)
		if !strings.HasSuffix(item, ".json") {
			if _, err := profiles.ByName(item); err != nil {
				return nil, fmt.Errorf("%s: %w", Env, err)
			}

			s.names = append(s.names, item)

			continue
		}

		data, err := os.ReadFile(item)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", Env, err)
		}

		p, err := profile.Parse(data)
		if err != nil {
			return nil, fmt.Errorf("%s: %s: %w", Env, item, err)
		}

		s.names = append(s.names, p.Name)
		s.files[p.Name] = data
	}

	return s, nil
}

// Names lists the profiles to check, as MIMICRY_PROFILES says.
func Names() ([]string, error) {
	s, err := selected()
	if err != nil {
		return nil, err
	}

	return slices.Clone(s.names), nil
}

// Load returns a fresh copy of the profile to check called name, and its
// JSON: a built-in profile's file in profiles/data, or the file
// MIMICRY_PROFILES named.
func Load(name string) (*profile.Profile, []byte, error) {
	s, err := selected()
	if err != nil {
		return nil, nil, err
	}

	data, ok := s.files[name]
	if !ok {
		if data, err = builtinJSON(name); err != nil {
			return nil, nil, err
		}
	}

	p, err := profile.Parse(data)
	if err != nil {
		return nil, nil, fmt.Errorf("%s: %w", name, err)
	}

	return p, data, nil
}

// repoDir returns the repository this package is in.
func repoDir() (string, error) {
	_, here, _, ok := runtime.Caller(0)
	if !ok {
		return "", errors.New("testprofiles: cannot locate the repository")
	}

	return filepath.Join(filepath.Dir(here), "..", "..", "..", ".."), nil
}

// builtinJSON reads a built-in profile's file, profiles/data/<name>.json.
func builtinJSON(name string) ([]byte, error) {
	if _, err := profiles.ByName(name); err != nil {
		return nil, err
	}

	dir, err := repoDir()
	if err != nil {
		return nil, err
	}

	return os.ReadFile(filepath.Join(dir, "profiles", "data", name+".json"))
}

// InheritingName is the synthetic profile Inheriting builds: a version far
// beyond any built-in one, so its captures and gap pins never meet a real
// profile's (chrome-156-macos is one since its refresh).
const InheritingName = "chrome-999-macos"

// Inheriting builds chrome-155-macos as chrome-999-macos, every version
// string set to 999's, inheriting every non-default scenario's captures from
// chrome-155, so its checks run through the version rewriter. edit, if not
// nil, changes the JSON further. It returns the profile's JSON.
func Inheriting(edit func([]byte) []byte) ([]byte, error) {
	const source = "chrome-155-macos"

	data, err := builtinJSON(source)
	if err != nil {
		return nil, err
	}

	for _, r := range [][2]string{
		{`"name": "chrome-155-macos"`, `"name": "` + InheritingName + `"`},
		{`"version": "155"`, `"version": "999"`},
		{`Chrome/155.0.0.0`, `Chrome/999.0.0.0`},
		{`\"Google Chrome\";v=\"155\", \"Chromium\";v=\"155\", \"Not(A:Brand\";v=\"24\"`, `\"Chromium\";v=\"999\", \"Not-A.Brand\";v=\"8\", \"Google Chrome\";v=\"999\"`},
	} {
		if !bytes.Contains(data, []byte(r[0])) {
			return nil, fmt.Errorf("chrome-155-macos no longer contains %s; update testprofiles.Inheriting", r[0])
		}

		data = bytes.ReplaceAll(data, []byte(r[0]), []byte(r[1]))
	}

	for _, old := range []string{`Chrome/155`, `v=\"155\"`} {
		if bytes.Contains(data, []byte(old)) {
			return nil, fmt.Errorf("%s has a version string (%s) testprofiles.Inheriting does not rewrite", source, old)
		}
	}

	dir, err := repoDir()
	if err != nil {
		return nil, err
	}

	files, err := filepath.Glob(filepath.Join(dir, "internal", "harness", "testdata", "captures", source+".*"))
	if err != nil {
		return nil, err
	}

	from := map[string]string{}
	for _, f := range files {
		if sc, ok := capture.ScenarioOf(source, f); ok && sc != capture.DefaultScenario {
			from[sc] = source
		}
	}

	q, err := profile.Parse(data)
	if err != nil {
		return nil, err
	}

	q.CapturesFrom = from
	if data, err = profile.Encode(q); err != nil {
		return nil, err
	}

	if edit != nil {
		data = edit(data)
	}

	if _, err := profile.Parse(data); err != nil {
		return nil, err
	}

	return data, nil
}
