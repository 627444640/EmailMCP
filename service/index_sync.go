package service

import (
	"context"
	"fmt"
	"strings"

	"email-mcp-service/config"
	"email-mcp-service/mailindex"
)

// IndexSyncRequest configures a mailbox-to-SQLite index sync.
type IndexSyncRequest struct {
	IndexPath      string   `json:"indexPath,omitempty"`
	AccountID      string   `json:"accountId,omitempty"`
	Folders        []string `json:"folders,omitempty"`
	LimitPerFolder int      `json:"limitPerFolder,omitempty"`
	FullBodies     bool     `json:"fullBodies,omitempty"`
}

// IndexSyncFolderResult describes one synced folder.
type IndexSyncFolderResult struct {
	Folder          string `json:"folder"`
	IndexedMessages int    `json:"indexedMessages"`
	Error           string `json:"error,omitempty"`
}

// IndexSyncResult is returned after indexing mailbox messages.
type IndexSyncResult struct {
	IndexPath       string                  `json:"indexPath"`
	AccountID       string                  `json:"accountId"`
	IndexedMessages int                     `json:"indexedMessages"`
	Folders         []IndexSyncFolderResult `json:"folders"`
}

type indexSyncSource interface {
	ListFolders(*config.Config) (*ListFoldersResult, error)
	ListEmailsV2(*config.Config, *ListEmailsV2Request) (*ListEmailsV2Result, error)
}

type defaultIndexSyncSource struct{}

func (defaultIndexSyncSource) ListFolders(cfg *config.Config) (*ListFoldersResult, error) {
	return ListFolders(cfg)
}

func (defaultIndexSyncSource) ListEmailsV2(cfg *config.Config, req *ListEmailsV2Request) (*ListEmailsV2Result, error) {
	return ListEmailsV2(cfg, req)
}

// SyncMailboxIndex opens the configured SQLite index and syncs messages from IMAP.
func SyncMailboxIndex(ctx context.Context, cfg *config.Config, req IndexSyncRequest) (*IndexSyncResult, error) {
	if cfg == nil {
		cfg = &config.Config{}
	}
	path := firstNonEmpty(strings.TrimSpace(req.IndexPath), strings.TrimSpace(cfg.Index.Path))
	if path == "" {
		var err error
		path, err = config.DefaultIndexPath()
		if err != nil {
			return nil, err
		}
	}
	store, err := mailindex.Open(path)
	if err != nil {
		return nil, err
	}
	defer store.Close()
	if err := store.Init(ctx); err != nil {
		return nil, err
	}
	req.IndexPath = path
	return syncMailboxIndex(ctx, cfg, store, defaultIndexSyncSource{}, req)
}

func syncMailboxIndex(ctx context.Context, cfg *config.Config, store *mailindex.Store, source indexSyncSource, req IndexSyncRequest) (*IndexSyncResult, error) {
	if store == nil {
		return nil, fmt.Errorf("index store is required")
	}
	if source == nil {
		source = defaultIndexSyncSource{}
	}
	accountID := firstNonEmpty(strings.TrimSpace(req.AccountID), strings.TrimSpace(cfg.AccountID), "default")
	folders := compactFolders(req.Folders)
	if len(folders) == 0 {
		list, err := source.ListFolders(cfg)
		if err != nil {
			return nil, err
		}
		for _, folder := range list.Folders {
			if strings.TrimSpace(folder.Path) != "" {
				folders = append(folders, folder.Path)
			}
		}
	}
	limitPerFolder := req.LimitPerFolder
	if limitPerFolder <= 0 {
		limitPerFolder = 200
	}
	view := "SUMMARY"
	if req.FullBodies {
		view = "FULL"
	}
	result := &IndexSyncResult{IndexPath: req.IndexPath, AccountID: accountID}
	for _, folder := range folders {
		folderResult := IndexSyncFolderResult{Folder: folder}
		cursor := ""
		for folderResult.IndexedMessages < limitPerFolder {
			pageLimit := 200
			remaining := limitPerFolder - folderResult.IndexedMessages
			if remaining < pageLimit {
				pageLimit = remaining
			}
			page, err := source.ListEmailsV2(cfg, &ListEmailsV2Request{
				Folder: folder,
				Limit:  pageLimit,
				Cursor: cursor,
				Sort:   "uid_asc",
				View:   view,
			})
			if err != nil {
				folderResult.Error = err.Error()
				break
			}
			indexMessages := make([]mailindex.Message, 0, len(page.Items))
			for _, item := range page.Items {
				item.Folder = folder
				indexMessages = append(indexMessages, mailMessageToIndexMessage(accountID, item))
			}
			if len(indexMessages) > 0 {
				if err := store.UpsertMessages(ctx, indexMessages); err != nil {
					folderResult.Error = err.Error()
					break
				}
			}
			folderResult.IndexedMessages += len(indexMessages)
			result.IndexedMessages += len(indexMessages)
			if !page.HasMore || page.NextCursor == "" || len(indexMessages) == 0 {
				break
			}
			cursor = page.NextCursor
		}
		result.Folders = append(result.Folders, folderResult)
	}
	return result, nil
}

func mailMessageToIndexMessage(accountID string, item MailMessage) mailindex.Message {
	return mailindex.Message{
		AccountID:       accountID,
		Folder:          item.Folder,
		UID:             item.UID,
		Subject:         item.Subject,
		From:            item.From,
		To:              append([]string(nil), item.To...),
		CC:              append([]string(nil), item.CC...),
		SentDate:        item.SentDate,
		Read:            item.Read,
		HasAttachment:   item.HasAttachment,
		AttachmentNames: append([]string(nil), item.AttachmentNames...),
		TextBody:        firstNonEmpty(item.TextBody, item.TextContent),
		HTMLBody:        firstNonEmpty(item.HTMLBody, item.HTMLContent),
		Summary:         item.Summary,
		MessageID:       item.MessageID,
		InReplyTo:       item.InReplyTo,
		References:      append([]string(nil), item.References...),
	}
}

func compactFolders(values []string) []string {
	result := make([]string, 0, len(values))
	seen := make(map[string]bool)
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		result = append(result, value)
	}
	return result
}
