package service

import (
	"io"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/emersion/go-imap"
)

func TestListEmailFetchItemsIncludePreviewBeforeFetch(t *testing.T) {
	fetchItems, previewSection := listEmailFetchItems(true)
	if previewSection == nil {
		t.Fatal("expected preview section for summary view")
	}

	want := previewSection.FetchItem()
	for _, item := range fetchItems {
		if item == want {
			return
		}
	}

	t.Fatalf("expected fetch items to include preview section %q, got %#v", want, fetchItems)
}

func TestListEmailFetchItemsOmitPreviewForFullView(t *testing.T) {
	fetchItems, previewSection := listEmailFetchItems(false)
	if previewSection != nil {
		t.Fatalf("expected no preview section for full view, got %#v", previewSection)
	}

	for _, item := range fetchItems {
		if item != imap.FetchUid && item != imap.FetchFlags && item != imap.FetchEnvelope && item != imap.FetchBodyStructure {
			t.Fatalf("unexpected full-view fetch item %q in %#v", item, fetchItems)
		}
	}
}

func TestNormalizeListEmailsV2RequestDefaultsAndCapsLimit(t *testing.T) {
	norm, err := normalizeListEmailsV2Request(&ListEmailsV2Request{Limit: 500})
	if err != nil {
		t.Fatalf("normalizeListEmailsV2Request returned error: %v", err)
	}
	if norm.folder != "INBOX" || norm.limit != 200 || norm.sort != "date_desc" || norm.view != "SUMMARY" {
		t.Fatalf("unexpected normalized request: %#v", norm)
	}
}

func TestNormalizeListEmailsV2RequestParsesRFC3339Range(t *testing.T) {
	norm, err := normalizeListEmailsV2Request(&ListEmailsV2Request{
		Since:  "2026-06-01T00:00:00+08:00",
		Before: "2026-07-01T00:00:00+08:00",
	})
	if err != nil {
		t.Fatalf("normalizeListEmailsV2Request returned error: %v", err)
	}
	if !norm.hasSince || !norm.hasBefore {
		t.Fatalf("expected parsed range flags: %#v", norm)
	}
	if got := norm.since.Format(time.RFC3339); got != "2026-06-01T00:00:00+08:00" {
		t.Fatalf("unexpected since: %s", got)
	}
	if got := norm.before.Format(time.RFC3339); got != "2026-07-01T00:00:00+08:00" {
		t.Fatalf("unexpected before: %s", got)
	}
}

func TestFilterListEmailsV2MatchesKeywordFieldsAndDateRange(t *testing.T) {
	norm, err := normalizeListEmailsV2Request(&ListEmailsV2Request{
		Keyword: "发票",
		Since:   "2026-06-01T00:00:00+08:00",
		Before:  "2026-07-01T00:00:00+08:00",
	})
	if err != nil {
		t.Fatalf("normalizeListEmailsV2Request returned error: %v", err)
	}
	items := []MailMessage{
		{UID: 1, Subject: "腾讯云电子发票已开具", SentDate: "2026-06-10T01:51:19+08:00"},
		{UID: 2, Subject: "普通通知", TextBody: "这是一张发票", SentDate: "2026-06-15T09:00:00+08:00"},
		{UID: 3, Subject: "腾讯云电子发票已开具", SentDate: "2026-07-01T00:00:00+08:00"},
		{UID: 4, Subject: "腾讯云电子发票已开具"},
	}

	filtered := filterListEmailsV2(items, norm)
	if len(filtered) != 2 {
		t.Fatalf("expected 2 filtered items, got %#v", filtered)
	}
	if !reflect.DeepEqual(filtered[0].MatchedFields, []string{"subject"}) {
		t.Fatalf("unexpected matched fields for subject hit: %#v", filtered[0].MatchedFields)
	}
	if !reflect.DeepEqual(filtered[1].MatchedFields, []string{"textBody"}) {
		t.Fatalf("unexpected matched fields for body hit: %#v", filtered[1].MatchedFields)
	}
}

func TestFilterListEmailsV2MatchesInvoiceCaseInsensitive(t *testing.T) {
	norm, err := normalizeListEmailsV2Request(&ListEmailsV2Request{Keyword: "invoice"})
	if err != nil {
		t.Fatalf("normalizeListEmailsV2Request returned error: %v", err)
	}
	filtered := filterListEmailsV2([]MailMessage{
		{UID: 1, Subject: "Monthly INVOICE"},
		{UID: 2, HTMLBody: "<p>Receipt</p>"},
	}, norm)
	if len(filtered) != 1 || filtered[0].UID != 1 {
		t.Fatalf("expected case-insensitive invoice match, got %#v", filtered)
	}
}

func TestPaginateListEmailsV2UsesOpaqueCursorWithoutDuplicates(t *testing.T) {
	norm, err := normalizeListEmailsV2Request(&ListEmailsV2Request{Limit: 2, Sort: "uid_asc"})
	if err != nil {
		t.Fatalf("normalizeListEmailsV2Request returned error: %v", err)
	}
	items := []MailMessage{{UID: 3}, {UID: 1}, {UID: 2}, {UID: 4}, {UID: 5}}

	page1, err := paginateListEmailsV2(items, norm)
	if err != nil {
		t.Fatalf("paginateListEmailsV2 page1 returned error: %v", err)
	}
	if got := uidsOf(page1.Items); !reflect.DeepEqual(got, []uint32{1, 2}) {
		t.Fatalf("unexpected page1 UIDs: %#v", got)
	}
	if !page1.HasMore || page1.NextCursor == "" {
		t.Fatalf("expected next cursor: %#v", page1)
	}

	norm.cursor = page1.NextCursor
	page2, err := paginateListEmailsV2(items, norm)
	if err != nil {
		t.Fatalf("paginateListEmailsV2 page2 returned error: %v", err)
	}
	if got := uidsOf(page2.Items); !reflect.DeepEqual(got, []uint32{3, 4}) {
		t.Fatalf("unexpected page2 UIDs: %#v", got)
	}
}

func TestPaginateListEmailsV2RejectsCursorForDifferentQuery(t *testing.T) {
	norm, err := normalizeListEmailsV2Request(&ListEmailsV2Request{Limit: 1, Keyword: "invoice"})
	if err != nil {
		t.Fatalf("normalizeListEmailsV2Request returned error: %v", err)
	}
	page, err := paginateListEmailsV2([]MailMessage{{UID: 1}, {UID: 2}}, norm)
	if err != nil {
		t.Fatalf("paginateListEmailsV2 returned error: %v", err)
	}

	other, err := normalizeListEmailsV2Request(&ListEmailsV2Request{Limit: 1, Keyword: "发票", Cursor: page.NextCursor})
	if err != nil {
		t.Fatalf("normalizeListEmailsV2Request other returned error: %v", err)
	}
	if _, err := paginateListEmailsV2([]MailMessage{{UID: 1}, {UID: 2}}, other); err == nil {
		t.Fatal("expected cursor fingerprint mismatch error")
	}
}

func TestAttachmentMetadataFromBodyStructure(t *testing.T) {
	metadata := attachmentMetadata(&imap.BodyStructure{
		MIMEType:    "multipart",
		MIMESubType: "mixed",
		Parts: []*imap.BodyStructure{
			{
				MIMEType:          "application",
				MIMESubType:       "pdf",
				Size:              123456,
				DispositionParams: map[string]string{"filename": "invoice.pdf"},
			},
		},
	})
	want := []EmailAttachment{{Name: "invoice.pdf", ContentType: "application/pdf", Size: 123456}}
	if !reflect.DeepEqual(metadata, want) {
		t.Fatalf("unexpected attachment metadata\nwant: %#v\n got: %#v", want, metadata)
	}
}

func TestReadBodyLiteralMatchesEquivalentSection(t *testing.T) {
	requested := &imap.BodySectionName{BodyPartName: imap.BodyPartName{Path: []int{1}}}
	responded, err := imap.ParseBodySectionName(requested.FetchItem())
	if err != nil {
		t.Fatalf("ParseBodySectionName returned error: %v", err)
	}
	msg := &imap.Message{
		Body: map[*imap.BodySectionName]imap.Literal{
			responded: strings.NewReader("reply body"),
		},
	}

	got := readBodyLiteral(msg, requested)

	data, err := io.ReadAll(got)
	if err != nil {
		t.Fatalf("ReadAll returned error: %v", err)
	}
	if string(data) != "reply body" {
		t.Fatalf("unexpected body %q", string(data))
	}
}

func TestDecodeBodyPartContentDecodesBase64(t *testing.T) {
	part := &imap.BodyStructure{
		MIMEType:    "text",
		MIMESubType: "plain",
		Encoding:    "base64",
		Params:      map[string]string{"charset": "UTF-8"},
	}

	got := decodeBodyPartContent([]byte("5L2g5aW977yM5rex5Zyz"), part)

	if got != "你好，深圳" {
		t.Fatalf("unexpected decoded body %q", got)
	}
}

func TestMarkAsReadRequestedDefaultsFalse(t *testing.T) {
	if markAsReadRequested(&ReceiveMailRequest{}) {
		t.Fatal("markAsRead should default to false")
	}
	enabled := true
	if !markAsReadRequested(&ReceiveMailRequest{MarkAsRead: &enabled}) {
		t.Fatal("explicit markAsRead=true should be honored")
	}
	disabled := false
	if markAsReadRequested(&ReceiveMailRequest{MarkAsRead: &disabled}) {
		t.Fatal("explicit markAsRead=false should be honored")
	}
}

func uidsOf(items []MailMessage) []uint32 {
	uids := make([]uint32, len(items))
	for i, item := range items {
		uids[i] = item.UID
	}
	return uids
}
