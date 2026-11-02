package profile_test

import (
	"fmt"
	"log"

	"github.com/roachadam/mimicry"
	"github.com/roachadam/mimicry/profile"
	"github.com/roachadam/mimicry/profiles"
)

// Edit a profile once and build every client from the result: With copies
// the profile, applies the edit and checks it, and a client built from a
// Profile copies nothing. With never fails: an edit that leaves the profile
// invalid is the error of the client built from it.
func ExampleProfile_With() {
	mine := profiles.ChromeMacOS().With(func(s *profile.Spec) {
		s.Name = "my-chrome"
	})

	for range 3 {
		if _, err := mimicry.NewClient(mimicry.Config{
			Profile: mine,
		}); err != nil {
			log.Fatal(err)
		}
	}

	fmt.Println(mine.Name())

	broken := profiles.ChromeMacOS().With(func(s *profile.Spec) {
		s.Name = ""
	})

	_, err := mimicry.NewClient(mimicry.Config{
		Profile: broken,
	})
	fmt.Println(err)

	// Output:
	// my-chrome
	// profile: invalid:
	//   name: is required
}

// Read what a profile sends, or save it, through Spec: a deep copy of the
// profile's data, as costly as With. Requests never need it; a client takes
// the Profile itself. Changing the copy changes no Profile.
func ExampleProfile_Spec() {
	s := profiles.Chrome156MacOS().Spec()

	for _, setting := range s.HTTP2.Settings {
		fmt.Printf("SETTINGS %d=%d\n", setting.ID, setting.Value)
	}

	fmt.Println("cookies without SameSite:", s.Cookies.UnsetSameSite)

	// Save it, to diff with another profile or to load later with Parse.
	data, err := profile.Encode(s)
	if err != nil {
		log.Fatal(err)
	}

	if _, err := profile.Parse(data); err != nil {
		log.Fatal(err)
	}

	// Output:
	// SETTINGS 1=65536
	// SETTINGS 2=0
	// SETTINGS 4=6291456
	// SETTINGS 6=262144
	// cookies without SameSite: lax
}
