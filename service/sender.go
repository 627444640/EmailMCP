// Package service 邮件收发服务实现.
package service

import (
	"bytes"
	"crypto/rand"
	"crypto/tls"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"log"
	"net"
	"net/smtp"
	"os"
	"path/filepath"
	"strings"
	"time"

	"email-mcp-service/config"
)

// isAttachmentPathAllowed 校验附件路径是否在用户配置的白名单目录内.
func isAttachmentPathAllowed(path string, allowedDirs []string) bool {
	if len(allowedDirs) == 0 {
		return false
	}
	absPath, err := filepath.Abs(path)
	if err != nil {
		return false
	}
	// 阻止读取系统敏感目录
	sensitiveDirs := []string{
		"C:\\Windows",
		"C:\\Program Files",
		"C:\\Program Files (x86)",
		"C:\\ProgramData",
		"/etc",
		"/root",
		"/var",
		"/usr",
		"/sbin",
		"/boot",
	}
	for _, dir := range sensitiveDirs {
		sensitiveAbs, err := filepath.Abs(dir)
		if err == nil && isWithinDir(absPath, sensitiveAbs) {
			return false
		}
	}
	// 检查是否在白名单内
	for _, dir := range allowedDirs {
		allowedAbs, err := filepath.Abs(dir)
		if err != nil {
			continue
		}
		if isWithinDir(absPath, allowedAbs) {
			return true
		}
	}
	return false
}

// SendMailRequest 发送邮件请求参数.
type SendMailRequest struct {
	Host            string   // SMTP 主机(可选,缺省取配置)
	Port            int      // SMTP 端口(可选)
	Username        string   // 发件人账号(可选)
	Password        string   // 发件人密码/授权码(可选)
	From            string   // 发件人显示地址(可选)
	To              []string // 收件人列表(必填)
	CC              []string // 抄送列表(可选)
	BCC             []string // 密送列表(可选)
	Subject         string   // 邮件主题(必填)
	Content         string   // 邮件正文(必填)
	ContentType     string   // 正文类型: text/plain 或 text/html(默认 text/plain)
	AttachmentPaths []string // 附件本地文件路径列表(可选)
}

// SendResult 发送结果.
type SendResult struct {
	Success            bool     `json:"success"`
	MessageID          string   `json:"messageId"`
	To                 []string `json:"to"`
	Subject            string   `json:"subject"`
	SkippedAttachments []string `json:"skippedAttachments,omitempty"` // 因路径不合法或文件不存在而跳过的附件列表
}

// SendEmail 发送邮件.
// 使用原生 net/smtp 构建 MIME 消息,
// 所有文本强制 UTF-8 + Base64 编码,彻底避免中文乱码.
func SendEmail(cfg *config.Config, req *SendMailRequest) (*SendResult, error) {
	// 1. 合并配置:请求参数优先,缺省回退到全局配置
	host := firstNonEmpty(req.Host, cfg.SMTP.Host)
	port := req.Port
	if port == 0 {
		port = cfg.SMTP.Port
	}
	username := firstNonEmpty(req.Username, cfg.SMTP.Username)
	password := firstNonEmpty(req.Password, cfg.SMTP.Password)
	from := firstNonEmpty(req.From, cfg.SMTP.From, username)
	useSSL := cfg.SMTP.SSL

	// 2. 参数校验(先处理错误分支,卫语句风格)
	if len(req.To) == 0 {
		return nil, fmt.Errorf("收件人(to)不能为空")
	}
	if req.Subject == "" {
		return nil, fmt.Errorf("邮件主题(subject)不能为空")
	}
	if req.Content == "" {
		return nil, fmt.Errorf("邮件正文(content)不能为空")
	}
	if host == "" {
		return nil, fmt.Errorf("SMTP 主机未配置,请在 .env 文件中设置 EMAIL_SMTP_HOST")
	}
	if username == "" {
		return nil, fmt.Errorf("SMTP 用户名未配置,请设置 EMAIL_SMTP_USERNAME 或 EMAIL_COMMON_USERNAME")
	}
	if password == "" {
		return nil, fmt.Errorf("SMTP 密码未配置,请设置 EMAIL_SMTP_PASSWORD 或 EMAIL_COMMON_PASSWORD")
	}

	// 3. 构建 MIME 消息
	contentType := req.ContentType
	if contentType == "" {
		contentType = "text/plain"
	}
	// ContentType 白名单校验,只允许 text/plain 和 text/html
	if contentType != "text/plain" && contentType != "text/html" {
		return nil, fmt.Errorf("不支持的正文类型: %s, 仅支持 text/plain 和 text/html", contentType)
	}

	var buf bytes.Buffer
	boundary := generateBoundary()

	// MIME 头
	buf.WriteString(fmt.Sprintf("From: %s\r\n", from))
	buf.WriteString(fmt.Sprintf("To: %s\r\n", strings.Join(req.To, ", ")))
	if len(req.CC) > 0 {
		buf.WriteString(fmt.Sprintf("Cc: %s\r\n", strings.Join(req.CC, ", ")))
	}
	// Subject 强制 UTF-8 Base64 编码(=?UTF-8?B?...?= 格式)
	// RFC 2047 规定每个编码行不超过 75 字节,超出需拆分为多个编码字
	buf.WriteString("Subject: ")
	buf.WriteString(encodeSubjectRFC2047(req.Subject))
	buf.WriteString("\r\n")
	buf.WriteString("MIME-Version: 1.0\r\n")

	hasAttachment := len(req.AttachmentPaths) > 0
	var skippedAttachments []string

	if hasAttachment {
		// multipart/mixed 包裹正文 + 附件
		buf.WriteString(fmt.Sprintf("Content-Type: multipart/mixed; boundary=\"%s\"\r\n", boundary))
		buf.WriteString("\r\n")

		// 正文段
		buf.WriteString(fmt.Sprintf("--%s\r\n", boundary))
		buf.WriteString(fmt.Sprintf("Content-Type: %s; charset=UTF-8\r\n", contentType))
		buf.WriteString("Content-Transfer-Encoding: base64\r\n")
		buf.WriteString("\r\n")
		buf.WriteString(chunkedBase64([]byte(req.Content)))
		buf.WriteString("\r\n")

		// 附件段
		for _, path := range req.AttachmentPaths {
			// 安全校验:阻止路径遍历和敏感目录读取
			if !isAttachmentPathAllowed(path, cfg.Attachments.AllowedSendDirs) {
				log.Printf("附件路径被拒绝(不在白名单内或属于敏感目录): %s", path)
				skippedAttachments = append(skippedAttachments, path)
				continue
			}
			data, err := os.ReadFile(path)
			if err != nil {
				// 单个附件缺失不阻塞,记录到跳过列表
				log.Printf("附件读取失败: %s, 错误: %v", path, err)
				skippedAttachments = append(skippedAttachments, path)
				continue
			}
			filename := filepath.Base(path)
			filenameEncoded := base64.StdEncoding.EncodeToString([]byte(filename))
			buf.WriteString(fmt.Sprintf("--%s\r\n", boundary))
			buf.WriteString(fmt.Sprintf("Content-Type: application/octet-stream; name=\"=?UTF-8?B?%s?=\"\r\n", filenameEncoded))
			buf.WriteString("Content-Transfer-Encoding: base64\r\n")
			buf.WriteString(fmt.Sprintf("Content-Disposition: attachment; filename=\"=?UTF-8?B?%s?=\"\r\n", filenameEncoded))
			buf.WriteString("\r\n")
			buf.WriteString(chunkedBase64(data))
			buf.WriteString("\r\n")
		}
		buf.WriteString(fmt.Sprintf("--%s--\r\n", boundary))
	} else {
		// 无附件,直接正文
		buf.WriteString(fmt.Sprintf("Content-Type: %s; charset=UTF-8\r\n", contentType))
		buf.WriteString("Content-Transfer-Encoding: base64\r\n")
		buf.WriteString("\r\n")
		buf.WriteString(chunkedBase64([]byte(req.Content)))
	}

	// 4. 发送
	auth := smtp.PlainAuth("", username, password, host)
	client, err := connectSMTPClient(host, port, useSSL)
	if err != nil {
		return nil, err
	}
	defer client.Close()

	// 认证
	if err = client.Auth(auth); err != nil {
		return nil, fmt.Errorf("SMTP 认证失败: %w", err)
	}

	// 设置发件人
	if err = client.Mail(from); err != nil {
		return nil, fmt.Errorf("SMTP Mail 失败: %w", err)
	}

	// 设置收件人(To + CC + BCC)
	allRecipients := make([]string, 0, len(req.To)+len(req.CC)+len(req.BCC))
	allRecipients = append(allRecipients, req.To...)
	allRecipients = append(allRecipients, req.CC...)
	allRecipients = append(allRecipients, req.BCC...)
	for _, rcpt := range allRecipients {
		if err = client.Rcpt(rcpt); err != nil {
			return nil, fmt.Errorf("SMTP Rcpt 失败(%s): %w", rcpt, err)
		}
	}

	// 发送正文
	wc, err := client.Data()
	if err != nil {
		return nil, fmt.Errorf("SMTP Data 失败: %w", err)
	}
	if _, err = wc.Write(buf.Bytes()); err != nil {
		return nil, fmt.Errorf("SMTP 写入失败: %w", err)
	}
	if err = wc.Close(); err != nil {
		return nil, fmt.Errorf("SMTP 关闭写入失败: %w", err)
	}

	// 退出(忽略 Quit 错误,邮件已投递)
	_ = client.Quit()

	// MessageID 为客户端生成的临时标识,非邮件服务器分配的真实 Message-ID
	return &SendResult{
		Success:            true,
		MessageID:          fmt.Sprintf("<%d@%s>", time.Now().UnixNano(), host),
		To:                 req.To,
		Subject:            req.Subject,
		SkippedAttachments: skippedAttachments,
	}, nil
}

// CheckSMTPConnection verifies SMTP connectivity and authentication without sending mail.
func CheckSMTPConnection(cfg *config.Config) error {
	if err := cfg.ValidateSMTP(); err != nil {
		return err
	}
	client, err := connectSMTPClient(cfg.SMTP.Host, cfg.SMTP.Port, cfg.SMTP.SSL)
	if err != nil {
		return err
	}
	defer client.Close()
	auth := smtp.PlainAuth("", cfg.SMTP.Username, cfg.SMTP.Password, cfg.SMTP.Host)
	if err := client.Auth(auth); err != nil {
		return fmt.Errorf("SMTP 认证失败: %w", err)
	}
	_ = client.Quit()
	return nil
}

func connectSMTPClient(host string, port int, useSSL bool) (*smtp.Client, error) {
	if port == 0 {
		if useSSL {
			port = 465
		} else {
			port = 587
		}
	}
	addr := fmt.Sprintf("%s:%d", host, port)
	dialer := &net.Dialer{Timeout: 10 * time.Second}

	if useSSL {
		// SSL 直连(465 端口)
		tlsConfig := &tls.Config{ServerName: host, MinVersion: tls.VersionTLS12}
		conn, dialErr := tls.DialWithDialer(dialer, "tcp", addr, tlsConfig)
		if dialErr != nil {
			return nil, fmt.Errorf("SMTP SSL 连接失败: %w", dialErr)
		}
		client, err := smtp.NewClient(conn, host)
		if err != nil {
			conn.Close()
			return nil, fmt.Errorf("SMTP 客户端创建失败: %w", err)
		}
		return client, nil
	}

	// STARTTLS(587 端口)
	conn, dialErr := dialer.Dial("tcp", addr)
	if dialErr != nil {
		return nil, fmt.Errorf("SMTP 连接失败: %w", dialErr)
	}
	client, err := smtp.NewClient(conn, host)
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("SMTP 客户端创建失败: %w", err)
	}
	tlsConfig := &tls.Config{ServerName: host, MinVersion: tls.VersionTLS12}
	if err = client.StartTLS(tlsConfig); err != nil {
		_ = client.Close()
		return nil, fmt.Errorf("SMTP STARTTLS 升级失败: %w", err)
	}
	return client, nil
}

// chunkedBase64 将字节编码为 Base64,每 76 字符插入 CRLF 换行(MIME 规范).
func chunkedBase64(data []byte) string {
	encoded := base64.StdEncoding.EncodeToString(data)
	var sb strings.Builder
	for i := 0; i < len(encoded); i += 76 {
		end := i + 76
		if end > len(encoded) {
			end = len(encoded)
		}
		sb.WriteString(encoded[i:end])
		if end < len(encoded) {
			sb.WriteString("\r\n")
		}
	}
	return sb.String()
}

// firstNonEmpty 返回第一个非空字符串.
func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

// generateBoundary 使用 crypto/rand 生成随机 MIME boundary,
// 避免基于时间的 boundary 被预测导致 MIME 注入攻击.
func generateBoundary() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		// crypto/rand 失败时回退到时间戳(极端情况)
		return fmt.Sprintf("----=_Part_%d", time.Now().UnixNano())
	}
	return "----=_Part_" + hex.EncodeToString(b)
}

// encodeSubjectRFC2047 将主题按 RFC 2047 规范编码为 UTF-8 Base64,
// 每个编码字不超过 75 字节(含 =?UTF-8?B?...?= 定界符).
func encodeSubjectRFC2047(subject string) string {
	encoded := base64.StdEncoding.EncodeToString([]byte(subject))
	// 每个编码字最大 75 字节,定界符占 12 字节(=?UTF-8?B?...?=),Base64 内容最多 63 字节
	maxChunk := 63
	if len(encoded) <= maxChunk {
		return "=?UTF-8?B?" + encoded + "?="
	}
	// 拆分为多个编码字,用 CRLF + 空格折叠长行
	var parts []string
	for i := 0; i < len(encoded); i += maxChunk {
		end := i + maxChunk
		if end > len(encoded) {
			end = len(encoded)
		}
		parts = append(parts, "=?UTF-8?B?"+encoded[i:end]+"?=")
	}
	return strings.Join(parts, "\r\n ")
}
