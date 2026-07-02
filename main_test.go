package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"path/filepath"
	"testing"
	"time"

	"email-mcp-service/config"
	"email-mcp-service/mailindex"
)

func TestCommandFromArgsDefaultsToMCP(t *testing.T) {
	if got := commandFromArgs(nil); got != "mcp" {
		t.Fatalf("expected default command mcp, got %q", got)
	}
	if got := commandFromArgs([]string{"mcp"}); got != "mcp" {
		t.Fatalf("expected explicit command mcp, got %q", got)
	}
}

func TestCommandFromArgsRecognizesConfigAndDoctor(t *testing.T) {
	for _, want := range []string{"config", "doctor", "index"} {
		if got := commandFromArgs([]string{want, "--json"}); got != want {
			t.Fatalf("expected command %q, got %q", want, got)
		}
	}
}

func TestIndexStatusInitializesSQLiteIndex(t *testing.T) {
	var stdout bytes.Buffer
	indexPath := filepath.Join(t.TempDir(), "mail.db")

	if err := run([]string{"index", "status", "--index", indexPath}, bytes.NewReader(nil), &stdout, &bytes.Buffer{}); err != nil {
		t.Fatalf("index status returned error: %v", err)
	}

	var got struct {
		Path         string `json:"path"`
		Initialized  bool   `json:"initialized"`
		FTSEnabled   bool   `json:"ftsEnabled"`
		MessageCount int    `json:"messageCount"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("index status did not return JSON: %v\n%s", err, stdout.String())
	}
	if got.Path != indexPath || !got.Initialized || !got.FTSEnabled || got.MessageCount != 0 {
		t.Fatalf("unexpected index status: %#v", got)
	}
}

func TestIndexSearchReadsQueryJSONFromStdin(t *testing.T) {
	indexPath := filepath.Join(t.TempDir(), "mail.db")
	store, err := mailindex.Open(indexPath)
	if err != nil {
		t.Fatalf("Open index returned error: %v", err)
	}
	defer store.Close()
	if err := store.Init(context.Background()); err != nil {
		t.Fatalf("Init index returned error: %v", err)
	}
	if err := store.UpsertMessages(context.Background(), []mailindex.Message{{
		AccountID: "default",
		Folder:    "INBOX",
		UID:       20,
		Subject:   "电子发票",
		From:      "billing@example.com",
		SentDate:  "2026-06-20T08:00:00+08:00",
		TextBody:  "invoice",
	}}); err != nil {
		t.Fatalf("UpsertMessages returned error: %v", err)
	}

	var stdout bytes.Buffer
	input := bytes.NewBufferString(`{"accountId":"default","keyword":"发票","limit":10}`)
	if err := run([]string{"index", "search", "--index", indexPath}, input, &stdout, &bytes.Buffer{}); err != nil {
		t.Fatalf("index search returned error: %v", err)
	}

	var got mailindex.Result
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("index search did not return JSON: %v\n%s", err, stdout.String())
	}
	if len(got.Items) != 1 || got.Items[0].UID != 20 || got.Items[0].Folder != "INBOX" {
		t.Fatalf("unexpected index search result: %#v", got)
	}
}

func TestCommandFromArgsRejectsOldHTTPTransport(t *testing.T) {
	if got := commandFromArgs([]string{"-transport", "http"}); got != "unsupported" {
		t.Fatalf("expected old HTTP transport to be unsupported, got %q", got)
	}
}

func TestConfigGetMissingFileReturnsDefaultLocalConfig(t *testing.T) {
	var stdout bytes.Buffer
	missingPath := filepath.Join(t.TempDir(), "missing", "config.json")

	if err := run([]string{"config", "get", "--config", missingPath}, bytes.NewReader(nil), &stdout, &bytes.Buffer{}); err != nil {
		t.Fatalf("config get returned error: %v", err)
	}

	var got config.LocalConfig
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("config get did not return JSON: %v", err)
	}
	if got.AccountID != "default" || !got.Permissions.Read || !got.Permissions.Send {
		t.Fatalf("unexpected default config: %#v", got)
	}
	if !got.Mailbox.SMTPSSL || !got.Mailbox.IMAPSSL || got.Mailbox.SMTPPort != 465 || got.Mailbox.IMAPPort != 993 {
		t.Fatalf("unexpected mailbox defaults: %#v", got.Mailbox)
	}
}

func TestReadSecretValueReturnsAfterNewlineWithoutEOF(t *testing.T) {
	reader := &blockingAfterFirstRead{data: []byte("smtp-secret\n")}
	result := make(chan string, 1)
	errs := make(chan error, 1)

	go func() {
		value, err := readSecretValue(reader)
		if err != nil {
			errs <- err
			return
		}
		result <- value
	}()

	select {
	case err := <-errs:
		t.Fatalf("readSecretValue returned error: %v", err)
	case value := <-result:
		if value != "smtp-secret" {
			t.Fatalf("expected smtp-secret, got %q", value)
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatal("readSecretValue waited for EOF after reading a complete line")
	}
}

type blockingAfterFirstRead struct {
	data []byte
	done bool
}

func (r *blockingAfterFirstRead) Read(p []byte) (int, error) {
	if !r.done {
		r.done = true
		return copy(p, r.data), nil
	}
	select {}
}

var _ io.Reader = (*blockingAfterFirstRead)(nil)
