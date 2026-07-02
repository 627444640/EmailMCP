package service

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"io"
	"mime/quotedprintable"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode"

	"email-mcp-service/config"

	"github.com/emersion/go-imap"
)

const defaultAttachmentOutputDir = "attachments"

// AttachmentDownloadRequest identifies one email whose attachments should be saved.
type AttachmentDownloadRequest struct {
	UID       uint32 `json:"uid"`
	Folder    string `json:"folder"`
	OutputDir string `json:"outputDir"`
}

// SavedAttachment describes one saved email attachment.
type SavedAttachment struct {
	FileName    string `json:"fileName"`
	Path        string `json:"path"`
	ContentType string `json:"contentType,omitempty"`
	Size        int64  `json:"size"`
}

// AttachmentDownloadResult contains saved attachments for one email.
type AttachmentDownloadResult struct {
	UID         uint32            `json:"uid"`
	Folder      string            `json:"folder"`
	Subject     string            `json:"subject"`
	From        string            `json:"from"`
	SentDate    string            `json:"sentDate"`
	Attachments []SavedAttachment `json:"attachments"`
	Skipped     []string          `json:"skipped,omitempty"`
}

type attachmentPart struct {
	path        []int
	filename    string
	contentType string
	encoding    string
	size        int64
}

// DownloadEmailAttachments downloads all named attachments for one email UID.
func DownloadEmailAttachments(cfg *config.Config, req *AttachmentDownloadRequest) (*AttachmentDownloadResult, error) {
	if req == nil {
		return nil, fmt.Errorf("request is required")
	}
	if req.UID == 0 {
		return nil, fmt.Errorf("uid 不能为空")
	}
	imapCfg, err := resolveIMAPConfig(cfg, &ReceiveMailRequest{})
	if err != nil {
		return nil, err
	}
	folderName := req.Folder
	if folderName == "" {
		folderName = "INBOX"
	}
	outputDir := attachmentOutputDir(cfg, req.OutputDir)

	c, err := connectIMAP(imapCfg)
	if err != nil {
		return nil, err
	}
	defer c.Logout()

	if _, err = c.Select(folderName, true); err != nil {
		return nil, fmt.Errorf("选择文件夹失败(%s): %w", folderName, err)
	}

	seqSet := new(imap.SeqSet)
	seqSet.AddNum(req.UID)
	messages := make(chan *imap.Message, 1)
	fetchItems := []imap.FetchItem{imap.FetchUid, imap.FetchEnvelope, imap.FetchBodyStructure}
	if err = c.UidFetch(seqSet, fetchItems, messages); err != nil {
		return nil, fmt.Errorf("IMAP Fetch 失败: %w", err)
	}
	msg := <-messages
	if msg == nil {
		return nil, fmt.Errorf("未找到 UID 对应的邮件")
	}

	mail := envelopeToMailMessage(msg)
	parts := attachmentParts(msg.BodyStructure)
	result := &AttachmentDownloadResult{
		UID:      req.UID,
		Folder:   folderName,
		Subject:  mail.Subject,
		From:     mail.From,
		SentDate: mail.SentDate,
	}
	for _, part := range parts {
		partSeq := new(imap.SeqSet)
		partSeq.AddNum(req.UID)
		section := &imap.BodySectionName{
			BodyPartName: imap.BodyPartName{Path: part.path},
			Peek:         true,
		}
		raw := fetchSectionBytes(c, partSeq, section)
		if len(raw) == 0 {
			result.Skipped = append(result.Skipped, part.filename+": empty attachment body")
			continue
		}
		data, err := decodeAttachmentData(raw, part.encoding)
		if err != nil {
			result.Skipped = append(result.Skipped, part.filename+": "+err.Error())
			continue
		}
		path, err := saveAttachment(outputDir, mail, part.filename, data)
		if err != nil {
			result.Skipped = append(result.Skipped, part.filename+": "+err.Error())
			continue
		}
		result.Attachments = append(result.Attachments, SavedAttachment{
			FileName:    filepath.Base(path),
			Path:        path,
			ContentType: part.contentType,
			Size:        int64(len(data)),
		})
	}
	return result, nil
}

func attachmentParts(bs *imap.BodyStructure) []attachmentPart {
	if bs == nil {
		return nil
	}
	var parts []attachmentPart
	bs.Walk(func(path []int, part *imap.BodyStructure) bool {
		filename, err := part.Filename()
		if err != nil {
			filename = ""
		}
		filename = decodeMIMEHeader(filename)
		if filename != "" {
			partPath := make([]int, len(path))
			copy(partPath, path)
			parts = append(parts, attachmentPart{
				path:        partPath,
				filename:    filename,
				contentType: strings.ToLower(part.MIMEType + "/" + part.MIMESubType),
				encoding:    strings.ToLower(part.Encoding),
				size:        int64(part.Size),
			})
		}
		return true
	})
	return parts
}

func attachmentMetadata(bs *imap.BodyStructure) []EmailAttachment {
	parts := attachmentParts(bs)
	attachments := make([]EmailAttachment, 0, len(parts))
	for _, part := range parts {
		attachments = append(attachments, EmailAttachment{
			Name:        part.filename,
			ContentType: part.contentType,
			Size:        part.size,
		})
	}
	return attachments
}

func attachmentNamesFromMetadata(attachments []EmailAttachment) []string {
	if len(attachments) == 0 {
		return nil
	}
	names := make([]string, 0, len(attachments))
	for _, attachment := range attachments {
		if attachment.Name != "" {
			names = append(names, attachment.Name)
		}
	}
	return names
}

func decodeAttachmentData(data []byte, encoding string) ([]byte, error) {
	switch strings.ToLower(strings.TrimSpace(encoding)) {
	case "base64":
		cleaned := make([]byte, 0, len(data))
		for _, b := range data {
			if b != '\r' && b != '\n' && b != ' ' && b != '\t' {
				cleaned = append(cleaned, b)
			}
		}
		return base64.StdEncoding.DecodeString(string(cleaned))
	case "quoted-printable":
		return io.ReadAll(quotedprintable.NewReader(bytes.NewReader(data)))
	default:
		return data, nil
	}
}

func saveAttachment(outputDir string, msg MailMessage, originalName string, data []byte) (string, error) {
	if strings.TrimSpace(outputDir) == "" {
		outputDir = defaultAttachmentOutputDir
	}
	baseAbs, err := filepath.Abs(outputDir)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(baseAbs, 0o700); err != nil {
		return "", err
	}

	prefix := attachmentFilePrefix(msg)
	filename := sanitizeFileName(originalName)
	if filename == "" {
		filename = "attachment"
	}
	target := filepath.Join(baseAbs, prefix+"_"+filename)
	target, err = uniquePath(target)
	if err != nil {
		return "", err
	}
	targetAbs, err := filepath.Abs(target)
	if err != nil {
		return "", err
	}
	if !isWithinDir(targetAbs, baseAbs) {
		return "", fmt.Errorf("attachment path escapes download dir")
	}
	if err := os.WriteFile(targetAbs, data, 0o600); err != nil {
		return "", err
	}
	return targetAbs, nil
}

func attachmentOutputDir(cfg *config.Config, requested string) string {
	if strings.TrimSpace(requested) != "" {
		return requested
	}
	if cfg != nil && strings.TrimSpace(cfg.Attachments.DownloadDir) != "" {
		return cfg.Attachments.DownloadDir
	}
	return defaultAttachmentOutputDir
}

func attachmentFilePrefix(msg MailMessage) string {
	date := "unknown-date"
	if msg.SentDate != "" {
		if t, err := time.Parse(time.RFC3339, msg.SentDate); err == nil {
			date = t.Format("20060102")
		}
	}
	from := sanitizeFileName(emailIdentity(msg.From))
	if from == "" {
		from = "unknown-sender"
	}
	return fmt.Sprintf("%s_%s_UID%d", date, from, msg.UID)
}

func sanitizeFileName(name string) string {
	name = strings.TrimSpace(filepath.Base(name))
	if name == "." || name == string(filepath.Separator) {
		name = ""
	}
	var b strings.Builder
	for _, r := range name {
		switch {
		case r < 32 || r == 127:
			b.WriteByte('_')
		case strings.ContainsRune(`<>:"/\|?*`, r):
			b.WriteByte('_')
		case unicode.IsSpace(r):
			b.WriteByte('_')
		default:
			b.WriteRune(r)
		}
	}
	cleaned := strings.Trim(b.String(), " ._")
	if len([]rune(cleaned)) > 160 {
		runes := []rune(cleaned)
		cleaned = string(runes[:160])
	}
	return cleaned
}

func emailIdentity(from string) string {
	from = strings.TrimSpace(from)
	if lt := strings.LastIndex(from, "<"); lt >= 0 {
		if gt := strings.LastIndex(from, ">"); gt > lt {
			return strings.TrimSpace(from[lt+1 : gt])
		}
	}
	return from
}

func uniquePath(path string) (string, error) {
	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			return path, nil
		}
		return "", err
	}
	ext := filepath.Ext(path)
	base := strings.TrimSuffix(path, ext)
	for i := 2; i < 1000; i++ {
		candidate := fmt.Sprintf("%s_%d%s", base, i, ext)
		if _, err := os.Stat(candidate); err != nil {
			if os.IsNotExist(err) {
				return candidate, nil
			}
			return "", err
		}
	}
	return "", fmt.Errorf("cannot allocate unique attachment filename")
}

func isWithinDir(path, dir string) bool {
	rel, err := filepath.Rel(dir, path)
	if err != nil {
		return false
	}
	return rel == "." || (rel != "" && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && rel != ".." && !filepath.IsAbs(rel))
}
