// Package codexconfig installs the local Email MCP stdio entry into Codex config.
package codexconfig

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const emailSection = "[mcp_servers.email]"

// InstallResult describes the Codex config change.
type InstallResult struct {
	Changed    bool   `json:"changed"`
	BackupPath string `json:"backupPath,omitempty"`
}

// InstallEmailServer installs or replaces the Email MCP stdio server entry.
func InstallEmailServer(configPath, commandPath string) (InstallResult, error) {
	if strings.TrimSpace(configPath) == "" {
		return InstallResult{}, fmt.Errorf("codex config path is required")
	}
	if strings.TrimSpace(commandPath) == "" {
		return InstallResult{}, fmt.Errorf("email-mcp command path is required")
	}

	var original []byte
	if data, err := os.ReadFile(configPath); err == nil {
		original = data
	} else if !os.IsNotExist(err) {
		return InstallResult{}, err
	}

	updated := replaceEmailSection(string(original), commandPath)
	if updated == string(original) {
		return InstallResult{Changed: false}, nil
	}

	if err := os.MkdirAll(filepath.Dir(configPath), 0o700); err != nil {
		return InstallResult{}, err
	}
	result := InstallResult{Changed: true}
	if len(original) > 0 {
		backupPath := configPath + ".bak." + time.Now().Format("20060102150405")
		if err := os.WriteFile(backupPath, original, 0o600); err != nil {
			return InstallResult{}, err
		}
		result.BackupPath = backupPath
	}
	if err := os.WriteFile(configPath, []byte(updated), 0o600); err != nil {
		return InstallResult{}, err
	}
	return result, nil
}

// RestoreBackup restores a previous Codex config backup.
func RestoreBackup(configPath, backupPath string) error {
	if strings.TrimSpace(configPath) == "" {
		return fmt.Errorf("codex config path is required")
	}
	if strings.TrimSpace(backupPath) == "" {
		return fmt.Errorf("backup path is required")
	}
	data, err := os.ReadFile(backupPath)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(configPath), 0o700); err != nil {
		return err
	}
	return os.WriteFile(configPath, data, 0o600)
}

// IsEmailServerInstalled reports whether config text contains the expected Email MCP command.
func IsEmailServerInstalled(configPath string) bool {
	data, err := os.ReadFile(configPath)
	if err != nil {
		return false
	}
	commandPath, argsOK := parseEmailServerEntry(string(data))
	return argsOK && commandAvailable(commandPath)
}

func parseEmailServerEntry(input string) (commandPath string, argsOK bool) {
	for _, line := range emailSectionBody(input) {
		trimmed := strings.TrimSpace(line)
		key, value, ok := strings.Cut(trimmed, "=")
		if !ok {
			continue
		}
		switch strings.TrimSpace(key) {
		case "command":
			commandPath, _ = parseTOMLString(strings.TrimSpace(value))
		case "args":
			argsOK = strings.Contains(value, `"mcp"`) || strings.Contains(value, `'mcp'`)
		}
	}
	return commandPath, argsOK
}

func emailSectionBody(input string) []string {
	lines := splitLines(input)
	for i, line := range lines {
		if strings.TrimSpace(line) != emailSection {
			continue
		}
		end := len(lines)
		for j := i + 1; j < len(lines); j++ {
			trimmed := strings.TrimSpace(lines[j])
			if strings.HasPrefix(trimmed, "[") && strings.HasSuffix(trimmed, "]") {
				end = j
				break
			}
		}
		return lines[i+1 : end]
	}
	return nil
}

func parseTOMLString(value string) (string, bool) {
	value = strings.TrimSpace(value)
	if len(value) >= 2 && value[0] == '\'' && value[len(value)-1] == '\'' {
		return value[1 : len(value)-1], true
	}
	if len(value) >= 2 && value[0] == '"' && value[len(value)-1] == '"' {
		unquoted, err := strconv.Unquote(value)
		return unquoted, err == nil
	}
	return "", false
}

func commandAvailable(commandPath string) bool {
	commandPath = strings.TrimSpace(commandPath)
	if commandPath == "" {
		return false
	}
	if filepath.IsAbs(commandPath) || strings.ContainsAny(commandPath, `/\`) {
		info, err := os.Stat(commandPath)
		return err == nil && !info.IsDir()
	}
	_, err := exec.LookPath(commandPath)
	return err == nil
}

func replaceEmailSection(input, commandPath string) string {
	block := emailServerBlock(commandPath)
	lines := splitLines(input)
	start := -1
	end := len(lines)
	for i, line := range lines {
		if strings.TrimSpace(line) == emailSection {
			start = i
			break
		}
	}
	if start >= 0 {
		for i := start + 1; i < len(lines); i++ {
			trimmed := strings.TrimSpace(lines[i])
			if strings.HasPrefix(trimmed, "[") && strings.HasSuffix(trimmed, "]") {
				end = i
				break
			}
		}
		next := append([]string{}, lines[:start]...)
		next = append(next, strings.Split(strings.TrimRight(block, "\n"), "\n")...)
		next = append(next, lines[end:]...)
		return ensureTrailingNewline(strings.Join(trimEmptyRuns(next), "\n"))
	}
	if strings.TrimSpace(input) == "" {
		return block
	}
	return ensureTrailingNewline(strings.TrimRight(input, "\r\n") + "\n\n" + strings.TrimRight(block, "\n"))
}

func emailServerBlock(commandPath string) string {
	return emailSection + "\n" +
		"command = " + tomlString(commandPath) + "\n" +
		`args = ["mcp"]` + "\n"
}

func tomlString(value string) string {
	if !strings.Contains(value, "'") {
		return "'" + value + "'"
	}
	escaped := strings.ReplaceAll(value, `\`, `\\`)
	escaped = strings.ReplaceAll(escaped, `"`, `\"`)
	return `"` + escaped + `"`
}

func splitLines(input string) []string {
	input = strings.ReplaceAll(input, "\r\n", "\n")
	input = strings.TrimRight(input, "\n")
	if input == "" {
		return nil
	}
	return strings.Split(input, "\n")
}

func trimEmptyRuns(lines []string) []string {
	result := make([]string, 0, len(lines))
	empty := 0
	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			empty++
			if empty > 1 {
				continue
			}
		} else {
			empty = 0
		}
		result = append(result, line)
	}
	return result
}

func ensureTrailingNewline(value string) string {
	return strings.TrimRight(value, "\r\n") + "\n"
}
