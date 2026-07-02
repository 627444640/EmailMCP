package service

import (
	"reflect"
	"testing"
)

func TestValidateMailboxWriteRequestRequiresUIDAndFolderForMove(t *testing.T) {
	if err := validateMailboxWriteRequest(&MailboxWriteRequest{Action: MailboxActionMove, UID: 0, Folder: "INBOX", TargetFolder: "Archive"}); err == nil {
		t.Fatalf("expected missing UID to fail")
	}
	if err := validateMailboxWriteRequest(&MailboxWriteRequest{Action: MailboxActionMove, UID: 10, Folder: "INBOX"}); err == nil {
		t.Fatalf("expected missing target folder to fail")
	}
	if err := validateMailboxWriteRequest(&MailboxWriteRequest{Action: MailboxActionMove, UID: 10, Folder: "INBOX", TargetFolder: "Archive"}); err != nil {
		t.Fatalf("expected valid move request, got %v", err)
	}
}

func TestValidateMailboxWriteRequestRejectsHardDelete(t *testing.T) {
	err := validateMailboxWriteRequest(&MailboxWriteRequest{Action: MailboxActionDelete, UID: 10, Folder: "INBOX", HardDelete: true})
	if err == nil {
		t.Fatalf("expected hard delete to be rejected in v1")
	}
}

func TestValidateMailboxWriteRequestAllowsReadStatus(t *testing.T) {
	seen := true
	req := &MailboxWriteRequest{Action: MailboxActionSetReadStatus, UID: 10, Folder: "INBOX", Read: &seen}
	if err := validateMailboxWriteRequest(req); err != nil {
		t.Fatalf("expected valid read-status request, got %v", err)
	}
}

func TestJoinMailboxPathUsesParentFolder(t *testing.T) {
	if got := joinMailboxPath("云服务", "归档"); got != "归档/云服务" {
		t.Fatalf("unexpected joined mailbox path: %q", got)
	}
	if got := joinMailboxPath("营销广告", ""); got != "营销广告" {
		t.Fatalf("unexpected root mailbox path: %q", got)
	}
}

func TestBulkDryRunBuildsPerUIDResults(t *testing.T) {
	req := &BulkMailboxRequest{
		Folder:       "INBOX",
		TargetFolder: "Archive",
		UIDs:         []uint32{3, 5},
		DryRun:       boolPtr(true),
	}
	result, err := bulkDryRun(MailboxActionMove, req)
	if err != nil {
		t.Fatalf("bulkDryRun returned error: %v", err)
	}
	if !result.DryRun || len(result.Results) != 2 {
		t.Fatalf("unexpected dry run result: %#v", result)
	}
	if result.Results[0].UID != 3 || result.Results[0].TargetFolder != "Archive" || !result.Results[0].DryRun {
		t.Fatalf("unexpected first item: %#v", result.Results[0])
	}
}

func TestBulkDeleteDefaultTrashFolder(t *testing.T) {
	req := &BulkMailboxRequest{Folder: "INBOX", UIDs: []uint32{7}, DryRun: boolPtr(true)}
	result, err := bulkDryRun(MailboxActionDelete, req)
	if err != nil {
		t.Fatalf("bulkDryRun returned error: %v", err)
	}
	if result.Results[0].TargetFolder != "Trash" {
		t.Fatalf("expected Trash fallback, got %#v", result.Results[0])
	}
}

func TestResolveSpecialFoldersFromCommonNames(t *testing.T) {
	folders := []MailFolder{
		{Path: "INBOX"},
		{Path: "Sent Messages"},
		{Path: "Deleted Messages"},
		{Path: "Junk"},
		{Path: "Archive"},
		{Path: "草稿箱"},
	}
	got := ResolveSpecialFoldersFromList(folders)
	want := SpecialFolders{
		Inbox:   "INBOX",
		Sent:    "Sent Messages",
		Drafts:  "草稿箱",
		Trash:   "Deleted Messages",
		Junk:    "Junk",
		Archive: "Archive",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected special folders\nwant: %#v\n got: %#v", want, got)
	}
}

func TestPreviewOrganizePlanBuildsActionsAndStoresPlan(t *testing.T) {
	store := NewOrganizePlanStore()
	mails := []MailMessage{
		{UID: 10, Folder: "INBOX", From: "cloud_noreply@tencent.com", Subject: "云服务通知"},
		{UID: 11, Folder: "INBOX", From: "noreply@example.com", Subject: "验证码 1234"},
	}
	result := PreviewOrganizePlanFromMessages(store, mails, []OrganizeRule{
		{RuleID: "cloud", FromEquals: "cloud_noreply@tencent.com", TargetFolder: "归档/云服务", Action: "move"},
		{RuleID: "code", SubjectContains: "验证码", TargetFolder: "归档/验证码", Action: "move"},
	}, 10)
	if result.PlanID == "" || len(result.Rules) != 2 {
		t.Fatalf("unexpected preview result: %#v", result)
	}
	actions, ok := store.Get(result.PlanID)
	if !ok || len(actions) != 2 {
		t.Fatalf("expected stored actions, ok=%t actions=%#v", ok, actions)
	}
}

func boolPtr(v bool) *bool {
	return &v
}
