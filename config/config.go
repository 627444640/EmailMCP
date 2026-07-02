// Package config 邮件 MCP 服务配置管理.
// 支持配置文件(.env)和环境变量两种方式,环境变量优先级更高.
// 配置文件放在可执行文件同目录下,文件名为 .env.
package config

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// SMTPConfig SMTP 发送服务器配置.
type SMTPConfig struct {
	Host     string `json:"host"`     // SMTP 服务器地址,例如 smtp.qq.com
	Port     int    `json:"port"`     // SMTP 服务器端口,SSL 通常 465,STARTTLS 通常 587
	Username string `json:"username"` // 发件人邮箱账号
	Password string `json:"-"`        // 发件人密码/授权码(不序列化到 JSON,防止泄露)
	From     string `json:"from"`     // 默认发件人显示地址(与 Username 不一致时使用)
	SSL      bool   `json:"ssl"`      // 是否启用 SSL(465 端口默认 true)
}

// IMAPConfig IMAP 接收服务器配置.
type IMAPConfig struct {
	Host     string `json:"host"`     // IMAP 服务器地址,例如 imap.qq.com
	Port     int    `json:"port"`     // IMAP 服务器端口,SSL 通常 993
	Username string `json:"username"` // 登录邮箱账号
	Password string `json:"-"`        // 登录密码/授权码(不序列化到 JSON,防止泄露)
	SSL      bool   `json:"ssl"`      // 是否启用 SSL(993 端口默认 true)
}

// Config 全局配置.
type Config struct {
	AccountID   string            `json:"accountId"`
	SMTP        SMTPConfig        `json:"smtp"`
	IMAP        IMAPConfig        `json:"imap"`
	Attachments AttachmentConfig  `json:"attachments"`
	Permissions PermissionConfig  `json:"permissions"`
	Index       IndexConfig       `json:"index"`
	envCache    map[string]string // 从 .env 文件加载的键值对(内部使用,不导出)
}

// AttachmentConfig controls where the local MCP may read/write attachment files.
type AttachmentConfig struct {
	DownloadDir     string   `json:"downloadDir"`
	AllowedSendDirs []string `json:"allowedSendDirs"`
}

// PermissionConfig controls which local MCP tools are exposed to Codex.
type PermissionConfig struct {
	Read                bool `json:"read"`
	Write               bool `json:"write"`
	Send                bool `json:"send"`
	DownloadAttachments bool `json:"downloadAttachments"`
}

// MailboxConfig is the non-secret desktop mailbox configuration.
type MailboxConfig struct {
	Email    string `json:"email"`
	SMTPHost string `json:"smtpHost"`
	SMTPPort int    `json:"smtpPort"`
	SMTPSSL  bool   `json:"smtpSsl"`
	SMTPFrom string `json:"smtpFrom,omitempty"`
	IMAPHost string `json:"imapHost"`
	IMAPPort int    `json:"imapPort"`
	IMAPSSL  bool   `json:"imapSsl"`
}

// FolderConfig is the non-secret desktop folder configuration.
type FolderConfig struct {
	AttachmentDownloadDir string   `json:"attachmentDownloadDir"`
	AllowedAttachmentDirs []string `json:"allowedAttachmentDirs"`
}

// LocalConfig is the desktop app configuration written to the user's config dir.
// It deliberately contains no mailbox authorization codes.
type LocalConfig struct {
	AccountID   string           `json:"accountId"`
	Mailbox     MailboxConfig    `json:"mailbox"`
	Folders     FolderConfig     `json:"folders"`
	Index       IndexConfig      `json:"index"`
	Permissions PermissionConfig `json:"permissions"`
	Autostart   bool             `json:"autostart"`
}

// IndexConfig controls the local SQLite mailbox index.
type IndexConfig struct {
	Enabled bool   `json:"enabled"`
	Path    string `json:"path,omitempty"`
}

// CommonEnvKeyPassword SMTP/IMAP 共用密码的环境变量名.
// 用户只需配置一次密码,SMTP 和 IMAP 都会读取此值.
const CommonEnvKeyPassword = "EMAIL_COMMON_PASSWORD"

// CommonEnvKeyUsername SMTP/IMAP 共用账号的环境变量名.
// 用户只需配置一次账号,SMTP 和 IMAP 都会读取此值.
const CommonEnvKeyUsername = "EMAIL_COMMON_USERNAME"

// Load 加载配置:优先读环境变量,其次读 .env 文件,最后用默认值.
// .env 文件放在可执行文件同目录下.
// envValues 存入 Config.envCache,避免全局变量竞争.
func Load() *Config {
	cfg := &Config{
		envCache: loadDotEnv(),
	}

	// 2. 读取共用账号/密码(优先级:专用变量 > 共用变量 > 默认值)
	commonUsername := cfg.getOr(CommonEnvKeyUsername, "")
	commonPassword := cfg.getOr(CommonEnvKeyPassword, "")

	// 3. SMTP 配置
	cfg.SMTP.Host = cfg.getOr("EMAIL_SMTP_HOST", "")
	cfg.SMTP.Port = cfg.getIntOr("EMAIL_SMTP_PORT", 465)
	cfg.SMTP.Username = cfg.getOr("EMAIL_SMTP_USERNAME", commonUsername)
	cfg.SMTP.Password = cfg.getOr("EMAIL_SMTP_PASSWORD", commonPassword)
	cfg.SMTP.From = cfg.getOr("EMAIL_SMTP_FROM", cfg.SMTP.Username)
	cfg.SMTP.SSL = cfg.getBoolOr("EMAIL_SMTP_SSL", true)

	// 4. IMAP 配置(账号/密码缺省时自动复用 SMTP 的,大多数邮箱 SMTP 和 IMAP 用同一套凭证)
	cfg.IMAP.Host = cfg.getOr("EMAIL_IMAP_HOST", "")
	cfg.IMAP.Port = cfg.getIntOr("EMAIL_IMAP_PORT", 993)
	cfg.IMAP.Username = cfg.getOr("EMAIL_IMAP_USERNAME", cfg.SMTP.Username)
	cfg.IMAP.Password = cfg.getOr("EMAIL_IMAP_PASSWORD", cfg.SMTP.Password)
	cfg.IMAP.SSL = cfg.getBoolOr("EMAIL_IMAP_SSL", true)

	cfg.Attachments.DownloadDir = cfg.getOr("EMAIL_MCP_ATTACHMENT_DOWNLOAD_DIR", "attachments")
	cfg.Attachments.AllowedSendDirs = splitCSV(cfg.getOr("EMAIL_MCP_ALLOWED_ATTACHMENT_DIRS", ""))
	cfg.Permissions = PermissionConfig{Read: true, Write: true, Send: true, DownloadAttachments: true}
	cfg.AccountID = cfg.getOr("EMAIL_MCP_ACCOUNT_ID", "default")
	cfg.Index = IndexConfig{Enabled: true, Path: cfg.getOr("EMAIL_MCP_INDEX_PATH", "")}

	return cfg
}

// loadDotEnv 从可执行文件同目录的 .env 文件加载配置.
// 格式: KEY=VALUE,以 # 开头的行为注释,空行忽略.
func loadDotEnv() map[string]string {
	values := make(map[string]string)

	// 获取可执行文件所在目录
	exePath, err := os.Executable()
	if err != nil {
		return values
	}
	envFile := filepath.Join(filepath.Dir(exePath), ".env")

	// 也尝试当前工作目录
	if _, err = os.Stat(envFile); err != nil {
		envFile = ".env"
	}

	data, err := os.ReadFile(envFile)
	if err != nil {
		// .env 文件不存在是正常的(用户可能只用环境变量)
		return values
	}

	scanner := bufio.NewScanner(strings.NewReader(string(data)))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		// 跳过空行和注释
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		// 解析 KEY=VALUE
		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			continue
		}
		key := strings.TrimSpace(parts[0])
		value := strings.TrimSpace(parts[1])
		// 去掉引号
		if (strings.HasPrefix(value, `"`) && strings.HasSuffix(value, `"`)) ||
			(strings.HasPrefix(value, "'") && strings.HasSuffix(value, "'")) {
			value = value[1 : len(value)-1]
		}
		values[key] = value
	}

	return values
}

// getOr 读取配置值:环境变量优先,其次 .env 文件,最后默认值.
func (c *Config) getOr(key, defaultVal string) string {
	// 环境变量优先
	if v := os.Getenv(key); v != "" {
		return v
	}
	// 其次 .env 文件
	if v, ok := c.envCache[key]; ok && v != "" {
		return v
	}
	return defaultVal
}

// getIntOr 读取配置值并解析为 int.
func (c *Config) getIntOr(key string, defaultVal int) int {
	v := c.getOr(key, "")
	if v == "" {
		return defaultVal
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return defaultVal
	}
	return n
}

// getBoolOr 读取配置值并解析为 bool.
func (c *Config) getBoolOr(key string, defaultVal bool) bool {
	v := c.getOr(key, "")
	if v == "" {
		return defaultVal
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		return defaultVal
	}
	return b
}

// ValidateSMTP 校验 SMTP 配置是否完整(发送邮件前调用).
func (c *Config) ValidateSMTP() error {
	if c.SMTP.Host == "" {
		return fmt.Errorf("SMTP 主机未配置,请在 .env 文件或环境变量中设置 EMAIL_SMTP_HOST")
	}
	if c.SMTP.Username == "" {
		return fmt.Errorf("SMTP 用户名未配置,请设置 EMAIL_SMTP_USERNAME")
	}
	if c.SMTP.Password == "" {
		return fmt.Errorf("SMTP 密码未配置,请设置 EMAIL_SMTP_PASSWORD")
	}
	return nil
}

// ValidateIMAP 校验 IMAP 配置是否完整(接收邮件前调用).
func (c *Config) ValidateIMAP() error {
	if c.IMAP.Host == "" {
		return fmt.Errorf("IMAP 主机未配置,请在 .env 文件或环境变量中设置 EMAIL_IMAP_HOST")
	}
	if c.IMAP.Username == "" {
		return fmt.Errorf("IMAP 用户名未配置,请设置 EMAIL_IMAP_USERNAME")
	}
	if c.IMAP.Password == "" {
		return fmt.Errorf("IMAP 密码未配置,请设置 EMAIL_IMAP_PASSWORD")
	}
	return nil
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

// DefaultLocalConfig returns first-run desktop configuration defaults.
func DefaultLocalConfig() LocalConfig {
	return LocalConfig{
		AccountID: "default",
		Mailbox: MailboxConfig{
			SMTPPort: 465,
			SMTPSSL:  true,
			IMAPPort: 993,
			IMAPSSL:  true,
		},
		Permissions: PermissionConfig{
			Read:                true,
			Write:               true,
			Send:                true,
			DownloadAttachments: true,
		},
		Index: IndexConfig{Enabled: true},
	}
}

// DefaultLocalConfigPath returns the desktop app's user-level config path.
func DefaultLocalConfigPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "EmailMCP", "config.json"), nil
}

// DefaultIndexPath returns the default local SQLite index path.
func DefaultIndexPath() (string, error) {
	dir, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "EmailMCP", "mail-index.db"), nil
}

// SaveLocalConfig writes non-secret desktop configuration to disk.
func SaveLocalConfig(path string, cfg LocalConfig) error {
	if strings.TrimSpace(path) == "" {
		var err error
		path, err = DefaultLocalConfigPath()
		if err != nil {
			return err
		}
	}
	cfg = normalizeLocalConfig(cfg)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return os.WriteFile(path, data, 0o600)
}

// LoadLocalConfig reads non-secret desktop configuration from disk.
func LoadLocalConfig(path string) (LocalConfig, error) {
	if strings.TrimSpace(path) == "" {
		var err error
		path, err = DefaultLocalConfigPath()
		if err != nil {
			return LocalConfig{}, err
		}
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return LocalConfig{}, err
	}
	var cfg LocalConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return LocalConfig{}, err
	}
	missingSMTPSSL, missingIMAPSSL := missingLocalMailboxSSLFields(data)
	missingIndexEnabled := missingLocalIndexEnabledField(data)
	cfg = normalizeLocalConfig(cfg)
	if missingSMTPSSL {
		cfg.Mailbox.SMTPSSL = true
	}
	if missingIMAPSSL {
		cfg.Mailbox.IMAPSSL = true
	}
	if missingIndexEnabled {
		cfg.Index.Enabled = true
	}
	return cfg, nil
}

// ToRuntimeConfig converts local config plus secret values into service config.
func (c LocalConfig) ToRuntimeConfig(smtpPassword, imapPassword string) *Config {
	c = normalizeLocalConfig(c)
	return &Config{
		AccountID: c.AccountID,
		SMTP: SMTPConfig{
			Host:     c.Mailbox.SMTPHost,
			Port:     c.Mailbox.SMTPPort,
			Username: c.Mailbox.Email,
			Password: smtpPassword,
			From:     firstNonEmpty(c.Mailbox.SMTPFrom, c.Mailbox.Email),
			SSL:      c.Mailbox.SMTPSSL,
		},
		IMAP: IMAPConfig{
			Host:     c.Mailbox.IMAPHost,
			Port:     c.Mailbox.IMAPPort,
			Username: c.Mailbox.Email,
			Password: imapPassword,
			SSL:      c.Mailbox.IMAPSSL,
		},
		Attachments: AttachmentConfig{
			DownloadDir:     c.Folders.AttachmentDownloadDir,
			AllowedSendDirs: append([]string(nil), c.Folders.AllowedAttachmentDirs...),
		},
		Permissions: c.Permissions,
		Index:       c.Index,
	}
}

func normalizeLocalConfig(cfg LocalConfig) LocalConfig {
	if strings.TrimSpace(cfg.AccountID) == "" {
		cfg.AccountID = "default"
	}
	cfg.Mailbox.Email = strings.TrimSpace(cfg.Mailbox.Email)
	cfg.Mailbox.SMTPHost = strings.TrimSpace(cfg.Mailbox.SMTPHost)
	cfg.Mailbox.IMAPHost = strings.TrimSpace(cfg.Mailbox.IMAPHost)
	if cfg.Mailbox.SMTPPort == 0 {
		cfg.Mailbox.SMTPPort = 465
	}
	if cfg.Mailbox.IMAPPort == 0 {
		cfg.Mailbox.IMAPPort = 993
	}
	cfg.Folders.AttachmentDownloadDir = strings.TrimSpace(cfg.Folders.AttachmentDownloadDir)
	cleanDirs := make([]string, 0, len(cfg.Folders.AllowedAttachmentDirs))
	seen := make(map[string]bool)
	for _, dir := range cfg.Folders.AllowedAttachmentDirs {
		dir = strings.TrimSpace(dir)
		if dir == "" || seen[dir] {
			continue
		}
		seen[dir] = true
		cleanDirs = append(cleanDirs, dir)
	}
	cfg.Folders.AllowedAttachmentDirs = cleanDirs
	cfg.Index.Path = strings.TrimSpace(cfg.Index.Path)
	return cfg
}

func missingLocalIndexEnabledField(data []byte) bool {
	var root struct {
		Index map[string]json.RawMessage `json:"index"`
	}
	if err := json.Unmarshal(data, &root); err != nil {
		return true
	}
	if root.Index == nil {
		return true
	}
	_, ok := root.Index["enabled"]
	return !ok
}

func missingLocalMailboxSSLFields(data []byte) (smtpMissing, imapMissing bool) {
	var root struct {
		Mailbox map[string]json.RawMessage `json:"mailbox"`
	}
	if err := json.Unmarshal(data, &root); err != nil {
		return false, false
	}
	if root.Mailbox == nil {
		return true, true
	}
	_, hasSMTPSSL := root.Mailbox["smtpSsl"]
	_, hasIMAPSSL := root.Mailbox["imapSsl"]
	return !hasSMTPSSL, !hasIMAPSSL
}

func splitCSV(value string) []string {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	parts := strings.Split(value, ",")
	result := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			result = append(result, part)
		}
	}
	return result
}
