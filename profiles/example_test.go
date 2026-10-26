package profiles_test

import (
	"fmt"
	"log"

	"github.com/roachadam/mimicry"
	"github.com/roachadam/mimicry/profiles"
)

// Pick the profile a task names in its configuration. Each call returns a
// fresh copy.
func ExampleByName() {
	p, err := profiles.ByName("safari-27-macos")
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(p.Browser, p.Version, p.Platform)

	if _, err := mimicry.NewClient(mimicry.Config{
		Profile: p,
	}); err != nil {
		log.Fatal(err)
	}

	_, err = profiles.ByName("chrome-133")
	fmt.Println(err)
	// Output:
	// safari 27 macos
	// profiles: "chrome-133" is not a built-in profile
}
