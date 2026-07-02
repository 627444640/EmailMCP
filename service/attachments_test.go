package service

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSaveAttachmentKeepsFileInsideConfiguredDownloadDir(t *testing.T) {
	outputDir := t.TempDir()
	msg := MailMessage{
		UID:      7,
		From:     "Billing Team <billing@example.com>",
		SentDate: "2026-07-01T09:30:00+08:00",
	}

	path, err := saveAttachment(outputDir, msg, `..\evil:invoice.pdf`, []byte("attachment-data"))
	if err != nil {
		t.Fatalf("saveAttachment returned error: %v", err)
	}
	if !strings.HasPrefix(path, outputDir+string(os.PathSeparator)) {
		t.Fatalf("expected attachment inside configured dir %q, got %q", outputDir, path)
	}
	if strings.Contains(filepath.Base(path), "..") || strings.ContainsAny(filepath.Base(path), `<>:"/\|?*`) {
		t.Fatalf("expected sanitized filename, got %q", filepath.Base(path))
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile returned error: %v", err)
	}
	if string(data) != "attachment-data" {
		t.Fatalf("expected saved data, got %q", string(data))
	}
}

func TestAttachmentPathAllowedUsesConfiguredAllowedDirs(t *testing.T) {
	allowedDir := t.TempDir()
	blockedDir := t.TempDir()
	allowedFile := filepath.Join(allowedDir, "send.pdf")
	blockedFile := filepath.Join(blockedDir, "send.pdf")
	if err := os.WriteFile(allowedFile, []byte("ok"), 0o600); err != nil {
		t.Fatalf("WriteFile allowed returned error: %v", err)
	}
	if err := os.WriteFile(blockedFile, []byte("no"), 0o600); err != nil {
		t.Fatalf("WriteFile blocked returned error: %v", err)
	}

	if !isAttachmentPathAllowed(allowedFile, []string{allowedDir}) {
		t.Fatalf("expected file in configured allowed dir to pass")
	}
	if isAttachmentPathAllowed(blockedFile, []string{allowedDir}) {
		t.Fatalf("expected file outside configured allowed dir to be rejected")
	}
}
