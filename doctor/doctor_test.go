package doctor

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"email-mcp-service/config"
)

type fakeSecretStore map[string]string

func (s fakeSecretStore) Get(kind string) (string, error) {
	return s[kind], nil
}

type fakeNetworkChecker struct {
	smtpErr error
	imapErr error
}

func (c fakeNetworkChecker) CheckSMTP(context.Context, *config.Config) error {
	return c.smtpErr
}

func (c fakeNetworkChecker) CheckIMAP(context.Context, *config.Config) error {
	return c.imapErr
}

func TestRunReportsConfiguredLocalSystem(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.json")
	downloadDir := filepath.Join(dir, "attachments")
	codexPath := filepath.Join(dir, "codex.toml")
	commandPath := filepath.Join(dir, "email-mcp.exe")
	local := config.LocalConfig{
		AccountID: "default",
		Mailbox: config.MailboxConfig{
			Email:    "reader@example.com",
			SMTPHost: "smtp.example.com",
			SMTPPort: 465,
			SMTPSSL:  true,
			IMAPHost: "imap.example.com",
			IMAPPort: 993,
			IMAPSSL:  true,
		},
		Folders: config.FolderConfig{AttachmentDownloadDir: downloadDir},
		Index:   config.IndexConfig{Enabled: true, Path: filepath.Join(dir, "mail.db")},
		Permissions: config.PermissionConfig{
			Read:                true,
			Write:               true,
			Send:                true,
			DownloadAttachments: true,
		},
	}
	if err := config.SaveLocalConfig(cfgPath, local); err != nil {
		t.Fatalf("SaveLocalConfig returned error: %v", err)
	}
	if err := os.WriteFile(commandPath, []byte("sidecar"), 0o700); err != nil {
		t.Fatalf("WriteFile sidecar returned error: %v", err)
	}
	if err := os.WriteFile(codexPath, []byte("[mcp_servers.email]\ncommand = "+codexCommandString(commandPath)+"\nargs = [\"mcp\"]\n"), 0o600); err != nil {
		t.Fatalf("WriteFile codex config returned error: %v", err)
	}

	status := Run(context.Background(), Options{
		ConfigPath: cfgPath,
		CodexPath:  codexPath,
		Secrets: fakeSecretStore{
			"smtp": "smtp-secret",
			"imap": "imap-secret",
		},
		SkipNetwork: true,
	})

	if !status.OK {
		t.Fatalf("expected OK status: %#v", status)
	}
	for _, name := range []string{"config", "smtpSecret", "imapSecret", "attachmentDir", "codexConfig", "index"} {
		check := status.Check(name)
		if check == nil || !check.OK {
			t.Fatalf("expected %s check to pass: %#v", name, status.Checks)
		}
	}
}

func TestRunChecksNetworkWhenNotSkipped(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.json")
	commandPath := filepath.Join(dir, "email-mcp.exe")
	codexPath := filepath.Join(dir, "codex.toml")
	local := config.LocalConfig{
		AccountID: "default",
		Mailbox: config.MailboxConfig{
			Email:    "reader@example.com",
			SMTPHost: "smtp.example.com",
			SMTPPort: 465,
			SMTPSSL:  true,
			IMAPHost: "imap.example.com",
			IMAPPort: 993,
			IMAPSSL:  true,
		},
		Folders: config.FolderConfig{AttachmentDownloadDir: filepath.Join(dir, "attachments")},
	}
	if err := config.SaveLocalConfig(cfgPath, local); err != nil {
		t.Fatalf("SaveLocalConfig returned error: %v", err)
	}
	if err := os.WriteFile(commandPath, []byte("sidecar"), 0o700); err != nil {
		t.Fatalf("WriteFile sidecar returned error: %v", err)
	}
	if err := os.WriteFile(codexPath, []byte("[mcp_servers.email]\ncommand = "+codexCommandString(commandPath)+"\nargs = [\"mcp\"]\n"), 0o600); err != nil {
		t.Fatalf("WriteFile codex config returned error: %v", err)
	}

	status := Run(context.Background(), Options{
		ConfigPath: cfgPath,
		CodexPath:  codexPath,
		Secrets: fakeSecretStore{
			"smtp": "smtp-secret",
			"imap": "imap-secret",
		},
		Network: fakeNetworkChecker{},
	})

	for _, name := range []string{"smtpConnection", "imapConnection"} {
		check := status.Check(name)
		if check == nil || !check.OK {
			t.Fatalf("expected %s check to pass: %#v", name, status.Checks)
		}
	}
}

func TestRunReportsNetworkFailure(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.json")
	local := config.LocalConfig{
		AccountID: "default",
		Mailbox: config.MailboxConfig{
			Email:    "reader@example.com",
			SMTPHost: "smtp.example.com",
			SMTPPort: 465,
			SMTPSSL:  true,
			IMAPHost: "imap.example.com",
			IMAPPort: 993,
			IMAPSSL:  true,
		},
		Folders: config.FolderConfig{AttachmentDownloadDir: filepath.Join(dir, "attachments")},
	}
	if err := config.SaveLocalConfig(cfgPath, local); err != nil {
		t.Fatalf("SaveLocalConfig returned error: %v", err)
	}

	status := Run(context.Background(), Options{
		ConfigPath: cfgPath,
		Secrets: fakeSecretStore{
			"smtp": "smtp-secret",
			"imap": "imap-secret",
		},
		Network: fakeNetworkChecker{smtpErr: fmt.Errorf("smtp refused")},
	})

	if status.OK {
		t.Fatalf("expected network failure to fail doctor: %#v", status)
	}
	check := status.Check("smtpConnection")
	if check == nil || check.OK || check.Detail != "smtp refused" {
		t.Fatalf("unexpected smtpConnection check: %#v", check)
	}
}

func codexCommandString(path string) string {
	return "'" + filepath.ToSlash(path) + "'"
}

func TestRunReportsMissingSecretsWithoutLeakingValues(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "config.json")
	local := config.LocalConfig{
		AccountID: "default",
		Mailbox:   config.MailboxConfig{Email: "reader@example.com", SMTPHost: "smtp.example.com", IMAPHost: "imap.example.com"},
	}
	if err := config.SaveLocalConfig(cfgPath, local); err != nil {
		t.Fatalf("SaveLocalConfig returned error: %v", err)
	}

	status := Run(context.Background(), Options{
		ConfigPath:  cfgPath,
		Secrets:     fakeSecretStore{},
		SkipNetwork: true,
	})

	if status.OK {
		t.Fatalf("expected missing secrets to fail doctor")
	}
	for _, check := range status.Checks {
		if check.Detail == "smtp-secret" || check.Detail == "imap-secret" {
			t.Fatalf("doctor leaked secret value: %#v", check)
		}
	}
}
