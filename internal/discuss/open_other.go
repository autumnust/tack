//go:build !darwin && !linux

package discuss

import "fmt"

// OpenURL on unsupported platforms returns an error so the caller can
// fall back to printing the URL in the status bar.
func OpenURL(url string) error {
	return fmt.Errorf("auto browser open not supported on this platform; visit %s manually", url)
}
