package service

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"
	"sync"
	"time"

	"email-mcp-service/config"

	"github.com/emersion/go-imap"
)

// BulkMailboxRequest describes a bulk mailbox write operation.
type BulkMailboxRequest struct {
	Folder        string   `json:"folder"`
	TargetFolder  string   `json:"targetFolder,omitempty"`
	TrashFolder   string   `json:"trashFolder,omitempty"`
	ArchiveFolder string   `json:"archiveFolder,omitempty"`
	UIDs          []uint32 `json:"uids"`
	Read          *bool    `json:"read,omitempty"`
	DryRun        *bool    `json:"dryRun,omitempty"`
}

// BulkMailboxItemResult describes one item in a bulk operation.
type BulkMailboxItemResult struct {
	UID          uint32        `json:"uid"`
	Success      bool          `json:"success"`
	Error        string        `json:"error,omitempty"`
	Action       MailboxAction `json:"action"`
	SourceFolder string        `json:"sourceFolder"`
	TargetFolder string        `json:"targetFolder,omitempty"`
	DryRun       bool          `json:"dryRun"`
}

// BulkMailboxResult contains per-message bulk operation results.
type BulkMailboxResult struct {
	Action  MailboxAction           `json:"action"`
	DryRun  bool                    `json:"dryRun"`
	Results []BulkMailboxItemResult `json:"results"`
}

// CreateFolderRequest describes a mailbox create operation.
type CreateFolderRequest struct {
	Name         string `json:"name"`
	ParentFolder string `json:"parentFolder,omitempty"`
}

// CreateFolderResult describes a created or already-existing folder.
type CreateFolderResult struct {
	Success       bool   `json:"success"`
	Name          string `json:"name"`
	Path          string `json:"path"`
	AlreadyExists bool   `json:"alreadyExists"`
}

// SpecialFolders contains normalized system folder paths.
type SpecialFolders struct {
	Inbox   string `json:"inbox,omitempty"`
	Sent    string `json:"sent,omitempty"`
	Drafts  string `json:"drafts,omitempty"`
	Trash   string `json:"trash,omitempty"`
	Junk    string `json:"junk,omitempty"`
	Archive string `json:"archive,omitempty"`
}

type OrganizeRule struct {
	RuleID          string `json:"ruleId"`
	FromContains    string `json:"fromContains,omitempty"`
	FromEquals      string `json:"fromEquals,omitempty"`
	SubjectContains string `json:"subjectContains,omitempty"`
	Keyword         string `json:"keyword,omitempty"`
	Since           string `json:"since,omitempty"`
	Before          string `json:"before,omitempty"`
	UnreadOnly      *bool  `json:"unreadOnly,omitempty"`
	TargetFolder    string `json:"targetFolder"`
	Action          string `json:"action"`
}

type OrganizeAction struct {
	Action       string `json:"action"`
	Folder       string `json:"folder"`
	TargetFolder string `json:"targetFolder,omitempty"`
	UID          uint32 `json:"uid"`
	RuleID       string `json:"ruleId,omitempty"`
}

type OrganizeMatch struct {
	Folder       string `json:"folder"`
	UID          uint32 `json:"uid"`
	Subject      string `json:"subject"`
	From         string `json:"from"`
	SentDate     string `json:"sentDate"`
	TargetFolder string `json:"targetFolder"`
	Reason       string `json:"reason"`
}

type OrganizeRulePreview struct {
	RuleID  string          `json:"ruleId"`
	Matches []OrganizeMatch `json:"matches"`
}

type OrganizePlanPreview struct {
	PlanID string                `json:"planId"`
	Rules  []OrganizeRulePreview `json:"rules"`
}

type ApplyOrganizePlanRequest struct {
	PlanID  string           `json:"planId,omitempty"`
	Actions []OrganizeAction `json:"actions,omitempty"`
	DryRun  *bool            `json:"dryRun,omitempty"`
}

type ApplyOrganizePlanResult struct {
	PlanID  string                  `json:"planId,omitempty"`
	DryRun  bool                    `json:"dryRun"`
	Results []BulkMailboxItemResult `json:"results"`
}

type OrganizePlanStore struct {
	mu    sync.Mutex
	plans map[string][]OrganizeAction
}

func NewOrganizePlanStore() *OrganizePlanStore {
	return &OrganizePlanStore{plans: make(map[string][]OrganizeAction)}
}

func (s *OrganizePlanStore) Put(actions []OrganizeAction) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	id := randomPlanID()
	s.plans[id] = append([]OrganizeAction(nil), actions...)
	return id
}

func (s *OrganizePlanStore) Get(id string) ([]OrganizeAction, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	actions, ok := s.plans[id]
	return append([]OrganizeAction(nil), actions...), ok
}

var defaultOrganizePlanStore = NewOrganizePlanStore()

func randomPlanID() string {
	buf := make([]byte, 12)
	if _, err := rand.Read(buf); err != nil {
		return "plan"
	}
	return hex.EncodeToString(buf)
}

func joinMailboxPath(name, parent string) string {
	name = strings.Trim(strings.TrimSpace(name), `/\`)
	parent = strings.Trim(strings.TrimSpace(parent), `/\`)
	if parent == "" {
		return name
	}
	return parent + "/" + name
}

func dryRunDefault(req *BulkMailboxRequest) bool {
	return req == nil || req.DryRun == nil || *req.DryRun
}

func bulkDryRun(action MailboxAction, req *BulkMailboxRequest) (*BulkMailboxResult, error) {
	if req == nil {
		req = &BulkMailboxRequest{}
	}
	folder := strings.TrimSpace(req.Folder)
	if folder == "" {
		folder = "INBOX"
	}
	target := bulkTargetFolder(action, req, SpecialFolders{})
	result := &BulkMailboxResult{Action: action, DryRun: true}
	for _, uid := range req.UIDs {
		result.Results = append(result.Results, BulkMailboxItemResult{
			UID:          uid,
			Success:      uid != 0,
			Action:       action,
			SourceFolder: folder,
			TargetFolder: target,
			DryRun:       true,
		})
		if uid == 0 {
			result.Results[len(result.Results)-1].Error = "uid 不能为空"
		}
	}
	return result, nil
}

func bulkTargetFolder(action MailboxAction, req *BulkMailboxRequest, special SpecialFolders) string {
	switch action {
	case MailboxActionDelete:
		return firstNonEmpty(req.TrashFolder, special.Trash, "Trash")
	case MailboxActionMove:
		return req.TargetFolder
	default:
		return req.TargetFolder
	}
}

func BulkMoveEmails(cfg *config.Config, req *BulkMailboxRequest) (*BulkMailboxResult, error) {
	if dryRunDefault(req) {
		return bulkDryRun(MailboxActionMove, req)
	}
	return executeBulkMailboxWrite(cfg, MailboxActionMove, req, SpecialFolders{})
}

func BulkDeleteEmails(cfg *config.Config, req *BulkMailboxRequest, special SpecialFolders) (*BulkMailboxResult, error) {
	if req == nil {
		req = &BulkMailboxRequest{}
	}
	if req.TrashFolder == "" {
		req.TrashFolder = firstNonEmpty(special.Trash, "Trash")
	}
	if dryRunDefault(req) {
		return bulkDryRun(MailboxActionDelete, req)
	}
	return executeBulkMailboxWrite(cfg, MailboxActionDelete, req, special)
}

func BulkSetEmailReadStatus(cfg *config.Config, req *BulkMailboxRequest) (*BulkMailboxResult, error) {
	return executeBulkMailboxWrite(cfg, MailboxActionSetReadStatus, req, SpecialFolders{})
}

func ArchiveEmails(cfg *config.Config, req *BulkMailboxRequest, special SpecialFolders) (*BulkMailboxResult, error) {
	if req == nil {
		req = &BulkMailboxRequest{}
	}
	req.TargetFolder = firstNonEmpty(req.ArchiveFolder, special.Archive, "Archive")
	if dryRunDefault(req) {
		return bulkDryRun(MailboxActionMove, req)
	}
	return executeBulkMailboxWrite(cfg, MailboxActionMove, req, special)
}

func executeBulkMailboxWrite(cfg *config.Config, action MailboxAction, req *BulkMailboxRequest, special SpecialFolders) (*BulkMailboxResult, error) {
	if req == nil {
		return nil, fmt.Errorf("request is required")
	}
	folder := firstNonEmpty(req.Folder, "INBOX")
	target := bulkTargetFolder(action, req, special)
	result := &BulkMailboxResult{Action: action}
	for _, uid := range req.UIDs {
		item := BulkMailboxItemResult{UID: uid, Action: action, SourceFolder: folder, TargetFolder: target}
		writeReq := &MailboxWriteRequest{Action: action, UID: uid, Folder: folder, TargetFolder: target, Read: req.Read}
		if action == MailboxActionDelete {
			writeReq.TargetFolder = firstNonEmpty(req.TrashFolder, special.Trash, "Trash")
			item.TargetFolder = writeReq.TargetFolder
		}
		_, err := executeMailboxWrite(cfg, writeReq)
		if err != nil {
			item.Error = err.Error()
		} else {
			item.Success = true
		}
		result.Results = append(result.Results, item)
	}
	return result, nil
}

func ResolveSpecialFoldersFromList(folders []MailFolder) SpecialFolders {
	var result SpecialFolders
	for _, folder := range folders {
		path := folder.Path
		name := normalizeFolderName(path)
		switch {
		case result.Inbox == "" && matchesAnyFolder(name, "inbox", "收件箱"):
			result.Inbox = path
		case result.Sent == "" && matchesAnyFolder(name, "sent", "sent messages", "已发送", "已发送邮件", "发件箱"):
			result.Sent = path
		case result.Drafts == "" && matchesAnyFolder(name, "drafts", "draft", "草稿箱", "草稿"):
			result.Drafts = path
		case result.Trash == "" && matchesAnyFolder(name, "trash", "deleted messages", "deleted items", "垃圾箱", "已删除", "废纸篓"):
			result.Trash = path
		case result.Junk == "" && matchesAnyFolder(name, "junk", "spam", "垃圾邮件", "广告邮件"):
			result.Junk = path
		case result.Archive == "" && matchesAnyFolder(name, "archive", "archives", "归档"):
			result.Archive = path
		}
	}
	return result
}

func normalizeFolderName(path string) string {
	path = strings.ReplaceAll(path, "\\", "/")
	parts := strings.Split(path, "/")
	return strings.ToLower(strings.TrimSpace(parts[len(parts)-1]))
}

func matchesAnyFolder(name string, values ...string) bool {
	for _, value := range values {
		if name == strings.ToLower(value) {
			return true
		}
	}
	return false
}

func PreviewOrganizePlanFromMessages(store *OrganizePlanStore, messages []MailMessage, rules []OrganizeRule, limitPerRule int) OrganizePlanPreview {
	if store == nil {
		store = defaultOrganizePlanStore
	}
	if limitPerRule <= 0 {
		limitPerRule = 200
	}
	var actions []OrganizeAction
	preview := OrganizePlanPreview{}
	for _, rule := range rules {
		rulePreview := OrganizeRulePreview{RuleID: rule.RuleID}
		for _, msg := range messages {
			if len(rulePreview.Matches) >= limitPerRule {
				break
			}
			reason, ok := organizeRuleMatch(msg, rule)
			if !ok {
				continue
			}
			action := OrganizeAction{Action: firstNonEmpty(rule.Action, "move"), Folder: firstNonEmpty(msg.Folder, "INBOX"), TargetFolder: rule.TargetFolder, UID: msg.UID, RuleID: rule.RuleID}
			actions = append(actions, action)
			rulePreview.Matches = append(rulePreview.Matches, OrganizeMatch{Folder: action.Folder, UID: msg.UID, Subject: msg.Subject, From: msg.From, SentDate: msg.SentDate, TargetFolder: rule.TargetFolder, Reason: reason})
		}
		preview.Rules = append(preview.Rules, rulePreview)
	}
	preview.PlanID = store.Put(actions)
	return preview
}

func organizeRuleMatch(msg MailMessage, rule OrganizeRule) (string, bool) {
	if rule.UnreadOnly != nil && *rule.UnreadOnly && msg.Read {
		return "", false
	}
	sentAt, hasDate := parseMessageTime(msg.SentDate)
	if strings.TrimSpace(rule.Since) != "" {
		since, err := time.Parse(time.RFC3339, strings.TrimSpace(rule.Since))
		if err == nil && (!hasDate || sentAt.Before(since)) {
			return "", false
		}
	}
	if strings.TrimSpace(rule.Before) != "" {
		before, err := time.Parse(time.RFC3339, strings.TrimSpace(rule.Before))
		if err == nil && (!hasDate || !sentAt.Before(before)) {
			return "", false
		}
	}
	if rule.FromEquals != "" && (strings.EqualFold(strings.TrimSpace(msg.From), strings.TrimSpace(rule.FromEquals)) || containsFold(msg.From, "<"+strings.TrimSpace(rule.FromEquals)+">")) {
		return "fromEquals", true
	}
	if rule.FromContains != "" && containsFold(msg.From, rule.FromContains) {
		return "fromContains", true
	}
	if rule.SubjectContains != "" && containsFold(msg.Subject, rule.SubjectContains) {
		return "subjectContains", true
	}
	if rule.Keyword != "" && len(keywordMatchedFields(msg, rule.Keyword)) > 0 {
		return "keyword", true
	}
	return "", false
}

func ApplyOrganizeActions(cfg *config.Config, actions []OrganizeAction, dryRun bool) ApplyOrganizePlanResult {
	result := ApplyOrganizePlanResult{DryRun: dryRun}
	for _, action := range actions {
		req := &BulkMailboxRequest{Folder: action.Folder, TargetFolder: action.TargetFolder, UIDs: []uint32{action.UID}, DryRun: &dryRun}
		var bulk *BulkMailboxResult
		var err error
		switch action.Action {
		case "delete":
			bulk, err = BulkDeleteEmails(cfg, req, SpecialFolders{})
		default:
			bulk, err = BulkMoveEmails(cfg, req)
		}
		if err != nil {
			result.Results = append(result.Results, BulkMailboxItemResult{UID: action.UID, Action: MailboxAction(action.Action), SourceFolder: action.Folder, TargetFolder: action.TargetFolder, Error: err.Error(), DryRun: dryRun})
			continue
		}
		result.Results = append(result.Results, bulk.Results...)
	}
	return result
}

func ApplyOrganizePlan(cfg *config.Config, store *OrganizePlanStore, req *ApplyOrganizePlanRequest) (*ApplyOrganizePlanResult, error) {
	if req == nil {
		req = &ApplyOrganizePlanRequest{}
	}
	if store == nil {
		store = defaultOrganizePlanStore
	}
	actions := append([]OrganizeAction(nil), req.Actions...)
	if req.PlanID != "" {
		stored, ok := store.Get(req.PlanID)
		if !ok {
			return nil, fmt.Errorf("planId not found")
		}
		actions = append(actions, stored...)
	}
	dryRun := false
	if req.DryRun != nil {
		dryRun = *req.DryRun
	}
	result := ApplyOrganizeActions(cfg, actions, dryRun)
	result.PlanID = req.PlanID
	return &result, nil
}

// CreateFolder creates a mailbox folder if it does not already exist.
func CreateFolder(cfg *config.Config, req *CreateFolderRequest) (*CreateFolderResult, error) {
	if req == nil || strings.TrimSpace(req.Name) == "" {
		return nil, fmt.Errorf("name 不能为空")
	}
	path := joinMailboxPath(req.Name, req.ParentFolder)
	folders, err := ListFolders(cfg)
	if err == nil {
		for _, folder := range folders.Folders {
			if strings.EqualFold(folder.Path, path) {
				return &CreateFolderResult{Success: true, Name: req.Name, Path: folder.Path, AlreadyExists: true}, nil
			}
		}
	}
	imapCfg, err := resolveIMAPConfig(cfg, &ReceiveMailRequest{})
	if err != nil {
		return nil, err
	}
	c, err := connectIMAP(imapCfg)
	if err != nil {
		return nil, err
	}
	defer c.Logout()
	if err := c.Create(path); err != nil {
		return nil, fmt.Errorf("创建文件夹失败(%s): %w", path, err)
	}
	return &CreateFolderResult{Success: true, Name: req.Name, Path: path}, nil
}

func ResolveSpecialFolders(cfg *config.Config) (*SpecialFolders, error) {
	folders, err := ListFolders(cfg)
	if err != nil {
		return nil, err
	}
	result := ResolveSpecialFoldersFromList(folders.Folders)
	return &result, nil
}

func seqSetFromUID(uid uint32) *imap.SeqSet {
	seqSet := new(imap.SeqSet)
	seqSet.AddNum(uid)
	return seqSet
}
