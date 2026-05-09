//go:build darwin

package discuss

import "os/exec"

// OpenURL hands the URL to the OS's default browser. Errors are
// returned for the caller to surface; we don't block on the browser.
func OpenURL(url string) error {
	return exec.Command("open", url).Start()
}
