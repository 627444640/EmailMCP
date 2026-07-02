// Email MCP desktop sidecar entrypoint.
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"email-mcp-service/codexconfig"
	"email-mcp-service/config"
	"email-mcp-service/credentials"
	"email-mcp-service/doctor"
	"email-mcp-service/mailindex"
	"email-mcp-service/service"
	"email-mcp-service/tools"

	"github.com/mark3labs/mcp-go/server"
)

func main() {
	if err := run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func commandFromArgs(args []string) string {
	if len(args) == 0 {
		return "mcp"
	}
	switch args[0] {
	case "mcp", "config", "doctor", "index":
		return args[0]
	default:
		if strings.HasPrefix(args[0], "-") {
			return "unsupported"
		}
		return args[0]
	}
}

func run(args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	switch commandFromArgs(args) {
	case "mcp":
		return runMCP(stripCommand(args, "mcp"), stderr)
	case "config":
		return runConfig(stripCommand(args, "config"), stdin, stdout)
	case "doctor":
		return runDoctor(stripCommand(args, "doctor"), stdout)
	case "index":
		return runIndex(stripCommand(args, "index"), stdin, stdout)
	case "unsupported":
		return fmt.Errorf("HTTP/transport flags were removed; use `email-mcp mcp`, `email-mcp config`, or `email-mcp doctor`")
	default:
		return fmt.Errorf("unknown command: %s", args[0])
	}
}

func runIndex(args []string, stdin io.Reader, stdout io.Writer) error {
	if len(args) == 0 {
		return fmt.Errorf("index command required")
	}
	switch args[0] {
	case "status":
		fs := flag.NewFlagSet("index status", flag.ContinueOnError)
		indexPath := fs.String("index", "", "SQLite mailbox index path")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		path, err := resolveIndexPath(*indexPath)
		if err != nil {
			return err
		}
		store, err := mailindex.Open(path)
		if err != nil {
			return err
		}
		defer store.Close()
		if err := store.Init(context.Background()); err != nil {
			return err
		}
		status, err := store.Status(context.Background())
		if err != nil {
			return err
		}
		return json.NewEncoder(stdout).Encode(status)
	case "search":
		fs := flag.NewFlagSet("index search", flag.ContinueOnError)
		indexPath := fs.String("index", "", "SQLite mailbox index path")
		inputPath := fs.String("input", "", "JSON query input path; stdin is used when empty")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		path, err := resolveIndexPath(*indexPath)
		if err != nil {
			return err
		}
		data, err := readJSONInput(stdin, *inputPath)
		if err != nil {
			return err
		}
		var query mailindex.Query
		if err := json.Unmarshal(data, &query); err != nil {
			return err
		}
		store, err := mailindex.Open(path)
		if err != nil {
			return err
		}
		defer store.Close()
		if err := store.Init(context.Background()); err != nil {
			return err
		}
		result, err := store.Search(context.Background(), query)
		if err != nil {
			return err
		}
		return json.NewEncoder(stdout).Encode(result)
	case "sync":
		fs := flag.NewFlagSet("index sync", flag.ContinueOnError)
		configPath := fs.String("config", "", "local config path")
		indexPath := fs.String("index", "", "SQLite mailbox index path")
		folders := fs.String("folders", "", "comma-separated folders; all selectable folders are synced when empty")
		limitPerFolder := fs.Int("limit-per-folder", 200, "maximum messages to index per folder")
		fullBodies := fs.Bool("full", false, "fetch and index full text/html bodies")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		cfg, err := loadRuntimeConfig(*configPath)
		if err != nil {
			return err
		}
		path, err := resolveIndexPath(firstNonEmptyString(*indexPath, cfg.Index.Path))
		if err != nil {
			return err
		}
		result, err := service.SyncMailboxIndex(context.Background(), cfg, service.IndexSyncRequest{
			IndexPath:      path,
			AccountID:      cfg.AccountID,
			Folders:        splitArgCSV(*folders),
			LimitPerFolder: *limitPerFolder,
			FullBodies:     *fullBodies,
		})
		if err != nil {
			return err
		}
		return json.NewEncoder(stdout).Encode(result)
	default:
		return fmt.Errorf("unknown index command: %s", args[0])
	}
}

func resolveIndexPath(path string) (string, error) {
	path = strings.TrimSpace(path)
	if path != "" {
		return path, nil
	}
	return config.DefaultIndexPath()
}

func readJSONInput(stdin io.Reader, inputPath string) ([]byte, error) {
	if strings.TrimSpace(inputPath) != "" {
		return os.ReadFile(inputPath)
	}
	return io.ReadAll(stdin)
}

func splitArgCSV(value string) []string {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	var result []string
	for _, part := range strings.Split(value, ",") {
		part = strings.TrimSpace(part)
		if part != "" {
			result = append(result, part)
		}
	}
	return result
}

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func runMCP(args []string, stderr io.Writer) error {
	fs := flag.NewFlagSet("mcp", flag.ContinueOnError)
	fs.SetOutput(stderr)
	configPath := fs.String("config", "", "local config path")
	if err := fs.Parse(args); err != nil {
		return err
	}
	cfg, err := loadRuntimeConfig(*configPath)
	if err != nil {
		return err
	}
	mcpServer := server.NewMCPServer(
		"email-mcp",
		"2.0.0",
		server.WithToolCapabilities(true),
	)
	tools.RegisterLocalEmailTools(mcpServer, cfg)
	return server.ServeStdio(mcpServer)
}

func runConfig(args []string, stdin io.Reader, stdout io.Writer) error {
	if len(args) == 0 {
		return fmt.Errorf("config command required")
	}
	switch args[0] {
	case "path":
		path, err := config.DefaultLocalConfigPath()
		if err != nil {
			return err
		}
		_, err = fmt.Fprintln(stdout, path)
		return err
	case "get":
		fs := flag.NewFlagSet("config get", flag.ContinueOnError)
		configPath := fs.String("config", "", "local config path")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		local, err := config.LoadLocalConfig(*configPath)
		if err != nil {
			if !os.IsNotExist(err) {
				return err
			}
			local = config.DefaultLocalConfig()
		}
		return json.NewEncoder(stdout).Encode(local)
	case "save":
		fs := flag.NewFlagSet("config save", flag.ContinueOnError)
		configPath := fs.String("config", "", "local config path")
		inputPath := fs.String("input", "", "JSON input path; stdin is used when empty")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		var data []byte
		var err error
		if *inputPath != "" {
			data, err = os.ReadFile(*inputPath)
		} else {
			data, err = io.ReadAll(stdin)
		}
		if err != nil {
			return err
		}
		var local config.LocalConfig
		if err := json.Unmarshal(data, &local); err != nil {
			return err
		}
		return config.SaveLocalConfig(*configPath, local)
	case "set-secret":
		fs := flag.NewFlagSet("config set-secret", flag.ContinueOnError)
		kind := fs.String("kind", "", "secret kind: smtp or imap")
		valueStdin := fs.Bool("value-stdin", true, "read secret value from stdin")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		if *kind != credentials.SMTPKind && *kind != credentials.IMAPKind {
			return fmt.Errorf("secret kind must be smtp or imap")
		}
		if !*valueStdin {
			return fmt.Errorf("only --value-stdin=true is supported to avoid leaking secrets in process args")
		}
		value, err := readSecretValue(stdin)
		if err != nil {
			return err
		}
		return credentials.Store{}.Set(*kind, value)
	case "install-codex":
		fs := flag.NewFlagSet("config install-codex", flag.ContinueOnError)
		codexPath := fs.String("codex", "", "Codex config.toml path")
		commandPath := fs.String("command", "", "installed email-mcp executable path")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		path := *codexPath
		if path == "" {
			path = defaultCodexConfigPath()
		}
		command := *commandPath
		if command == "" {
			exe, err := os.Executable()
			if err != nil {
				return err
			}
			command = exe
		}
		result, err := codexconfig.InstallEmailServer(path, command)
		if err != nil {
			return err
		}
		return json.NewEncoder(stdout).Encode(result)
	case "restore-codex":
		fs := flag.NewFlagSet("config restore-codex", flag.ContinueOnError)
		codexPath := fs.String("codex", "", "Codex config.toml path")
		backupPath := fs.String("backup", "", "backup path to restore")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		path := *codexPath
		if path == "" {
			path = defaultCodexConfigPath()
		}
		return codexconfig.RestoreBackup(path, *backupPath)
	default:
		return fmt.Errorf("unknown config command: %s", args[0])
	}
}

func readSecretValue(stdin io.Reader) (string, error) {
	value, err := bufio.NewReader(stdin).ReadString('\n')
	if err != nil && !(err == io.EOF && value != "") {
		return "", err
	}
	return strings.TrimRight(value, "\r\n"), nil
}

func runDoctor(args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("doctor", flag.ContinueOnError)
	asJSON := fs.Bool("json", false, "write JSON status")
	configPath := fs.String("config", "", "local config path")
	codexPath := fs.String("codex", "", "Codex config.toml path")
	skipNetwork := fs.Bool("skip-network", false, "skip SMTP/IMAP connection checks")
	if err := fs.Parse(args); err != nil {
		return err
	}
	path := *codexPath
	if path == "" {
		path = defaultCodexConfigPath()
	}
	status := doctor.Run(context.Background(), doctor.Options{
		ConfigPath:  *configPath,
		CodexPath:   path,
		Secrets:     credentials.Store{},
		SkipNetwork: *skipNetwork,
	})
	if *asJSON {
		return json.NewEncoder(stdout).Encode(status)
	}
	for _, check := range status.Checks {
		state := "ok"
		if !check.OK {
			state = "fail"
		}
		if _, err := fmt.Fprintf(stdout, "%s\t%s\t%s\n", state, check.Name, check.Detail); err != nil {
			return err
		}
	}
	if !status.OK {
		return fmt.Errorf("doctor found failed checks")
	}
	return nil
}

func loadRuntimeConfig(configPath string) (*config.Config, error) {
	local, err := config.LoadLocalConfig(configPath)
	if err == nil {
		secrets := credentials.Store{}
		smtpPassword, err := secrets.Get(credentials.SMTPKind)
		if err != nil {
			return nil, err
		}
		imapPassword, err := secrets.Get(credentials.IMAPKind)
		if err != nil {
			return nil, err
		}
		return local.ToRuntimeConfig(smtpPassword, imapPassword), nil
	}

	legacy := config.Load()
	if legacy.SMTP.Host == "" && legacy.IMAP.Host == "" {
		return nil, fmt.Errorf("local config not found: %w", err)
	}
	if !legacy.Permissions.Read && !legacy.Permissions.Write && !legacy.Permissions.Send && !legacy.Permissions.DownloadAttachments {
		legacy.Permissions = config.PermissionConfig{Read: true, Write: true, Send: true, DownloadAttachments: true}
	}
	return legacy, nil
}

func stripCommand(args []string, command string) []string {
	if len(args) > 0 && args[0] == command {
		return args[1:]
	}
	return args
}

func defaultCodexConfigPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return "config.toml"
	}
	return filepath.Join(home, ".codex", "config.toml")
}
