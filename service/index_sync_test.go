package service

import (
	"context"
	"path/filepath"
	"testing"

	"email-mcp-service/config"
	"email-mcp-service/mailindex"
)

type fakeIndexSyncSource struct {
	folders ListFoldersResult
	pages   map[string][]ListEmailsV2Result
	calls   []ListEmailsV2Request
}

func (s *fakeIndexSyncSource) ListFolders(*config.Config) (*ListFoldersResult, error) {
	return &s.folders, nil
}

func (s *fakeIndexSyncSource) ListEmailsV2(_ *config.Config, req *ListEmailsV2Request) (*ListEmailsV2Result, error) {
	s.calls = append(s.calls, *req)
	pages := s.pages[req.Folder]
	if len(pages) == 0 {
		return &ListEmailsV2Result{}, nil
	}
	page := pages[0]
	s.pages[req.Folder] = pages[1:]
	return &page, nil
}

func TestSyncMailboxIndexIndexesPagedFolderMessages(t *testing.T) {
	ctx := context.Background()
	store, err := mailindex.Open(filepath.Join(t.TempDir(), "mail.db"))
	if err != nil {
		t.Fatalf("Open returned error: %v", err)
	}
	defer store.Close()
	if err := store.Init(ctx); err != nil {
		t.Fatalf("Init returned error: %v", err)
	}
	source := &fakeIndexSyncSource{
		folders: ListFoldersResult{Folders: []MailFolder{{Name: "INBOX", Path: "INBOX"}}},
		pages: map[string][]ListEmailsV2Result{
			"INBOX": {
				{
					Items: []MailMessage{{
						UID:      1,
						Subject:  "电子发票",
						From:     "billing@example.com",
						SentDate: "2026-06-01T08:00:00+08:00",
						TextBody: "invoice",
					}},
					HasMore:    true,
					NextCursor: "cursor-1",
				},
				{
					Items: []MailMessage{{
						UID:      2,
						Subject:  "Receipt",
						From:     "billing@example.com",
						SentDate: "2026-06-02T08:00:00+08:00",
					}},
				},
			},
		},
	}

	result, err := syncMailboxIndex(ctx, &config.Config{AccountID: "default"}, store, source, IndexSyncRequest{LimitPerFolder: 2, FullBodies: true})
	if err != nil {
		t.Fatalf("syncMailboxIndex returned error: %v", err)
	}
	if result.IndexedMessages != 2 || len(result.Folders) != 1 || result.Folders[0].IndexedMessages != 2 {
		t.Fatalf("unexpected sync result: %#v", result)
	}
	if len(source.calls) != 2 || source.calls[0].View != "FULL" || source.calls[1].Cursor != "cursor-1" {
		t.Fatalf("unexpected ListEmailsV2 calls: %#v", source.calls)
	}

	search, err := store.Search(ctx, mailindex.Query{AccountID: "default", Folder: "INBOX", Keyword: "发票", Limit: 10})
	if err != nil {
		t.Fatalf("Search returned error: %v", err)
	}
	if len(search.Items) != 1 || search.Items[0].UID != 1 || search.Items[0].Folder != "INBOX" {
		t.Fatalf("unexpected indexed search result: %#v", search.Items)
	}
}
