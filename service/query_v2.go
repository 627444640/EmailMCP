package service

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"
)

const (
	defaultListEmailsV2Limit = 50
	maxListEmailsV2Limit     = 200
	listEmailsV2CursorVer    = 1
)

// ListEmailsV2Request is the stable, paginated email listing request.
type ListEmailsV2Request struct {
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

// ListEmailsV2Result is returned by listEmailsV2.
type ListEmailsV2Result struct {
	Items      []MailMessage `json:"items"`
	NextCursor string        `json:"nextCursor"`
	HasMore    bool          `json:"hasMore"`
	Total      *int          `json:"total"`
}

type normalizedListEmailsV2Request struct {
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

type listEmailsV2Cursor struct {
	Version     int    `json:"v"`
	Fingerprint string `json:"fp"`
	Sort        string `json:"sort"`
	LastUID     uint32 `json:"lastUid"`
	LastDate    string `json:"lastDate,omitempty"`
}

func normalizeListEmailsV2Request(req *ListEmailsV2Request) (normalizedListEmailsV2Request, error) {
	if req == nil {
		req = &ListEmailsV2Request{}
	}
	norm := normalizedListEmailsV2Request{
		folder:  strings.TrimSpace(req.Folder),
		limit:   req.Limit,
		cursor:  strings.TrimSpace(req.Cursor),
		keyword: strings.TrimSpace(req.Keyword),
		from:    strings.TrimSpace(req.From),
		subject: strings.TrimSpace(req.Subject),
		sort:    strings.TrimSpace(req.Sort),
		view:    strings.TrimSpace(req.View),
	}
	if norm.folder == "" {
		norm.folder = "INBOX"
	}
	if norm.limit <= 0 {
		norm.limit = defaultListEmailsV2Limit
	}
	if norm.limit > maxListEmailsV2Limit {
		norm.limit = maxListEmailsV2Limit
	}
	if req.UnreadOnly != nil {
		v := *req.UnreadOnly
		norm.unreadOnly = &v
	}
	if norm.sort == "" {
		norm.sort = "date_desc"
	}
	switch norm.sort {
	case "date_desc", "date_asc", "uid_desc", "uid_asc":
	default:
		return normalizedListEmailsV2Request{}, fmt.Errorf("unsupported sort: %s", norm.sort)
	}
	if norm.view == "" {
		norm.view = "SUMMARY"
	}
	norm.view = strings.ToUpper(norm.view)
	switch norm.view {
	case "SUMMARY", "FULL":
	default:
		return normalizedListEmailsV2Request{}, fmt.Errorf("unsupported view: %s", norm.view)
	}
	if strings.TrimSpace(req.Since) != "" {
		t, err := time.Parse(time.RFC3339, strings.TrimSpace(req.Since))
		if err != nil {
			return normalizedListEmailsV2Request{}, fmt.Errorf("invalid since: %w", err)
		}
		norm.since = t
		norm.hasSince = true
	}
	if strings.TrimSpace(req.Before) != "" {
		t, err := time.Parse(time.RFC3339, strings.TrimSpace(req.Before))
		if err != nil {
			return normalizedListEmailsV2Request{}, fmt.Errorf("invalid before: %w", err)
		}
		norm.before = t
		norm.hasBefore = true
	}
	if norm.hasSince && norm.hasBefore && norm.before.Before(norm.since) {
		return normalizedListEmailsV2Request{}, fmt.Errorf("before must be after or equal to since")
	}
	norm.fingerprint = listEmailsV2Fingerprint(norm)
	return norm, nil
}

func filterListEmailsV2(items []MailMessage, norm normalizedListEmailsV2Request) []MailMessage {
	result := make([]MailMessage, 0, len(items))
	for _, item := range items {
		sentAt, hasDate := parseMessageTime(item.SentDate)
		item.DateMissing = !hasDate
		if (norm.hasSince || norm.hasBefore) && !hasDate {
			continue
		}
		if norm.hasSince && sentAt.Before(norm.since) {
			continue
		}
		if norm.hasBefore && !sentAt.Before(norm.before) {
			continue
		}
		if norm.unreadOnly != nil && *norm.unreadOnly && item.Read {
			continue
		}
		fields := make([]string, 0, 4)
		if norm.keyword != "" {
			fields = append(fields, keywordMatchedFields(item, norm.keyword)...)
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
		result = append(result, item)
	}
	return result
}

func paginateListEmailsV2(items []MailMessage, norm normalizedListEmailsV2Request) (ListEmailsV2Result, error) {
	sorted := append([]MailMessage(nil), items...)
	sortListEmailsV2(sorted, norm.sort)
	if norm.cursor != "" {
		cursor, err := decodeListEmailsV2Cursor(norm.cursor)
		if err != nil {
			return ListEmailsV2Result{}, err
		}
		if cursor.Fingerprint != norm.fingerprint || cursor.Sort != norm.sort {
			return ListEmailsV2Result{}, fmt.Errorf("cursor does not match current query")
		}
		sorted = sorted[pageStartAfterCursor(sorted, cursor, norm.sort):]
	}

	hasMore := len(sorted) > norm.limit
	pageItems := sorted
	if hasMore {
		pageItems = sorted[:norm.limit]
	}
	result := ListEmailsV2Result{Items: pageItems, HasMore: hasMore}
	if hasMore && len(pageItems) > 0 {
		result.NextCursor = encodeListEmailsV2Cursor(listEmailsV2Cursor{
			Version:     listEmailsV2CursorVer,
			Fingerprint: norm.fingerprint,
			Sort:        norm.sort,
			LastUID:     pageItems[len(pageItems)-1].UID,
			LastDate:    pageItems[len(pageItems)-1].SentDate,
		})
	}
	return result, nil
}

func sortListEmailsV2(items []MailMessage, sortMode string) {
	sort.SliceStable(items, func(i, j int) bool {
		left, right := items[i], items[j]
		switch sortMode {
		case "uid_asc":
			return left.UID < right.UID
		case "uid_desc":
			return left.UID > right.UID
		case "date_asc":
			return compareMessageDate(left, right, true) < 0
		default:
			return compareMessageDate(left, right, false) < 0
		}
	})
}

func compareMessageDate(left, right MailMessage, asc bool) int {
	leftTime, leftOK := parseMessageTime(left.SentDate)
	rightTime, rightOK := parseMessageTime(right.SentDate)
	if leftOK && rightOK {
		if !leftTime.Equal(rightTime) {
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
	} else if leftOK != rightOK {
		if leftOK {
			return -1
		}
		return 1
	}
	if asc {
		if left.UID < right.UID {
			return -1
		}
		if left.UID > right.UID {
			return 1
		}
		return 0
	}
	if left.UID > right.UID {
		return -1
	}
	if left.UID < right.UID {
		return 1
	}
	return 0
}

func pageStartAfterCursor(items []MailMessage, cursor listEmailsV2Cursor, sortMode string) int {
	for i, item := range items {
		if item.UID == cursor.LastUID {
			return i + 1
		}
	}
	for i, item := range items {
		if itemComesAfterCursor(item, cursor, sortMode) {
			return i
		}
	}
	return len(items)
}

func itemComesAfterCursor(item MailMessage, cursor listEmailsV2Cursor, sortMode string) bool {
	switch sortMode {
	case "uid_asc":
		return item.UID > cursor.LastUID
	case "uid_desc":
		return item.UID < cursor.LastUID
	case "date_asc":
		cursorDate, cursorOK := parseMessageTime(cursor.LastDate)
		itemDate, itemOK := parseMessageTime(item.SentDate)
		if cursorOK && itemOK {
			return itemDate.After(cursorDate) || (itemDate.Equal(cursorDate) && item.UID > cursor.LastUID)
		}
		if cursorOK != itemOK {
			return !itemOK
		}
		return item.UID > cursor.LastUID
	default:
		cursorDate, cursorOK := parseMessageTime(cursor.LastDate)
		itemDate, itemOK := parseMessageTime(item.SentDate)
		if cursorOK && itemOK {
			return itemDate.Before(cursorDate) || (itemDate.Equal(cursorDate) && item.UID < cursor.LastUID)
		}
		if cursorOK != itemOK {
			return !itemOK
		}
		return item.UID < cursor.LastUID
	}
}

func keywordMatchedFields(item MailMessage, keyword string) []string {
	fields := make([]string, 0, 4)
	if containsFold(item.Subject, keyword) {
		fields = append(fields, "subject")
	}
	if containsFold(item.From, keyword) {
		fields = append(fields, "from")
	}
	if containsFold(firstNonEmpty(item.TextBody, item.TextContent, item.Summary), keyword) {
		fields = append(fields, "textBody")
	}
	if containsFold(firstNonEmpty(item.HTMLBody, item.HTMLContent), keyword) {
		fields = append(fields, "htmlBody")
	}
	return fields
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

func parseMessageTime(value string) (time.Time, bool) {
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

func listEmailsV2Fingerprint(norm normalizedListEmailsV2Request) string {
	payload := map[string]interface{}{
		"folder":  norm.folder,
		"keyword": norm.keyword,
		"from":    norm.from,
		"subject": norm.subject,
		"sort":    norm.sort,
		"view":    norm.view,
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

func encodeListEmailsV2Cursor(cursor listEmailsV2Cursor) string {
	data, _ := json.Marshal(cursor)
	return base64.RawURLEncoding.EncodeToString(data)
}

func decodeListEmailsV2Cursor(value string) (listEmailsV2Cursor, error) {
	data, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return listEmailsV2Cursor{}, fmt.Errorf("invalid cursor: %w", err)
	}
	var cursor listEmailsV2Cursor
	if err := json.Unmarshal(data, &cursor); err != nil {
		return listEmailsV2Cursor{}, fmt.Errorf("invalid cursor: %w", err)
	}
	if cursor.Version != listEmailsV2CursorVer || cursor.LastUID == 0 {
		return listEmailsV2Cursor{}, fmt.Errorf("unsupported cursor")
	}
	return cursor, nil
}
