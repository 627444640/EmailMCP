//go:build darwin

package credentials

import (
	"bytes"
	"fmt"
	"os/exec"
	"strings"
)

const keychainService = "EmailMCP"

// Set writes a secret to macOS Keychain.
func (Store) Set(kind, value string) error {
	account := "default:" + kind
	_ = exec.Command("security", "delete-generic-password", "-s", keychainService, "-a", account).Run()
	out, err := exec.Command("security", "add-generic-password", "-s", keychainService, "-a", account, "-w", value).CombinedOutput()
	if err != nil {
		return fmt.Errorf("security add-generic-password failed: %s: %w", strings.TrimSpace(string(out)), err)
	}
	return nil
}

// Get reads a secret from macOS Keychain.
func (Store) Get(kind string) (string, error) {
	account := "default:" + kind
	out, err := exec.Command("security", "find-generic-password", "-s", keychainService, "-a", account, "-w").CombinedOutput()
	if err != nil {
		if bytes.Contains(out, []byte("could not be found")) {
			return "", nil
		}
		return "", fmt.Errorf("security find-generic-password failed: %s: %w", strings.TrimSpace(string(out)), err)
	}
	return strings.TrimRight(string(out), "\r\n"), nil
}

// Delete removes a secret from macOS Keychain.
func (Store) Delete(kind string) error {
	account := "default:" + kind
	out, err := exec.Command("security", "delete-generic-password", "-s", keychainService, "-a", account).CombinedOutput()
	if err != nil && !bytes.Contains(out, []byte("could not be found")) {
		return fmt.Errorf("security delete-generic-password failed: %s: %w", strings.TrimSpace(string(out)), err)
	}
	return nil
}
