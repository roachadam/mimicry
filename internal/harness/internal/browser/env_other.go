//go:build !windows && !linux

package browser

// browserEnv keeps the tool's environment here.
func browserEnv(*Browser, string) []string {
	return nil
}
