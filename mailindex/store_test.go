package mailindex

import (
	"context"
	"path/filepath"
	"testing"
)

func TestStoreSearchSupportsKeywordTimeFolderAndPagination(t *testing.T) {
	ctx := context.Background()
	store, err := Open(filepath.Join(t.TempDir(), "mail.db"))
	if err != nil {
		t.Fatalf("Open returned error: %v", err)
	}
	defer store.Close()
	if err := store.Init(ctx); err != nil {
		t.Fatalf("Init returned error: %v", err)
	}

	messages := []Message{
		{
			AccountID:       "default",
			Folder:          "INBOX",
			UID:             10,
			Subject:         "腾讯云电子发票已开具",
			From:            "腾讯云 <cloud_noreply@tencent.com>",
			SentDate:        "2026-06-10T01:51:19+08:00",
			Read:            true,
			HasAttachment:   true,
			AttachmentNames: []string{"fapiao.pdf"},
			TextBody:        "您的 invoice 和电子发票已经开具",
		},
		{
			AccountID: "default",
			Folder:    "INBOX",
			UID:       11,
			Subject:   "June Invoice",
			From:      "billing@example.com",
			SentDate:  "2026-06-12T08:00:00+08:00",
			Read:      false,
			TextBody:  "monthly receipt",
		},
		{
			AccountID: "default",
			Folder:    "Archive",
			UID:       10,
			Subject:   "归档发票",
			From:      "archive@example.com",
			SentDate:  "2026-06-13T08:00:00+08:00",
			Read:      true,
			TextBody:  "跨文件夹同 UID 不应冲突",
		},
		{
			AccountID: "default",
			Folder:    "INBOX",
			UID:       12,
			Subject:   "验证码",
			From:      "notice@example.com",
			SentDate:  "2026-05-31T23:59:00+08:00",
			Read:      false,
			TextBody:  "非六月邮件",
		},
	}
	if err := store.UpsertMessages(ctx, messages); err != nil {
		t.Fatalf("UpsertMessages returned error: %v", err)
	}

	firstPage, err := store.Search(ctx, Query{
		AccountID: "default",
		Folder:    "INBOX",
		Keyword:   "invoice",
		Since:     "2026-06-01T00:00:00+08:00",
		Before:    "2026-07-01T00:00:00+08:00",
		Sort:      "uid_asc",
		Limit:     1,
	})
	if err != nil {
		t.Fatalf("Search first page returned error: %v", err)
	}
	if len(firstPage.Items) != 1 || firstPage.Items[0].UID != 10 {
		t.Fatalf("first page items = %+v, want UID 10", firstPage.Items)
	}
	if !firstPage.HasMore || firstPage.NextCursor == "" {
		t.Fatalf("first page should expose next cursor: %+v", firstPage)
	}
	if !contains(firstPage.Items[0].MatchedFields, "textBody") {
		t.Fatalf("matched fields = %v, want textBody", firstPage.Items[0].MatchedFields)
	}

	secondPage, err := store.Search(ctx, Query{
		AccountID: "default",
		Folder:    "INBOX",
		Keyword:   "INVOICE",
		Since:     "2026-06-01T00:00:00+08:00",
		Before:    "2026-07-01T00:00:00+08:00",
		Sort:      "uid_asc",
		Limit:     1,
		Cursor:    firstPage.NextCursor,
	})
	if err != nil {
		t.Fatalf("Search second page returned error: %v", err)
	}
	if len(secondPage.Items) != 1 || secondPage.Items[0].UID != 11 {
		t.Fatalf("second page items = %+v, want UID 11", secondPage.Items)
	}
	if secondPage.HasMore {
		t.Fatalf("second page should be final: %+v", secondPage)
	}

	chinese, err := store.Search(ctx, Query{
		AccountID: "default",
		Keyword:   "发票",
		Sort:      "date_desc",
		Limit:     10,
	})
	if err != nil {
		t.Fatalf("Search Chinese keyword returned error: %v", err)
	}
	if len(chinese.Items) != 2 {
		t.Fatalf("Chinese keyword item count = %d, want 2: %+v", len(chinese.Items), chinese.Items)
	}
	if chinese.Items[0].Folder != "Archive" || chinese.Items[0].UID != 10 {
		t.Fatalf("first Chinese result = %+v, want Archive UID 10", chinese.Items[0])
	}

	status, err := store.Status(ctx)
	if err != nil {
		t.Fatalf("Status returned error: %v", err)
	}
	if status.MessageCount != 4 || !status.FTSEnabled {
		t.Fatalf("status = %+v, want 4 messages and FTS enabled", status)
	}
}

func contains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
