package service

import (
	"bytes"
	"crypto/tls"
	"fmt"
	"io"
	"log"
	"mime"
	"net"
	"sort"
	"strings"
	"time"
	"unicode"

	"email-mcp-service/config"

	"github.com/emersion/go-imap"
	"github.com/emersion/go-imap/client"
)

// ReceiveMailRequest 接收邮件请求参数.
type ReceiveMailRequest struct {
	Host       string // IMAP 主机(可选,缺省取配置)
	Port       int    // IMAP 端口(可选)
	Username   string // 登录账号(可选)
	Password   string // 登录密码/授权码(可选)
	SSL        *bool  // 是否启用 SSL(可选)
	Folder     string // 邮箱文件夹,默认 INBOX
	Limit      int    // 返回最大条数,默认 10
	Keyword    string // 关键字模糊匹配(主题/发件人/正文)
	From       string // 发件人邮箱精确匹配
	Subject    string // 主题包含的关键字
	UnreadOnly *bool  // 是否仅未读,默认 false
	UID        uint32 // 邮件 UID(读取单封时必填)
	MarkAsRead *bool  // 读取后是否标记为已读,默认 false
	View       string // 视图: SUMMARY(摘要) 或 FULL(完整),默认 SUMMARY
}

// MailMessage 邮件信息 DTO.
type MailMessage struct {
	UID             uint32            `json:"uid"`
	Folder          string            `json:"folder,omitempty"`
	Subject         string            `json:"subject"`
	From            string            `json:"from"`
	To              []string          `json:"to"`
	CC              []string          `json:"cc"`
	SentDate        string            `json:"sentDate"`
	Read            bool              `json:"read"`
	HasAttachment   bool              `json:"hasAttachment"`
	AttachmentNames []string          `json:"attachmentNames"`
	TextContent     string            `json:"textContent,omitempty"`
	HTMLContent     string            `json:"htmlContent,omitempty"`
	TextBody        string            `json:"textBody,omitempty"`
	HTMLBody        string            `json:"htmlBody,omitempty"`
	Attachments     []EmailAttachment `json:"attachments,omitempty"`
	MatchedFields   []string          `json:"matchedFields,omitempty"`
	DateMissing     bool              `json:"dateMissing,omitempty"`
	BodyError       string            `json:"bodyError,omitempty"`
	MessageID       string            `json:"messageId,omitempty"`
	InReplyTo       string            `json:"inReplyTo,omitempty"`
	References      []string          `json:"references,omitempty"`
	Summary         string            `json:"summary,omitempty"`
}

// EmailAttachment describes one attachment without downloading its content.
type EmailAttachment struct {
	Name        string `json:"name"`
	ContentType string `json:"contentType,omitempty"`
	Size        int64  `json:"size,omitempty"`
}

// MailFolder describes one selectable mailbox folder.
type MailFolder struct {
	Name         string  `json:"name"`
	Path         string  `json:"path"`
	MessageCount *uint32 `json:"messageCount"`
	UnreadCount  *uint32 `json:"unreadCount"`
}

// ListFoldersResult contains all accessible folders for the configured account.
type ListFoldersResult struct {
	Folders []MailFolder `json:"folders"`
}

// ListEmails 列出/搜索邮件.
// 服务端过滤 + 本地内存过滤组合:
//  1. ASCII 条件走服务端(From 邮箱、纯英文 Subject/Keyword),减少拉取量
//  2. 含 CJK 字符的条件走本地内存过滤(规避 QQ 邮箱等 IMAP 服务端的
//     "cannot send literal: no continuation request received" 兼容性问题)
func ListEmails(cfg *config.Config, req *ReceiveMailRequest) ([]MailMessage, error) {
	// 1. 合并配置
	imapCfg, err := resolveIMAPConfig(cfg, req)
	if err != nil {
		return nil, err
	}

	folderName := req.Folder
	if folderName == "" {
		folderName = "INBOX"
	}
	limit := req.Limit
	if limit <= 0 {
		limit = 10
	}
	if limit > 100 {
		limit = 100
	}

	// 2. 检测是否需要本地过滤(任一过滤字段含 CJK 则走本地)
	needLocalFilter := containsCJK(req.Keyword) || containsCJK(req.From) || containsCJK(req.Subject)

	// 3. 连接 IMAP
	c, err := connectIMAP(imapCfg)
	if err != nil {
		return nil, err
	}
	defer c.Logout()

	// 4. 选择文件夹(只读)
	_, err = c.Select(folderName, true)
	if err != nil {
		return nil, fmt.Errorf("选择文件夹失败(%s): %w", folderName, err)
	}

	// 5. 构造搜索条件(buildSearchCriteria 已自动跳过 CJK 字段)
	criteria := buildSearchCriteria(req)
	uids, err := c.UidSearch(criteria)
	if err != nil {
		return nil, fmt.Errorf("IMAP 搜索失败: %w", err)
	}

	if len(uids) == 0 {
		return []MailMessage{}, nil
	}

	// 6. 本地过滤场景:拉所有匹配邮件,本地过滤后再截断 limit
	//    非本地过滤场景:直接取最新 N 条,降低拉取量
	fetchCount := len(uids)
	if !needLocalFilter && fetchCount > limit {
		fetchCount = limit
		uids = uids[len(uids)-limit:]
	}
	reverseUids(uids[:fetchCount])

	// 7. 批量获取(Envelope + Flags)
	seqSet := new(imap.SeqSet)
	for i := 0; i < fetchCount; i++ {
		seqSet.AddNum(uids[i])
	}

	isSummary := req.View != "FULL"
	fetchItems, previewSection := listEmailFetchItems(isSummary)
	messages := make(chan *imap.Message, fetchCount)
	if err = c.UidFetch(seqSet, fetchItems, messages); err != nil {
		return nil, fmt.Errorf("IMAP Fetch 失败: %w", err)
	}

	result := make([]MailMessage, 0, fetchCount)
	for msg := range messages {
		if msg == nil {
			continue
		}
		mm := envelopeToMailMessage(msg)
		// 本地过滤:匹配 From/Subject/To/TextContent(全部小写)
		if needLocalFilter && !matchLocalFilter(&mm, req) {
			continue
		}
		if isSummary {
			// 从 Body 中提取正文预览
			if previewSection != nil {
				if r, ok := msg.Body[previewSection]; ok {
					data, readErr := io.ReadAll(r)
					if readErr == nil && len(data) > 0 {
						mm.Summary = truncate(string(data), 200)
					}
				}
			}
			mm.TextContent = ""
			mm.HTMLContent = ""
		}
		result = append(result, mm)
		// 命中 limit 条后停止
		if len(result) >= limit {
			break
		}
	}

	return result, nil
}

// ListFolders lists all accessible folders for the configured IMAP account.
func ListFolders(cfg *config.Config) (*ListFoldersResult, error) {
	imapCfg, err := resolveIMAPConfig(cfg, &ReceiveMailRequest{})
	if err != nil {
		return nil, err
	}
	c, err := connectIMAP(imapCfg)
	if err != nil {
		return nil, err
	}
	defer c.Logout()

	folderCh := make(chan *imap.MailboxInfo, 32)
	errCh := make(chan error, 1)
	go func() {
		errCh <- c.List("", "*", folderCh)
	}()

	var folders []MailFolder
	for info := range folderCh {
		if info == nil || hasMailboxAttribute(info.Attributes, "\\Noselect") {
			continue
		}
		folder := MailFolder{Name: info.Name, Path: info.Name}
		if status, err := c.Status(info.Name, []imap.StatusItem{imap.StatusMessages, imap.StatusUnseen}); err == nil {
			messageCount := status.Messages
			unreadCount := status.Unseen
			folder.MessageCount = &messageCount
			folder.UnreadCount = &unreadCount
		}
		folders = append(folders, folder)
	}
	if err := <-errCh; err != nil {
		return nil, fmt.Errorf("列出文件夹失败: %w", err)
	}
	sort.Slice(folders, func(i, j int) bool {
		return strings.ToLower(folders[i].Path) < strings.ToLower(folders[j].Path)
	})
	return &ListFoldersResult{Folders: folders}, nil
}

// ListEmailsV2 lists emails with stable cursor pagination and local filtering.
func ListEmailsV2(cfg *config.Config, req *ListEmailsV2Request) (*ListEmailsV2Result, error) {
	norm, err := normalizeListEmailsV2Request(req)
	if err != nil {
		return nil, err
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

	if _, err = c.Select(norm.folder, true); err != nil {
		return nil, fmt.Errorf("选择文件夹失败(%s): %w", norm.folder, err)
	}
	criteria := buildSearchCriteriaV2(norm)
	uids, err := c.UidSearch(criteria)
	if err != nil {
		return nil, fmt.Errorf("IMAP 搜索失败: %w", err)
	}
	if len(uids) == 0 {
		result, err := paginateListEmailsV2(nil, norm)
		if err != nil {
			return nil, err
		}
		return &result, nil
	}

	messages, err := fetchEnvelopeMessages(c, uids)
	if err != nil {
		return nil, err
	}
	items := make([]MailMessage, 0, len(messages))
	needBodies := norm.keyword != "" || norm.view == "FULL"
	for _, msg := range messages {
		if msg == nil {
			continue
		}
		mail := envelopeToMailMessage(msg)
		mail.Attachments = attachmentMetadata(msg.BodyStructure)
		mail.AttachmentNames = attachmentNamesFromMetadata(mail.Attachments)
		if len(mail.Attachments) > 0 {
			mail.HasAttachment = true
		}
		if needBodies {
			text, html, _ := fetchBodySections(c, msg.Uid, msg)
			setMessageBodies(&mail, text, html)
			if norm.view == "SUMMARY" {
				mail.Summary = truncate(firstNonEmpty(mail.TextBody, mail.HTMLBody), 200)
			}
		}
		items = append(items, mail)
	}

	filtered := filterListEmailsV2(items, norm)
	page, err := paginateListEmailsV2(filtered, norm)
	if err != nil {
		return nil, err
	}
	if norm.view == "SUMMARY" {
		for i := range page.Items {
			if page.Items[i].Summary == "" {
				page.Items[i].Summary = fetchMessagePreview(c, page.Items[i].UID)
			}
			page.Items[i].TextContent = ""
			page.Items[i].HTMLContent = ""
			page.Items[i].TextBody = ""
			page.Items[i].HTMLBody = ""
		}
	}
	return &page, nil
}

func buildSearchCriteriaV2(norm normalizedListEmailsV2Request) *imap.SearchCriteria {
	criteria := imap.NewSearchCriteria()
	if norm.unreadOnly != nil && *norm.unreadOnly {
		criteria.WithoutFlags = []string{imap.SeenFlag}
	}
	return criteria
}

func fetchEnvelopeMessages(c *client.Client, uids []uint32) ([]*imap.Message, error) {
	seqSet := new(imap.SeqSet)
	for _, uid := range uids {
		seqSet.AddNum(uid)
	}
	messages := make(chan *imap.Message, len(uids))
	fetchItems := []imap.FetchItem{imap.FetchUid, imap.FetchFlags, imap.FetchEnvelope, imap.FetchBodyStructure}
	if err := c.UidFetch(seqSet, fetchItems, messages); err != nil {
		return nil, fmt.Errorf("IMAP Fetch 失败: %w", err)
	}
	result := make([]*imap.Message, 0, len(uids))
	for msg := range messages {
		if msg != nil {
			result = append(result, msg)
		}
	}
	return result, nil
}

func fetchMessagePreview(c *client.Client, uid uint32) string {
	seqSet := new(imap.SeqSet)
	seqSet.AddNum(uid)
	section := &imap.BodySectionName{
		BodyPartName: imap.BodyPartName{Path: []int{1}},
		Peek:         true,
		Partial:      []int{0, 1024},
	}
	return truncate(fetchSection(c, seqSet, section), 200)
}

func setMessageBodies(mail *MailMessage, text, html string) {
	mail.TextContent = text
	mail.HTMLContent = html
	mail.TextBody = text
	mail.HTMLBody = html
}

func markAsReadRequested(req *ReceiveMailRequest) bool {
	return req != nil && req.MarkAsRead != nil && *req.MarkAsRead
}

func hasMailboxAttribute(attributes []string, want string) bool {
	for _, attr := range attributes {
		if strings.EqualFold(attr, want) {
			return true
		}
	}
	return false
}

func listEmailFetchItems(isSummary bool) ([]imap.FetchItem, *imap.BodySectionName) {
	fetchItems := []imap.FetchItem{imap.FetchUid, imap.FetchFlags, imap.FetchEnvelope, imap.FetchBodyStructure}
	if !isSummary {
		return fetchItems, nil
	}
	previewSection := &imap.BodySectionName{
		BodyPartName: imap.BodyPartName{Path: []int{1}},
		Peek:         true,
		Partial:      []int{0, 1024},
	}
	return append(fetchItems, previewSection.FetchItem()), previewSection
}

// GetEmailByUID 按 UID 读取单封邮件详情.
func GetEmailByUID(cfg *config.Config, req *ReceiveMailRequest) (*MailMessage, error) {
	if req.UID == 0 {
		return nil, fmt.Errorf("uid 不能为空")
	}

	// 1. 合并配置
	imapCfg, err := resolveIMAPConfig(cfg, req)
	if err != nil {
		return nil, err
	}

	folderName := req.Folder
	if folderName == "" {
		folderName = "INBOX"
	}

	// 2. 连接
	c, err := connectIMAP(imapCfg)
	if err != nil {
		return nil, err
	}
	defer c.Logout()

	// 3. 选择文件夹(读写,以便标记已读)
	markRead := markAsReadRequested(req)
	_, err = c.Select(folderName, !markRead)
	if err != nil {
		return nil, fmt.Errorf("选择文件夹失败(%s): %w", folderName, err)
	}

	// 4. 按 UID 获取
	seqSet := new(imap.SeqSet)
	seqSet.AddNum(req.UID)

	// 先获取 Envelope + Flags
	messages := make(chan *imap.Message, 1)
	fetchItems := []imap.FetchItem{imap.FetchUid, imap.FetchFlags, imap.FetchEnvelope, imap.FetchBodyStructure}
	if err = c.UidFetch(seqSet, fetchItems, messages); err != nil {
		return nil, fmt.Errorf("IMAP Fetch 失败: %w", err)
	}

	msg := <-messages
	if msg == nil {
		return nil, nil
	}

	mm := envelopeToMailMessage(msg)

	// 5. 获取正文(用 BODY[] section)
	textContent, htmlContent, attachmentNames := fetchBodySections(c, req.UID, msg)
	setMessageBodies(&mm, textContent, htmlContent)
	mm.Attachments = attachmentMetadata(msg.BodyStructure)
	mm.References = fetchReferencesHeader(c, req.UID)
	if len(mm.Attachments) > 0 {
		attachmentNames = attachmentNamesFromMetadata(mm.Attachments)
	}
	mm.AttachmentNames = attachmentNames
	if len(attachmentNames) > 0 {
		mm.HasAttachment = true
	}

	// 6. 标记已读
	if markRead && !mm.Read {
		seqSet2 := new(imap.SeqSet)
		seqSet2.AddNum(req.UID)
		if err = c.UidStore(seqSet2, imap.AddFlags, []interface{}{imap.SeenFlag}, nil); err != nil {
			log.Printf("标记已读失败, uid=%d: %v", req.UID, err)
		}
		mm.Read = true
	}

	return &mm, nil
}

// fetchBodySections 获取邮件正文(纯文本 + HTML)和附件名.
func fetchBodySections(c *client.Client, uid uint32, msg *imap.Message) (text, html string, attachments []string) {
	if msg.BodyStructure == nil {
		return "", "", nil
	}

	// 遍历 BodyStructure,找到 text/plain 和 text/html 部分
	var textPart, htmlPart *bodyPartRef

	msg.BodyStructure.Walk(func(path []int, part *imap.BodyStructure) bool {
		// 收集附件名
		if filename, err := part.Filename(); err == nil && filename != "" {
			attachments = append(attachments, decodeMIMEHeader(filename))
		}

		mimeType := strings.ToLower(part.MIMEType + "/" + part.MIMESubType)
		switch mimeType {
		case "text/plain":
			if textPart == nil {
				textPart = newBodyPartRef(path, part)
			}
		case "text/html":
			if htmlPart == nil {
				htmlPart = newBodyPartRef(path, part)
			}
		}
		return true
	})

	// 获取 text/plain 正文
	if textPart != nil {
		text = fetchBodyPart(c, uid, textPart)
	}

	// 获取 text/html 正文
	if htmlPart != nil {
		html = fetchBodyPart(c, uid, htmlPart)
	}

	// 如果没有找到分段的 text/plain,尝试整个 BODY[]
	if text == "" && html == "" {
		seqSet := new(imap.SeqSet)
		seqSet.AddNum(uid)
		section := &imap.BodySectionName{}
		text = fetchSection(c, seqSet, section)
	}

	return text, html, attachments
}

type bodyPartRef struct {
	path []int
	part *imap.BodyStructure
}

func newBodyPartRef(path []int, part *imap.BodyStructure) *bodyPartRef {
	return &bodyPartRef{path: append([]int(nil), path...), part: part}
}

func fetchBodyPart(c *client.Client, uid uint32, ref *bodyPartRef) string {
	if ref == nil {
		return ""
	}
	seqSet := new(imap.SeqSet)
	seqSet.AddNum(uid)
	section := &imap.BodySectionName{BodyPartName: imap.BodyPartName{Path: ref.path}}
	data := fetchSectionBytes(c, seqSet, section)
	return decodeBodyPartContent(data, ref.part)
}

func decodeBodyPartContent(data []byte, part *imap.BodyStructure) string {
	if len(data) == 0 {
		return ""
	}
	if part == nil {
		return string(data)
	}
	decoded, err := decodeAttachmentData(data, part.Encoding)
	if err != nil {
		log.Printf("正文解码失败, encoding=%s: %v", part.Encoding, err)
		return string(data)
	}
	return string(decoded)
}

// fetchSection 获取指定 section 的正文内容.
func fetchSection(c *client.Client, seqSet *imap.SeqSet, section *imap.BodySectionName) string {
	data := fetchSectionBytes(c, seqSet, section)
	if len(data) == 0 {
		return ""
	}
	return string(data)
}

func fetchSectionBytes(c *client.Client, seqSet *imap.SeqSet, section *imap.BodySectionName) []byte {
	messages := make(chan *imap.Message, 1)
	if err := c.UidFetch(seqSet, []imap.FetchItem{section.FetchItem()}, messages); err != nil {
		log.Printf("Fetch section 失败: %v", err)
		return nil
	}

	msg := <-messages
	if msg == nil {
		return nil
	}

	r := readBodyLiteral(msg, section)
	if r == nil {
		return nil
	}
	data, err := io.ReadAll(r)
	if err == nil && len(data) > 0 {
		return data
	}

	return nil
}

func readBodyLiteral(msg *imap.Message, section *imap.BodySectionName) imap.Literal {
	if msg == nil || section == nil {
		return nil
	}
	return msg.GetBody(section)
}

// imapConn IMAP 连接配置.
type imapConn struct {
	host     string
	port     int
	username string
	password string
	ssl      bool
}

// resolveIMAPConfig 合并请求参数和全局配置.
func resolveIMAPConfig(cfg *config.Config, req *ReceiveMailRequest) (*imapConn, error) {
	host := firstNonEmpty(req.Host, cfg.IMAP.Host)
	port := req.Port
	if port == 0 {
		port = cfg.IMAP.Port
	}
	username := firstNonEmpty(req.Username, cfg.IMAP.Username)
	password := firstNonEmpty(req.Password, cfg.IMAP.Password)
	ssl := cfg.IMAP.SSL
	if req.SSL != nil {
		ssl = *req.SSL
	}

	if host == "" {
		return nil, fmt.Errorf("IMAP 主机未配置,请在 .env 文件中设置 EMAIL_IMAP_HOST")
	}
	if username == "" {
		return nil, fmt.Errorf("IMAP 用户名未配置,请设置 EMAIL_IMAP_USERNAME 或 EMAIL_COMMON_USERNAME")
	}
	if password == "" {
		return nil, fmt.Errorf("IMAP 密码未配置,请设置 EMAIL_IMAP_PASSWORD 或 EMAIL_COMMON_PASSWORD")
	}

	return &imapConn{host, port, username, password, ssl}, nil
}

// connectIMAP 建立 IMAP 连接.
// 使用 10 秒连接超时,防止长时间阻塞;TLS 最低版本要求 1.2.
func connectIMAP(cfg *imapConn) (*client.Client, error) {
	addr := fmt.Sprintf("%s:%d", cfg.host, cfg.port)
	dialer := &net.Dialer{Timeout: 10 * time.Second}

	var c *client.Client
	var err error

	if cfg.ssl {
		tlsConfig := &tls.Config{ServerName: cfg.host, MinVersion: tls.VersionTLS12}
		c, err = client.DialWithDialerTLS(dialer, addr, tlsConfig)
	} else {
		c, err = client.DialWithDialer(dialer, addr)
	}
	if err != nil {
		return nil, fmt.Errorf("IMAP 连接失败(%s): %w", addr, err)
	}

	if err = c.Login(cfg.username, cfg.password); err != nil {
		c.Logout()
		return nil, fmt.Errorf("IMAP 登录失败: %w", err)
	}

	return c, nil
}

// CheckIMAPConnection verifies IMAP connectivity and authentication without reading messages.
func CheckIMAPConnection(cfg *config.Config) error {
	if err := cfg.ValidateIMAP(); err != nil {
		return err
	}
	port := cfg.IMAP.Port
	if port == 0 {
		port = 993
	}
	c, err := connectIMAP(&imapConn{
		host:     cfg.IMAP.Host,
		port:     port,
		username: cfg.IMAP.Username,
		password: cfg.IMAP.Password,
		ssl:      cfg.IMAP.SSL,
	})
	if err != nil {
		return err
	}
	_ = c.Logout()
	return nil
}

// buildSearchCriteria 构造 IMAP 搜索条件.
// 注意:含 CJK 字符的过滤条件不在此设置,转交给 ListEmails 的本地过滤处理.
// 原因:QQ 邮箱等 IMAP 服务端在收到中文 literal 时,会触发
// "cannot send literal: no continuation request received" 协议错误.
func buildSearchCriteria(req *ReceiveMailRequest) *imap.SearchCriteria {
	criteria := imap.NewSearchCriteria()

	// From/Subject 字段含中文时,跳过服务端搜索,在 ListEmails 中本地匹配
	if req.From != "" && !containsCJK(req.From) {
		criteria.Header.Add("From", req.From)
	}
	if req.Subject != "" && !containsCJK(req.Subject) {
		criteria.Header.Add("Subject", req.Subject)
	}
	if req.Keyword != "" && !containsCJK(req.Keyword) {
		criteria.Text = []string{req.Keyword}
	}
	if req.UnreadOnly != nil && *req.UnreadOnly {
		criteria.WithoutFlags = []string{imap.SeenFlag}
	}

	return criteria
}

// envelopeToMailMessage 将 IMAP Envelope 转换为 MailMessage.
func envelopeToMailMessage(msg *imap.Message) MailMessage {
	mm := MailMessage{
		UID:  msg.Uid,
		Read: containsFlag(msg.Flags, imap.SeenFlag),
	}

	if msg.Envelope != nil {
		mm.Subject = decodeMIMEHeader(msg.Envelope.Subject)
		mm.From = addressListToString(msg.Envelope.From)
		mm.To = addressListToStrings(msg.Envelope.To)
		mm.CC = addressListToStrings(msg.Envelope.Cc)
		mm.MessageID = msg.Envelope.MessageId
		mm.InReplyTo = msg.Envelope.InReplyTo
		if msg.Envelope.Date != (time.Time{}) {
			mm.SentDate = msg.Envelope.Date.Format("2006-01-02T15:04:05Z07:00")
		}
	}

	// 检查附件
	if msg.BodyStructure != nil {
		mm.HasAttachment = hasAttachmentInStructure(msg.BodyStructure)
	}

	return mm
}

func fetchReferencesHeader(c *client.Client, uid uint32) []string {
	seqSet := new(imap.SeqSet)
	seqSet.AddNum(uid)
	section := &imap.BodySectionName{
		BodyPartName: imap.BodyPartName{Specifier: imap.HeaderSpecifier, Fields: []string{"References"}},
		Peek:         true,
	}
	raw := fetchSectionBytes(c, seqSet, section)
	if len(raw) == 0 {
		return nil
	}
	return parseReferencesHeader(string(raw))
}

func parseReferencesHeader(raw string) []string {
	raw = strings.ReplaceAll(raw, "\r\n\t", " ")
	raw = strings.ReplaceAll(raw, "\r\n ", " ")
	lines := strings.Split(raw, "\n")
	for _, line := range lines {
		if strings.HasPrefix(strings.ToLower(strings.TrimSpace(line)), "references:") {
			value := strings.TrimSpace(strings.SplitN(line, ":", 2)[1])
			return strings.Fields(value)
		}
	}
	raw = string(bytes.TrimSpace([]byte(raw)))
	if raw == "" {
		return nil
	}
	return strings.Fields(raw)
}

// hasAttachmentInStructure 检查 BodyStructure 是否包含附件.
func hasAttachmentInStructure(bs *imap.BodyStructure) bool {
	found := false
	bs.Walk(func(path []int, part *imap.BodyStructure) bool {
		if filename, err := part.Filename(); err == nil && filename != "" {
			found = true
			return false
		}
		return true
	})
	return found
}

// addressListToString 将 Address 列表转为单个字符串(取第一个).
func addressListToString(addrs []*imap.Address) string {
	if len(addrs) == 0 {
		return ""
	}
	return formatAddress(addrs[0])
}

// addressListToStrings 将 Address 列表转为字符串列表.
func addressListToStrings(addrs []*imap.Address) []string {
	if len(addrs) == 0 {
		return nil
	}
	result := make([]string, len(addrs))
	for i, a := range addrs {
		result[i] = formatAddress(a)
	}
	return result
}

// formatAddress 格式化单个邮件地址.
func formatAddress(a *imap.Address) string {
	personal := decodeMIMEHeader(a.PersonalName)
	addr := a.MailboxName + "@" + a.HostName
	if personal != "" {
		return personal + " <" + addr + ">"
	}
	return addr
}

// decodeMIMEHeader 解码 MIME 编码的邮件头(=?UTF-8?B?...?= 或 =?UTF-8?Q?...?=).
func decodeMIMEHeader(s string) string {
	if s == "" {
		return ""
	}
	dec := new(mime.WordDecoder)
	decoded, err := dec.DecodeHeader(s)
	if err != nil {
		return s
	}
	return decoded
}

// containsCJK 判断字符串是否包含中日韩字符.
// 用于检测是否需要走本地内存过滤,规避 IMAP 服务端的中文 literal 兼容性问题.
func containsCJK(s string) bool {
	for _, r := range s {
		if unicode.Is(unicode.Han, r) || unicode.Is(unicode.Hiragana, r) ||
			unicode.Is(unicode.Katakana, r) || unicode.Is(unicode.Hangul, r) {
			return true
		}
	}
	return false
}

// matchLocalFilter 本地内存匹配邮件元数据(规避 IMAP 服务端中文搜索问题).
// 匹配范围:From / Subject / To / TextContent / Summary(小写包含).
// CJK 过滤字段(client 传来的 Keyword/From/Subject)只校验非空部分,避免重复拼接.
func matchLocalFilter(mm *MailMessage, req *ReceiveMailRequest) bool {
	keyword := strings.ToLower(req.Keyword)
	fromFilter := strings.ToLower(req.From)
	subjectFilter := strings.ToLower(req.Subject)

	haystack := strings.ToLower(strings.Join([]string{
		mm.From, mm.Subject, strings.Join(mm.To, " "),
		mm.TextContent, mm.HTMLContent, mm.Summary,
	}, " "))

	if keyword != "" && !strings.Contains(haystack, keyword) {
		return false
	}
	if fromFilter != "" && !strings.Contains(strings.ToLower(mm.From), fromFilter) {
		return false
	}
	if subjectFilter != "" && !strings.Contains(strings.ToLower(mm.Subject), subjectFilter) {
		return false
	}
	return true
}

// containsFlag 检查标志列表中是否包含指定标志.
func containsFlag(flags []string, flag string) bool {
	for _, f := range flags {
		if f == flag {
			return true
		}
	}
	return false
}

// reverseUids 反转 UID 切片(使最新邮件在前).
func reverseUids(uids []uint32) {
	for i, j := 0, len(uids)-1; i < j; i, j = i+1, j-1 {
		uids[i], uids[j] = uids[j], uids[i]
	}
}

// truncate 截断字符串到指定长度.
func truncate(s string, maxLen int) string {
	if s == "" {
		return ""
	}
	// 统计 rune 数,避免截断在多字节字符中间
	runes := []rune(s)
	if len(runes) > maxLen {
		return string(runes[:maxLen]) + "..."
	}
	return s
}
