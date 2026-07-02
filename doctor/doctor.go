// Package doctor reports local Email MCP desktop configuration status.
package doctor

import (
	"context"
	"fmt"
	"os"

	"email-mcp-service/codexconfig"
	"email-mcp-service/config"
	"email-mcp-service/mailindex"
	"email-mcp-service/service"
)

// SecretGetter reads one kind of local mailbox secret.
type SecretGetter interface {
	Get(kind string) (string, error)
}

// NetworkChecker verifies mailbox connectivity without sending or reading messages.
type NetworkChecker interface {
	CheckSMTP(context.Context, *config.Config) error
	CheckIMAP(context.Context, *config.Config) error
}

// Options configures a doctor run.
type Options struct {
	ConfigPath  string
	CodexPath   string
	Secrets     SecretGetter
	Network     NetworkChecker
	SkipNetwork bool
}

// CheckResult is one named diagnostic check.
type CheckResult struct {
	Name   string `json:"name"`
	OK     bool   `json:"ok"`
	Detail string `json:"detail,omitempty"`
}

// Status is the complete doctor result.
type Status struct {
	OK     bool          `json:"ok"`
	Checks []CheckResult `json:"checks"`
}

// Check returns a check by name.
func (s Status) Check(name string) *CheckResult {
	for i := range s.Checks {
		if s.Checks[i].Name == name {
			return &s.Checks[i]
		}
	}
	return nil
}

// Run performs local desktop diagnostics.
func Run(ctx context.Context, opts Options) Status {
	var status Status

	local, err := config.LoadLocalConfig(opts.ConfigPath)
	if err != nil {
		status.add("config", false, err.Error())
		status.finalize()
		return status
	}
	status.add("config", true, "loaded")

	secrets := make(map[string]string, 2)
	checkSecret := func(name string) {
		if opts.Secrets == nil {
			status.add(name+"Secret", false, "secret store is not configured")
			return
		}
		value, err := opts.Secrets.Get(name)
		if err != nil {
			status.add(name+"Secret", false, err.Error())
			return
		}
		if value == "" {
			status.add(name+"Secret", false, "missing")
			return
		}
		secrets[name] = value
		status.add(name+"Secret", true, "configured")
	}
	checkSecret("smtp")
	checkSecret("imap")

	if local.Folders.AttachmentDownloadDir == "" {
		status.add("attachmentDir", false, "attachment download directory is not configured")
	} else if err := os.MkdirAll(local.Folders.AttachmentDownloadDir, 0o700); err != nil {
		status.add("attachmentDir", false, err.Error())
	} else {
		status.add("attachmentDir", true, local.Folders.AttachmentDownloadDir)
	}

	if opts.CodexPath == "" {
		status.add("codexConfig", false, "codex config path is not configured")
	} else if codexconfig.IsEmailServerInstalled(opts.CodexPath) {
		status.add("codexConfig", true, "email MCP installed")
	} else {
		status.add("codexConfig", false, fmt.Sprintf("email MCP is not installed in %s", opts.CodexPath))
	}

	if local.Index.Enabled {
		indexPath := local.Index.Path
		if indexPath == "" {
			indexPath, err = config.DefaultIndexPath()
		}
		if err != nil {
			status.add("index", false, err.Error())
		} else if check, err := checkIndex(ctx, indexPath); err != nil {
			status.add("index", false, err.Error())
		} else {
			status.add("index", true, fmt.Sprintf("%s messages=%d fts=%t", check.Path, check.MessageCount, check.FTSEnabled))
		}
	}

	if opts.SkipNetwork {
		status.add("network", true, "skipped")
	} else {
		checker := opts.Network
		if checker == nil {
			checker = serviceNetworkChecker{}
		}
		runtime := local.ToRuntimeConfig(secrets["smtp"], secrets["imap"])
		if err := checker.CheckSMTP(ctx, runtime); err != nil {
			status.add("smtpConnection", false, err.Error())
		} else {
			status.add("smtpConnection", true, "connected")
		}
		if err := checker.CheckIMAP(ctx, runtime); err != nil {
			status.add("imapConnection", false, err.Error())
		} else {
			status.add("imapConnection", true, "connected")
		}
	}
	status.finalize()
	return status
}

func checkIndex(ctx context.Context, path string) (mailindex.Status, error) {
	store, err := mailindex.Open(path)
	if err != nil {
		return mailindex.Status{}, err
	}
	defer store.Close()
	if err := store.Init(ctx); err != nil {
		return mailindex.Status{}, err
	}
	return store.Status(ctx)
}

type serviceNetworkChecker struct{}

func (serviceNetworkChecker) CheckSMTP(ctx context.Context, cfg *config.Config) error {
	_ = ctx
	return service.CheckSMTPConnection(cfg)
}

func (serviceNetworkChecker) CheckIMAP(ctx context.Context, cfg *config.Config) error {
	_ = ctx
	return service.CheckIMAPConnection(cfg)
}

func (s *Status) add(name string, ok bool, detail string) {
	s.Checks = append(s.Checks, CheckResult{Name: name, OK: ok, Detail: detail})
}

func (s *Status) finalize() {
	s.OK = true
	for _, check := range s.Checks {
		if !check.OK {
			s.OK = false
			return
		}
	}
}
