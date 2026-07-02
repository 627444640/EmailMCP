//go:build !windows && !darwin

package credentials

import "fmt"

// Set is unsupported outside Windows and macOS in v1.
func (Store) Set(kind, value string) error {
	return fmt.Errorf("system credential store is unsupported for %s on this platform", kind)
}

// Get is unsupported outside Windows and macOS in v1.
func (Store) Get(kind string) (string, error) {
	return "", fmt.Errorf("system credential store is unsupported for %s on this platform", kind)
}

// Delete is unsupported outside Windows and macOS in v1.
func (Store) Delete(kind string) error {
	return fmt.Errorf("system credential store is unsupported for %s on this platform", kind)
}
