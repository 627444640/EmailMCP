package service

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"

	"email-mcp-service/config"
)

type SearchAllFoldersRequest struct {
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

type SearchAllFoldersResult = ListEmailsV2Result

type searchAllCursor struct {
	Version     int    `json:"v"`
	Fingerprint string `json:"fp"`
	Sort        string `json:"sort"`
	LastFolder  string `json:"lastFolder"`
	LastUID     uint32 `json:"lastUid"`
	LastDate    string `json:"lastDate,omitempty"`
}

const searchAllCursorVersion = 1

func SearchAllFolders(cfg *config.Config, req *SearchAllFoldersRequest) (*SearchAllFoldersResult, error) {
	if req == nil {
		req = &SearchAllFoldersRequest{}
	}
	folders, err := ListFolders(cfg)
	if err != nil {
		return nil, err
	}
	var items []MailMessage
	for _, folder := range folders.Folders {
		cursor := ""
		v2Req := &ListEmailsV2Request{
			Folder:     folder.Path,
			Limit:      maxListEmailsV2Limit,
			Keyword:    req.Keyword,
			From:       req.From,
			Subject:    req.Subject,
			UnreadOnly: req.UnreadOnly,
			Since:      req.Since,
			Before:     req.Before,
			Sort:       req.Sort,
			View:       req.View,
		}
		for {
			v2Req.Cursor = cursor
			page, err := ListEmailsV2(cfg, v2Req)
			if err != nil {
				break
			}
			for _, item := range page.Items {
				item.Folder = folder.Path
				items = append(items, item)
			}
			if !page.HasMore || page.NextCursor == "" {
				break
			}
			cursor = page.NextCursor
		}
	}
	v2Req := &ListEmailsV2Request{Limit: req.Limit, Keyword: req.Keyword, From: req.From, Subject: req.Subject, UnreadOnly: req.UnreadOnly, Since: req.Since, Before: req.Before, Sort: req.Sort, View: req.View}
	norm, err := normalizeListEmailsV2Request(v2Req)
	if err != nil {
		return nil, err
	}
	cursor, hasCursor, err := unwrapSearchAllCursor(req.Cursor, searchAllFingerprint(req))
	if err != nil {
		return nil, err
	}
	sortSearchAllMessages(items, norm.sort)
	if hasCursor {
		items = items[searchAllPageStart(items, cursor, norm.sort):]
	}
	hasMore := len(items) > norm.limit
	pageItems := items
	if hasMore {
		pageItems = items[:norm.limit]
	}
	result := SearchAllFoldersResult{Items: pageItems, HasMore: hasMore}
	if hasMore && len(pageItems) > 0 {
		last := pageItems[len(pageItems)-1]
		result.NextCursor = wrapSearchAllCursor(searchAllCursor{
			Version:     searchAllCursorVersion,
			Fingerprint: searchAllFingerprint(req),
			Sort:        norm.sort,
			LastFolder:  last.Folder,
			LastUID:     last.UID,
			LastDate:    last.SentDate,
		})
	}
	return &result, nil
}

func unwrapSearchAllCursor(value, fingerprint string) (searchAllCursor, bool, error) {
	if value == "" {
		return searchAllCursor{}, false, nil
	}
	data, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return searchAllCursor{}, false, fmt.Errorf("invalid cursor: %w", err)
	}
	var decoded searchAllCursor
	if err := json.Unmarshal(data, &decoded); err != nil || decoded.Version != searchAllCursorVersion {
		return searchAllCursor{}, false, fmt.Errorf("invalid cursor")
	}
	if decoded.Fingerprint != fingerprint {
		return searchAllCursor{}, false, fmt.Errorf("cursor does not match current query")
	}
	if decoded.LastUID == 0 {
		return searchAllCursor{}, false, fmt.Errorf("invalid cursor uid")
	}
	return decoded, true, nil
}

func wrapSearchAllCursor(cursor searchAllCursor) string {
	data, _ := json.Marshal(cursor)
	return base64.RawURLEncoding.EncodeToString(data)
}

func searchAllFingerprint(req *SearchAllFoldersRequest) string {
	if req == nil {
		req = &SearchAllFoldersRequest{}
	}
	payload := SearchAllFoldersRequest{
		Keyword:    req.Keyword,
		From:       req.From,
		Subject:    req.Subject,
		UnreadOnly: req.UnreadOnly,
		Since:      req.Since,
		Before:     req.Before,
		Sort:       req.Sort,
		View:       req.View,
	}
	data, _ := json.Marshal(payload)
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func sortSearchAllMessages(items []MailMessage, sortMode string) {
	sort.SliceStable(items, func(i, j int) bool {
		return compareSearchAllMessages(items[i], items[j], sortMode) < 0
	})
}

func searchAllPageStart(items []MailMessage, cursor searchAllCursor, sortMode string) int {
	for i, item := range items {
		if item.Folder == cursor.LastFolder && item.UID == cursor.LastUID {
			return i + 1
		}
	}
	for i, item := range items {
		if compareSearchAllMessageToCursor(item, cursor, sortMode) > 0 {
			return i
		}
	}
	return len(items)
}

func compareSearchAllMessages(left, right MailMessage, sortMode string) int {
	switch sortMode {
	case "uid_asc":
		return compareUIDThenFolder(left, right, true)
	case "uid_desc":
		return compareUIDThenFolder(left, right, false)
	case "date_asc":
		if cmp := compareMessageDate(left, right, true); cmp != 0 {
			return cmp
		}
	default:
		if cmp := compareMessageDate(left, right, false); cmp != 0 {
			return cmp
		}
	}
	return compareFolder(left.Folder, right.Folder)
}

func compareSearchAllMessageToCursor(item MailMessage, cursor searchAllCursor, sortMode string) int {
	return compareSearchAllMessages(item, MailMessage{Folder: cursor.LastFolder, UID: cursor.LastUID, SentDate: cursor.LastDate}, sortMode)
}

func compareUIDThenFolder(left, right MailMessage, asc bool) int {
	if left.UID != right.UID {
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
	return compareFolder(left.Folder, right.Folder)
}

func compareFolder(left, right string) int {
	switch {
	case left < right:
		return -1
	case left > right:
		return 1
	default:
		return 0
	}
}
