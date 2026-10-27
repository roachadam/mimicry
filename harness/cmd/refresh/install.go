package main

import (
	"context"
	"log"
	"os"
	"path/filepath"
	"slices"
	"strconv"

	"github.com/roachadam/mimicry/harness/internal/capture"
	"github.com/roachadam/mimicry/profile"
)

// supported is how many versions per browser and platform stay supported
// (decision 0035); older ones are deprecated.
const supported = 5

// install moves p's derived profile and staged captures into the
// repository: for a refresh in place the committed files of the recaptured
// scenarios go first. Then it deprecates what falls out of the newest
// five and regenerates the accessors.
func (r *refreshRun) install(ctx context.Context, p *plan, derived string, rep *report) error {
	overlay := filepath.Join(r.Dir, p.Target, "overlay")
	staged, err := os.ReadDir(r.stagedCaptures(p))
	if err != nil {
		return err
	}
	recaptured := map[string]bool{}
	for _, c := range p.Captures {
		recaptured[c.Scenario] = true
	}
	committed, err := os.ReadDir(capturesDir)
	if err != nil {
		return err
	}
	for _, e := range committed {
		if sc, ok := capture.ScenarioOf(p.Name, e.Name()); ok && recaptured[sc] {
			if err := os.Remove(filepath.Join(capturesDir, e.Name())); err != nil {
				return err
			}
		}
	}
	for _, e := range staged {
		// The overlay's copy: hellogen rewrote the default scenario's.
		if err := copyFile(filepath.Join(overlay, e.Name()), filepath.Join(capturesDir, e.Name())); err != nil {
			return err
		}
	}
	if err := copyFile(derived, filepath.Join(profilesDir, p.Name+".json")); err != nil {
		return err
	}
	log.Printf("%s: installed %s and %d captures", p.Target, p.Name, len(staged))
	if rep.deprecated, err = deprecate(p.Browser); err != nil {
		return err
	}
	for _, name := range rep.deprecated {
		log.Printf("%s: deprecated %s (more than %d supported %s versions)", p.Target, name, supported, p.Browser)
	}
	return runLogged(ctx, filepath.Join(r.Dir, p.Target, "generators.log"), "..", "go", "generate", "./profiles")
}

// deprecate marks deprecated every built-in profile of browser older than
// its platform's newest five, and returns the names it marked.
func deprecate(browser string) ([]string, error) {
	builtins, err := loadProfiles(profilesDir)
	if err != nil {
		return nil, err
	}
	byPlatform := map[string][]*profile.Profile{}
	for _, p := range builtins {
		if p.Browser == browser {
			byPlatform[p.Platform] = append(byPlatform[p.Platform], p)
		}
	}
	var marked []string
	for _, ps := range byPlatform {
		slices.SortFunc(ps, func(a, b *profile.Profile) int {
			return versionNumber(b) - versionNumber(a)
		})
		for _, p := range ps[min(supported, len(ps)):] {
			if p.Deprecated {
				continue
			}
			p.Deprecated = true
			data, err := profile.Encode(p)
			if err != nil {
				return nil, err
			}
			if err := os.WriteFile(filepath.Join(profilesDir, p.Name+".json"), data, 0o600); err != nil {
				return nil, err
			}
			marked = append(marked, p.Name)
		}
	}
	slices.Sort(marked)
	return marked, nil
}

// versionNumber is p's version as a number; profile.Validate allows digits
// only.
func versionNumber(p *profile.Profile) int {
	v, _ := strconv.Atoi(p.Version)
	return v
}
