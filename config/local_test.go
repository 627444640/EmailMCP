package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSaveLocalConfigDoesNotPersistSecrets(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	cfg := LocalConfig{
		AccountID: "default",
		Mailbox: MailboxConfig{
			Email:    "reader@example.com",
			SMTPHost: "smtp.example.com",
			SMTPPort: 465,
			SMTPSSL:  true,
			IMAPHost: "imap.example.com",
			IMAPPort: 993,
			IMAPSSL:  true,
		},
		Folders: FolderConfig{
			AttachmentDownloadDir: filepath.Join(t.TempDir(), "attachments"),
			AllowedAttachmentDirs: []string{filepath.Join(t.TempDir(), "outbox")},
		},
		Permissions: PermissionConfig{
			Read:                true,
			Write:               true,
			Send:                true,
			DownloadAttachments: true,
		},
	}

	if err := SaveLocalConfig(path, cfg); err != nil {
		t.Fatalf("SaveLocalConfig returned error: %v", err)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile returned error: %v", err)
	}
	text := string(raw)
	if strings.Contains(text, "password") || strings.Contains(text, "authCode") || strings.Contains(text, "secret") {
		t.Fatalf("local config should not contain secret-looking fields: %s", text)
	}

	loaded, err := LoadLocalConfig(path)
	if err != nil {
		t.Fatalf("LoadLocalConfig returned error: %v", err)
	}
	if loaded.AccountID != "default" || loaded.Mailbox.Email != "reader@example.com" {
		t.Fatalf("unexpected loaded config: %#v", loaded)
	}
	if !loaded.Permissions.Read || !loaded.Permissions.Write || !loaded.Permissions.Send || !loaded.Permissions.DownloadAttachments {
		t.Fatalf("permissions were not preserved: %#v", loaded.Permissions)
	}
}

func TestLocalConfigToRuntimeConfigUsesSuppliedSecrets(t *testing.T) {
	local := LocalConfig{
		AccountID: "default",
		Mailbox: MailboxConfig{
			Email:    "reader@example.com",
			SMTPHost: "smtp.example.com",
			SMTPPort: 465,
			SMTPSSL:  true,
			IMAPHost: "imap.example.com",
			IMAPPort: 993,
			IMAPSSL:  true,
		},
		Folders: FolderConfig{AttachmentDownloadDir: "attachments", AllowedAttachmentDirs: []string{"allowed"}},
		Index:   IndexConfig{Enabled: true, Path: "mail.db"},
	}

	runtime := local.ToRuntimeConfig("smtp-secret", "imap-secret")
	if runtime.SMTP.Username != "reader@example.com" || runtime.SMTP.Password != "smtp-secret" {
		t.Fatalf("unexpected SMTP runtime config: %#v", runtime.SMTP)
	}
	if runtime.IMAP.Username != "reader@example.com" || runtime.IMAP.Password != "imap-secret" {
		t.Fatalf("unexpected IMAP runtime config: %#v", runtime.IMAP)
	}
	if runtime.Attachments.DownloadDir != "attachments" || len(runtime.Attachments.AllowedSendDirs) != 1 {
		t.Fatalf("unexpected attachment config: %#v", runtime.Attachments)
	}
	if runtime.AccountID != "default" || !runtime.Index.Enabled || runtime.Index.Path != "mail.db" {
		t.Fatalf("unexpected index runtime config: %#v", runtime)
	}
}

func TestLoadLocalConfigDefaultsSSLForOlderConfigFiles(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	raw := `{
		"accountId": "default",
		"mailbox": {
			"email": "reader@example.com",
			"smtpHost": "smtp.example.com",
			"smtpPort": 465,
			"imapHost": "imap.example.com",
			"imapPort": 993
		}
	}`
	if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
		t.Fatalf("WriteFile returned error: %v", err)
	}

	loaded, err := LoadLocalConfig(path)
	if err != nil {
		t.Fatalf("LoadLocalConfig returned error: %v", err)
	}
	if !loaded.Mailbox.SMTPSSL || !loaded.Mailbox.IMAPSSL {
		t.Fatalf("expected missing SSL fields to default to true, got %#v", loaded.Mailbox)
	}
	if !loaded.Index.Enabled {
		t.Fatalf("expected missing index.enabled to default to true, got %#v", loaded.Index)
	}
}

func TestLoadLocalConfigPreservesDisabledIndex(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	cfg := DefaultLocalConfig()
	cfg.Index.Enabled = false
	cfg.Index.Path = filepath.Join(t.TempDir(), "mail.db")
	if err := SaveLocalConfig(path, cfg); err != nil {
		t.Fatalf("SaveLocalConfig returned error: %v", err)
	}

	loaded, err := LoadLocalConfig(path)
	if err != nil {
		t.Fatalf("LoadLocalConfig returned error: %v", err)
	}
	if loaded.Index.Enabled {
		t.Fatalf("expected disabled index to be preserved: %#v", loaded.Index)
	}
}
