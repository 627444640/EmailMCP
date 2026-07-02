package codexconfig

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInstallEmailServerBacksUpAndIsIdempotent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	initial := `[projects.'D:\Workspace\EmailMCP']
trust_level = "trusted"

[mcp_servers.other]
command = "other.exe"
`
	if err := os.WriteFile(path, []byte(initial), 0o600); err != nil {
		t.Fatalf("WriteFile returned error: %v", err)
	}

	result, err := InstallEmailServer(path, `C:\Program Files\EmailMCP\email-mcp.exe`)
	if err != nil {
		t.Fatalf("InstallEmailServer returned error: %v", err)
	}
	if result.BackupPath == "" {
		t.Fatalf("expected backup path")
	}
	if _, err := os.Stat(result.BackupPath); err != nil {
		t.Fatalf("expected backup file: %v", err)
	}

	updatedBytes, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile returned error: %v", err)
	}
	updated := string(updatedBytes)
	if !strings.Contains(updated, "[mcp_servers.email]") {
		t.Fatalf("email MCP section missing: %s", updated)
	}
	if !strings.Contains(updated, `command = 'C:\Program Files\EmailMCP\email-mcp.exe'`) {
		t.Fatalf("email MCP command missing: %s", updated)
	}
	if !strings.Contains(updated, `args = ["mcp"]`) {
		t.Fatalf("email MCP args missing: %s", updated)
	}
	if strings.Count(updated, "[mcp_servers.email]") != 1 {
		t.Fatalf("expected one email MCP section, got: %s", updated)
	}

	second, err := InstallEmailServer(path, `C:\Program Files\EmailMCP\email-mcp.exe`)
	if err != nil {
		t.Fatalf("second InstallEmailServer returned error: %v", err)
	}
	if second.Changed {
		t.Fatalf("second install should be idempotent")
	}
	secondBytes, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile second returned error: %v", err)
	}
	if strings.Count(string(secondBytes), "[mcp_servers.email]") != 1 {
		t.Fatalf("expected one email MCP section after second install, got: %s", string(secondBytes))
	}
}

func TestInstallEmailServerReplacesExistingEmailSection(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	initial := `[mcp_servers.email]
command = "old.exe"
args = ["old"]

[mcp_servers.other]
command = "other.exe"
`
	if err := os.WriteFile(path, []byte(initial), 0o600); err != nil {
		t.Fatalf("WriteFile returned error: %v", err)
	}

	if _, err := InstallEmailServer(path, `/Applications/Email MCP.app/Contents/MacOS/email-mcp`); err != nil {
		t.Fatalf("InstallEmailServer returned error: %v", err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile returned error: %v", err)
	}
	text := string(raw)
	if strings.Contains(text, "old.exe") || strings.Contains(text, `["old"]`) {
		t.Fatalf("old email section was not replaced: %s", text)
	}
	if !strings.Contains(text, "[mcp_servers.other]") {
		t.Fatalf("unrelated sections should be preserved: %s", text)
	}
}

func TestRestoreBackupReplacesCodexConfig(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.toml")
	backupPath := filepath.Join(dir, "config.toml.bak.20260701120000")
	if err := os.WriteFile(configPath, []byte("[mcp_servers.email]\nargs = [\"mcp\"]\n"), 0o600); err != nil {
		t.Fatalf("WriteFile config returned error: %v", err)
	}
	want := "[mcp_servers.original]\ncommand = 'keep'\n"
	if err := os.WriteFile(backupPath, []byte(want), 0o600); err != nil {
		t.Fatalf("WriteFile backup returned error: %v", err)
	}

	if err := RestoreBackup(configPath, backupPath); err != nil {
		t.Fatalf("RestoreBackup returned error: %v", err)
	}

	got, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("ReadFile returned error: %v", err)
	}
	if string(got) != want {
		t.Fatalf("unexpected restored config:\n%s", got)
	}
}

func TestIsEmailServerInstalledRejectsMissingCommandPath(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	raw := `[mcp_servers.email]
command = 'C:\missing\email-mcp.exe'
args = ["mcp"]
`
	if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
		t.Fatalf("WriteFile returned error: %v", err)
	}

	if IsEmailServerInstalled(path) {
		t.Fatal("expected missing command path to be rejected")
	}
}

func TestIsEmailServerInstalledAcceptsExistingCommandPath(t *testing.T) {
	dir := t.TempDir()
	commandPath := filepath.Join(dir, "email-mcp.exe")
	if err := os.WriteFile(commandPath, []byte("sidecar"), 0o700); err != nil {
		t.Fatalf("WriteFile command returned error: %v", err)
	}
	path := filepath.Join(dir, "config.toml")
	raw := "[mcp_servers.email]\ncommand = " + tomlString(commandPath) + "\nargs = [\"mcp\"]\n"
	if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
		t.Fatalf("WriteFile config returned error: %v", err)
	}

	if !IsEmailServerInstalled(path) {
		t.Fatal("expected existing command path to be accepted")
	}
}
