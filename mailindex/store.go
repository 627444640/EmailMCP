// Package mailindex provides a local SQLite-backed mailbox index.
package mailindex

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

const (
	defaultLimit = 50
	maxLimit     = 200
	cursorVer    = 1
	defaultAcct  = "default"
)

// Message is the indexed representation of one mailbox message.
type Message struct {
	AccountID       string   `json:"accountId"`
	Folder          string   `json:"folder"`
	UID             uint32   `json:"uid"`
	Subject         string   `json:"subject"`
	From            string   `json:"from"`
	To              []string `json:"to,omitempty"`
	CC              []string `json:"cc,omitempty"`
	SentDate        string   `json:"sentDate"`
	Read            bool     `json:"read"`
	HasAttachment   bool     `json:"hasAttachment"`
	AttachmentNames []string `json:"attachmentNames,omitempty"`
	TextBody        string   `json:"textBody,omitempty"`
	HTMLBody        string   `json:"htmlBody,omitempty"`
	Summary         string   `json:"summary,omitempty"`
	MessageID       string   `json:"messageId,omitempty"`
	InReplyTo       string   `json:"inReplyTo,omitempty"`
	References      []string `json:"references,omitempty"`
	MatchedFields   []string `json:"matchedFields,omitempty"`
	DateMissing     bool     `json:"dateMissing,omitempty"`
}

// Query describes a local index search.
type Query struct {
	AccountID  string `json:"accountId"`
	Folder     string `json:"folder"`
	Limit      int    `json:"limit"`
	Cursor     string `json:"cursor"`
	Keyword    string `json:"keyword"`
	From       string `json:"from"`
	Subject    string `json:"subject"`
	UnreadOnly *bool  `json:"unreadOnly"`
	Since      string `json:"since"`
	Before     string `json:"before"`
	Sort       string `json:"sort"`
	View       string `json:"view"`
}

// Result is returned by Search.
type Result struct {
	Items      []Message `json:"items"`
	NextCursor string    `json:"nextCursor"`
	HasMore    bool      `json:"hasMore"`
	Total      *int      `json:"total,omitempty"`
}

// Status reports the local index state.
type Status struct {
	Path         string `json:"path"`
	Initialized  bool   `json:"initialized"`
	FTSEnabled   bool   `json:"ftsEnabled"`
	MessageCount int    `json:"messageCount"`
}

// Store owns a SQLite connection for the local mailbox index.
type Store struct {
	db   *sql.DB
	path string
}

type normalizedQuery struct {
	accountID   string
	folder      string
	limit       int
	cursor      string
	keyword     string
	from        string
	subject     string
	unreadOnly  *bool
	since       time.Time
	before      time.Time
	hasSince    bool
	hasBefore   bool
	sort        string
	view        string
	fingerprint string
}

type pageCursor struct {
	Version     int    `json:"v"`
	Fingerprint string `json:"fp"`
	Sort        string `json:"sort"`
	LastFolder  string `json:"lastFolder"`
	LastUID     uint32 `json:"lastUid"`
	LastDate    string `json:"lastDate,omitempty"`
}

// Open opens or creates a SQLite mailbox index at path.
func Open(path string) (*Store, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil, fmt.Errorf("index path is required")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	return &Store{db: db, path: path}, nil
}

// Close closes the underlying SQLite connection.
func (s *Store) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

// Init creates the index schema.
func (s *Store) Init(ctx context.Context) error {
	if s == nil || s.db == nil {
		return fmt.Errorf("store is not open")
	}
	statements := []string{
		`PRAGMA journal_mode=WAL`,
		`CREATE TABLE IF NOT EXISTS messages (
			account_id TEXT NOT NULL,
			folder TEXT NOT NULL,
			uid INTEGER NOT NULL,
			subject TEXT NOT NULL DEFAULT '',
			from_addr TEXT NOT NULL DEFAULT '',
			to_json TEXT NOT NULL DEFAULT '[]',
			cc_json TEXT NOT NULL DEFAULT '[]',
			sent_date TEXT NOT NULL DEFAULT '',
			sent_unix INTEGER,
			read INTEGER NOT NULL DEFAULT 0,
			has_attachment INTEGER NOT NULL DEFAULT 0,
			attachment_names_json TEXT NOT NULL DEFAULT '[]',
			text_body TEXT NOT NULL DEFAULT '',
			html_body TEXT NOT NULL DEFAULT '',
			summary TEXT NOT NULL DEFAULT '',
			message_id TEXT NOT NULL DEFAULT '',
			in_reply_to TEXT NOT NULL DEFAULT '',
			references_json TEXT NOT NULL DEFAULT '[]',
			updated_at TEXT NOT NULL,
			PRIMARY KEY (account_id, folder, uid)
		)`,
		`CREATE INDEX IF NOT EXISTS messages_account_folder_date_idx ON messages(account_id, folder, sent_unix, uid)`,
		`CREATE VIRTUAL TABLE IF NOT EXISTS messages_fts USING fts5(
			account_id UNINDEXED,
			folder UNINDEXED,
			uid UNINDEXED,
			subject,
			from_addr,
			text_body,
			html_body,
			summary
		)`,
	}
	for _, statement := range statements {
		if _, err := s.db.ExecContext(ctx, statement); err != nil {
			return err
		}
	}
	return nil
}

// UpsertMessages inserts or updates indexed messages and keeps the FTS table in sync.
func (s *Store) UpsertMessages(ctx context.Context, messages []Message) error {
	if s == nil || s.db == nil {
		return fmt.Errorf("store is not open")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	upsert, err := tx.PrepareContext(ctx, `INSERT INTO messages (
		account_id, folder, uid, subject, from_addr, to_json, cc_json, sent_date, sent_unix,
		read, has_attachment, attachment_names_json, text_body, html_body, summary,
		message_id, in_reply_to, references_json, updated_at
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	ON CONFLICT(account_id, folder, uid) DO UPDATE SET
		subject=excluded.subject,
		from_addr=excluded.from_addr,
		to_json=excluded.to_json,
		cc_json=excluded.cc_json,
		sent_date=excluded.sent_date,
		sent_unix=excluded.sent_unix,
		read=excluded.read,
		has_attachment=excluded.has_attachment,
		attachment_names_json=excluded.attachment_names_json,
		text_body=excluded.text_body,
		html_body=excluded.html_body,
		summary=excluded.summary,
		message_id=excluded.message_id,
		in_reply_to=excluded.in_reply_to,
		references_json=excluded.references_json,
		updated_at=excluded.updated_at`)
	if err != nil {
		return err
	}
	defer upsert.Close()

	for _, msg := range messages {
		msg = normalizeMessage(msg)
		if msg.Folder == "" || msg.UID == 0 {
			return fmt.Errorf("folder and uid are required")
		}
		sentUnix := any(nil)
		if sentAt, ok := parseTime(msg.SentDate); ok {
			sentUnix = sentAt.Unix()
		}
		toJSON, _ := json.Marshal(msg.To)
		ccJSON, _ := json.Marshal(msg.CC)
		attachmentJSON, _ := json.Marshal(msg.AttachmentNames)
		referencesJSON, _ := json.Marshal(msg.References)
		if _, err := upsert.ExecContext(ctx,
			msg.AccountID,
			msg.Folder,
			msg.UID,
			msg.Subject,
			msg.From,
			string(toJSON),
			string(ccJSON),
			msg.SentDate,
			sentUnix,
			boolInt(msg.Read),
			boolInt(msg.HasAttachment),
			string(attachmentJSON),
			msg.TextBody,
			msg.HTMLBody,
			msg.Summary,
			msg.MessageID,
			msg.InReplyTo,
			string(referencesJSON),
			time.Now().UTC().Format(time.RFC3339Nano),
		); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM messages_fts WHERE account_id = ? AND folder = ? AND uid = ?`, msg.AccountID, msg.Folder, msg.UID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO messages_fts(account_id, folder, uid, subject, from_addr, text_body, html_body, summary) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
			msg.AccountID, msg.Folder, msg.UID, msg.Subject, msg.From, msg.TextBody, msg.HTMLBody, msg.Summary); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// Search queries the local index with stable cursor pagination.
func (s *Store) Search(ctx context.Context, req Query) (Result, error) {
	if s == nil || s.db == nil {
		return Result{}, fmt.Errorf("store is not open")
	}
	norm, err := normalizeQuery(req)
	if err != nil {
		return Result{}, err
	}
	items, err := s.loadMessages(ctx, norm)
	if err != nil {
		return Result{}, err
	}
	filtered := filterMessages(items, norm)
	sortMessages(filtered, norm.sort)
	if norm.cursor != "" {
		cursor, err := decodeCursor(norm.cursor)
		if err != nil {
			return Result{}, err
		}
		if cursor.Fingerprint != norm.fingerprint || cursor.Sort != norm.sort {
			return Result{}, fmt.Errorf("cursor does not match current query")
		}
		filtered = filtered[startAfterCursor(filtered, cursor, norm.sort):]
	}

	total := len(filtered)
	hasMore := len(filtered) > norm.limit
	pageItems := filtered
	if hasMore {
		pageItems = filtered[:norm.limit]
	}
	if norm.view == "SUMMARY" {
		for i := range pageItems {
			pageItems[i].TextBody = ""
			pageItems[i].HTMLBody = ""
		}
	}
	result := Result{Items: pageItems, HasMore: hasMore, Total: &total}
	if hasMore && len(pageItems) > 0 {
		last := pageItems[len(pageItems)-1]
		result.NextCursor = encodeCursor(pageCursor{
			Version:     cursorVer,
			Fingerprint: norm.fingerprint,
			Sort:        norm.sort,
			LastFolder:  last.Folder,
			LastUID:     last.UID,
			LastDate:    last.SentDate,
		})
	}
	return result, nil
}

// Status reports whether the local index schema exists and how many messages are indexed.
func (s *Store) Status(ctx context.Context) (Status, error) {
	if s == nil || s.db == nil {
		return Status{}, fmt.Errorf("store is not open")
	}
	status := Status{Path: s.path}
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_master WHERE type IN ('table', 'virtual table') AND name = 'messages'`).Scan(&status.MessageCount); err != nil {
		return Status{}, err
	}
	status.Initialized = status.MessageCount > 0
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_master WHERE name = 'messages_fts'`).Scan(&status.MessageCount); err != nil {
		return Status{}, err
	}
	status.FTSEnabled = status.MessageCount > 0
	count := 0
	if status.Initialized {
		if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM messages`).Scan(&count); err != nil {
			return Status{}, err
		}
	}
	status.MessageCount = count
	return status, nil
}

func (s *Store) loadMessages(ctx context.Context, norm normalizedQuery) ([]Message, error) {
	clauses := []string{"account_id = ?"}
	args := []any{norm.accountID}
	if norm.folder != "" {
		clauses = append(clauses, "folder = ?")
		args = append(args, norm.folder)
	}
	if norm.unreadOnly != nil && *norm.unreadOnly {
		clauses = append(clauses, "read = 0")
	}
	if norm.hasSince {
		clauses = append(clauses, "sent_unix IS NOT NULL AND sent_unix >= ?")
		args = append(args, norm.since.Unix())
	}
	if norm.hasBefore {
		clauses = append(clauses, "sent_unix IS NOT NULL AND sent_unix < ?")
		args = append(args, norm.before.Unix())
	}
	rows, err := s.db.QueryContext(ctx, `SELECT account_id, folder, uid, subject, from_addr, to_json, cc_json, sent_date,
		read, has_attachment, attachment_names_json, text_body, html_body, summary, message_id, in_reply_to, references_json
		FROM messages WHERE `+strings.Join(clauses, " AND "), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var items []Message
	for rows.Next() {
		var msg Message
		var toJSON, ccJSON, attachmentJSON, referencesJSON string
		var read, hasAttachment int
		if err := rows.Scan(&msg.AccountID, &msg.Folder, &msg.UID, &msg.Subject, &msg.From, &toJSON, &ccJSON, &msg.SentDate, &read, &hasAttachment, &attachmentJSON, &msg.TextBody, &msg.HTMLBody, &msg.Summary, &msg.MessageID, &msg.InReplyTo, &referencesJSON); err != nil {
			return nil, err
		}
		_ = json.Unmarshal([]byte(toJSON), &msg.To)
		_ = json.Unmarshal([]byte(ccJSON), &msg.CC)
		_ = json.Unmarshal([]byte(attachmentJSON), &msg.AttachmentNames)
		_ = json.Unmarshal([]byte(referencesJSON), &msg.References)
		msg.Read = read != 0
		msg.HasAttachment = hasAttachment != 0
		if _, ok := parseTime(msg.SentDate); !ok {
			msg.DateMissing = true
		}
		items = append(items, msg)
	}
	return items, rows.Err()
}

func normalizeMessage(msg Message) Message {
	msg.AccountID = firstNonEmpty(strings.TrimSpace(msg.AccountID), defaultAcct)
	msg.Folder = strings.TrimSpace(msg.Folder)
	return msg
}

func normalizeQuery(req Query) (normalizedQuery, error) {
	norm := normalizedQuery{
		accountID: firstNonEmpty(strings.TrimSpace(req.AccountID), defaultAcct),
		folder:    strings.TrimSpace(req.Folder),
		limit:     req.Limit,
		cursor:    strings.TrimSpace(req.Cursor),
		keyword:   strings.TrimSpace(req.Keyword),
		from:      strings.TrimSpace(req.From),
		subject:   strings.TrimSpace(req.Subject),
		sort:      strings.TrimSpace(req.Sort),
		view:      strings.TrimSpace(req.View),
	}
	if req.UnreadOnly != nil {
		v := *req.UnreadOnly
		norm.unreadOnly = &v
	}
	if norm.limit <= 0 {
		norm.limit = defaultLimit
	}
	if norm.limit > maxLimit {
		norm.limit = maxLimit
	}
	if norm.sort == "" {
		norm.sort = "date_desc"
	}
	switch norm.sort {
	case "date_desc", "date_asc", "uid_desc", "uid_asc":
	default:
		return normalizedQuery{}, fmt.Errorf("unsupported sort: %s", norm.sort)
	}
	if norm.view == "" {
		norm.view = "SUMMARY"
	}
	norm.view = strings.ToUpper(norm.view)
	switch norm.view {
	case "SUMMARY", "FULL":
	default:
		return normalizedQuery{}, fmt.Errorf("unsupported view: %s", norm.view)
	}
	if strings.TrimSpace(req.Since) != "" {
		t, err := time.Parse(time.RFC3339, strings.TrimSpace(req.Since))
		if err != nil {
			return normalizedQuery{}, fmt.Errorf("invalid since: %w", err)
		}
		norm.since = t
		norm.hasSince = true
	}
	if strings.TrimSpace(req.Before) != "" {
		t, err := time.Parse(time.RFC3339, strings.TrimSpace(req.Before))
		if err != nil {
			return normalizedQuery{}, fmt.Errorf("invalid before: %w", err)
		}
		norm.before = t
		norm.hasBefore = true
	}
	if norm.hasSince && norm.hasBefore && norm.before.Before(norm.since) {
		return normalizedQuery{}, fmt.Errorf("before must be after or equal to since")
	}
	norm.fingerprint = fingerprint(norm)
	return norm, nil
}

func filterMessages(items []Message, norm normalizedQuery) []Message {
	filtered := make([]Message, 0, len(items))
	for _, item := range items {
		var fields []string
		if norm.keyword != "" {
			fields = append(fields, matchedFields(item, norm.keyword)...)
			if len(fields) == 0 {
				continue
			}
		}
		if norm.from != "" {
			if !containsFold(item.From, norm.from) {
				continue
			}
			fields = appendUnique(fields, "from")
		}
		if norm.subject != "" {
			if !containsFold(item.Subject, norm.subject) {
				continue
			}
			fields = appendUnique(fields, "subject")
		}
		item.MatchedFields = fields
		filtered = append(filtered, item)
	}
	return filtered
}

func matchedFields(item Message, keyword string) []string {
	var fields []string
	if containsFold(item.Subject, keyword) {
		fields = append(fields, "subject")
	}
	if containsFold(item.From, keyword) {
		fields = append(fields, "from")
	}
	if containsFold(firstNonEmpty(item.TextBody, item.Summary), keyword) {
		fields = append(fields, "textBody")
	}
	if containsFold(item.HTMLBody, keyword) {
		fields = append(fields, "htmlBody")
	}
	return fields
}

func sortMessages(items []Message, sortMode string) {
	sort.SliceStable(items, func(i, j int) bool {
		return compareMessages(items[i], items[j], sortMode) < 0
	})
}

func compareMessages(left, right Message, sortMode string) int {
	switch sortMode {
	case "uid_asc":
		return compareUID(left, right, true)
	case "uid_desc":
		return compareUID(left, right, false)
	case "date_asc":
		if cmp := compareDate(left, right, true); cmp != 0 {
			return cmp
		}
	default:
		if cmp := compareDate(left, right, false); cmp != 0 {
			return cmp
		}
	}
	if left.Folder != right.Folder {
		if left.Folder < right.Folder {
			return -1
		}
		return 1
	}
	return compareUID(left, right, true)
}

func compareUID(left, right Message, asc bool) int {
	if left.UID == right.UID {
		return 0
	}
	if asc {
		if left.UID < right.UID {
			return -1
		}
		return 1
	}
	if left.UID > right.UID {
		return -1
	}
	return 1
}

func compareDate(left, right Message, asc bool) int {
	leftTime, leftOK := parseTime(left.SentDate)
	rightTime, rightOK := parseTime(right.SentDate)
	if leftOK && rightOK && !leftTime.Equal(rightTime) {
		if asc {
			if leftTime.Before(rightTime) {
				return -1
			}
			return 1
		}
		if leftTime.After(rightTime) {
			return -1
		}
		return 1
	}
	if leftOK != rightOK {
		if leftOK {
			return -1
		}
		return 1
	}
	return 0
}

func startAfterCursor(items []Message, cursor pageCursor, sortMode string) int {
	for i, item := range items {
		if item.Folder == cursor.LastFolder && item.UID == cursor.LastUID {
			return i + 1
		}
	}
	cursorItem := Message{Folder: cursor.LastFolder, UID: cursor.LastUID, SentDate: cursor.LastDate}
	for i, item := range items {
		if compareMessages(item, cursorItem, sortMode) > 0 {
			return i
		}
	}
	return len(items)
}

func encodeCursor(cursor pageCursor) string {
	data, _ := json.Marshal(cursor)
	return base64.RawURLEncoding.EncodeToString(data)
}

func decodeCursor(value string) (pageCursor, error) {
	data, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return pageCursor{}, fmt.Errorf("invalid cursor: %w", err)
	}
	var cursor pageCursor
	if err := json.Unmarshal(data, &cursor); err != nil {
		return pageCursor{}, fmt.Errorf("invalid cursor: %w", err)
	}
	if cursor.Version != cursorVer || cursor.LastUID == 0 {
		return pageCursor{}, fmt.Errorf("unsupported cursor")
	}
	return cursor, nil
}

func fingerprint(norm normalizedQuery) string {
	payload := map[string]any{
		"accountId": norm.accountID,
		"folder":    norm.folder,
		"keyword":   strings.ToLower(norm.keyword),
		"from":      strings.ToLower(norm.from),
		"subject":   strings.ToLower(norm.subject),
		"sort":      norm.sort,
		"view":      norm.view,
	}
	if norm.unreadOnly != nil {
		payload["unreadOnly"] = *norm.unreadOnly
	}
	if norm.hasSince {
		payload["since"] = norm.since.Format(time.RFC3339Nano)
	}
	if norm.hasBefore {
		payload["before"] = norm.before.Format(time.RFC3339Nano)
	}
	data, _ := json.Marshal(payload)
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func parseTime(value string) (time.Time, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return time.Time{}, false
	}
	t, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return time.Time{}, false
	}
	return t, true
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

func containsFold(value, needle string) bool {
	return strings.Contains(strings.ToLower(value), strings.ToLower(needle))
}

func appendUnique(values []string, value string) []string {
	for _, existing := range values {
		if existing == value {
			return values
		}
	}
	return append(values, value)
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
