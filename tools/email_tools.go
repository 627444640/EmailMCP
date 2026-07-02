// Package tools MCP 工具注册层.
// 将邮件收发能力以 MCP Tool 方式暴露给 AI 客户端.
package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"email-mcp-service/config"
	"email-mcp-service/mailindex"
	"email-mcp-service/service"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// ResolvedConfig is the mailbox configuration authorized for one tool call.
type ResolvedConfig struct {
	Config         *config.Config
	UserID         int64
	Email          string
	OutputDir      string
	SendToSelfOnly bool
	ForceFrom      string
}

// ConfigResolver returns the mailbox configuration for a tool call.
type ConfigResolver func(ctx context.Context, tool string) (*ResolvedConfig, error)

// AuditFunc records a tool call outcome.
type AuditFunc func(ctx context.Context, userID int64, tool, status, detail string)

// RegisterEmailTools 注册所有邮件 MCP 工具到 MCP Server.
func RegisterEmailTools(s *server.MCPServer, cfg *config.Config) {
	RegisterLocalEmailTools(s, cfg)
}

// StaticConfigResolver always returns the same config, used by stdio mode.
func StaticConfigResolver(cfg *config.Config) ConfigResolver {
	return func(ctx context.Context, tool string) (*ResolvedConfig, error) {
		return &ResolvedConfig{Config: cfg, Email: cfg.SMTP.Username, OutputDir: cfg.Attachments.DownloadDir}, nil
	}
}

// RegisterLocalEmailTools registers tools according to local desktop permissions.
func RegisterLocalEmailTools(s *server.MCPServer, cfg *config.Config) {
	if cfg == nil {
		cfg = &config.Config{}
	}
	resolver := StaticConfigResolver(cfg)
	if cfg.Permissions.Send {
		registerSendEmail(s, resolver, nil)
	}
	if cfg.Permissions.Read {
		registerListFolders(s, resolver, nil)
		registerResolveSpecialFolders(s, resolver, nil)
		registerListEmails(s, resolver, nil)
		registerListEmailsV2(s, resolver, nil)
		registerSearchEmails(s, resolver, nil)
		registerSearchAllFolders(s, resolver, nil)
		registerPreviewOrganizePlan(s, resolver, nil)
		registerGetEmail(s, resolver, nil)
	}
	if cfg.Permissions.DownloadAttachments {
		registerDownloadEmailAttachments(s, resolver, nil)
	}
	if cfg.Permissions.Write {
		registerCreateFolder(s, resolver, nil)
		registerSetEmailReadStatus(s, resolver, nil)
		registerMoveEmail(s, resolver, nil)
		registerDeleteEmail(s, resolver, nil)
		registerBulkMoveEmails(s, resolver, nil)
		registerBulkDeleteEmails(s, resolver, nil)
		registerBulkSetEmailReadStatus(s, resolver, nil)
		registerArchiveEmails(s, resolver, nil)
		registerApplyOrganizePlan(s, resolver, nil)
	}
}

// RegisterEmailToolsWithResolver 注册所有邮件 MCP 工具到 MCP Server.
func RegisterEmailToolsWithResolver(s *server.MCPServer, resolver ConfigResolver, audit AuditFunc) {
	registerSendEmail(s, resolver, audit)
	registerListFolders(s, resolver, audit)
	registerResolveSpecialFolders(s, resolver, audit)
	registerListEmails(s, resolver, audit)
	registerListEmailsV2(s, resolver, audit)
	registerSearchEmails(s, resolver, audit)
	registerSearchAllFolders(s, resolver, audit)
	registerPreviewOrganizePlan(s, resolver, audit)
	registerGetEmail(s, resolver, audit)
	registerDownloadEmailAttachments(s, resolver, audit)
	registerCreateFolder(s, resolver, audit)
	registerSetEmailReadStatus(s, resolver, audit)
	registerMoveEmail(s, resolver, audit)
	registerDeleteEmail(s, resolver, audit)
	registerBulkMoveEmails(s, resolver, audit)
	registerBulkDeleteEmails(s, resolver, audit)
	registerBulkSetEmailReadStatus(s, resolver, audit)
	registerArchiveEmails(s, resolver, audit)
	registerApplyOrganizePlan(s, resolver, audit)
}

// registerSendEmail 注册 sendEmail 工具.
func registerSendEmail(s *server.MCPServer, resolver ConfigResolver, audit AuditFunc) {
	const toolName = "sendEmail"
	tool := mcp.NewTool("sendEmail",
		mcp.WithDescription("使用本机已配置邮箱发送一封邮件。支持纯文本/HTML正文、抄送、密送和白名单目录内的本地附件。"),
		mcp.WithString("to", mcp.Required(), mcp.Description("收件人邮箱地址,多个用逗号分隔")),
		mcp.WithString("subject", mcp.Required(), mcp.Description("邮件主题")),
		mcp.WithString("content", mcp.Required(), mcp.Description("邮件正文")),
		mcp.WithString("contentType", mcp.Description("正文类型,默认 text/plain,支持 text/html")),
		mcp.WithString("cc", mcp.Description("抄送邮箱地址,多个用逗号分隔")),
		mcp.WithString("bcc", mcp.Description("密送邮箱地址,多个用逗号分隔")),
		mcp.WithString("attachmentPaths", mcp.Description("附件本地文件绝对路径,多个用逗号分隔")),
		mcp.WithString("from", mcp.Description("发件人显示地址,缺省等于 username")),
	)

	handler := func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		resolved, err := resolver(ctx, toolName)
		if err != nil {
			auditTool(ctx, audit, 0, toolName, "failure", err.Error())
			return mcp.NewToolResultError(fmt.Sprintf("权限校验失败: %v", err)), nil
		}
		req := &service.SendMailRequest{
			To:              splitCSV(request.GetString("to", "")),
			Subject:         request.GetString("subject", ""),
			Content:         request.GetString("content", ""),
			ContentType:     request.GetString("contentType", ""),
			CC:              splitCSV(request.GetString("cc", "")),
			BCC:             splitCSV(request.GetString("bcc", "")),
			AttachmentPaths: splitCSV(request.GetString("attachmentPaths", "")),
			From:            request.GetString("from", ""),
		}
		if resolved.SendToSelfOnly {
			if len(req.CC) > 0 || len(req.BCC) > 0 || len(req.To) != 1 || !sameEmail(req.To[0], resolved.Email) {
				err := fmt.Errorf("sendEmail is restricted to the configured owner email")
				auditTool(ctx, audit, resolved.UserID, toolName, "failure", err.Error())
				return mcp.NewToolResultError(err.Error()), nil
			}
		}
		if resolved.ForceFrom != "" {
			req.From = resolved.ForceFrom
		}

		result, err := service.SendEmail(resolved.Config, req)
		if err != nil {
			auditTool(ctx, audit, resolved.UserID, toolName, "failure", err.Error())
			return mcp.NewToolResultError(fmt.Sprintf("发送邮件失败: %v", err)), nil
		}

		auditTool(ctx, audit, resolved.UserID, toolName, "success", fmt.Sprintf("to=%s subject=%s", strings.Join(req.To, ","), req.Subject))
		jsonBytes, _ := json.Marshal(result)
		return mcp.NewToolResultText(string(jsonBytes)), nil
	}

	s.AddTool(tool, handler)
}

// registerDownloadEmailAttachments 注册 downloadEmailAttachments 工具.
func registerDownloadEmailAttachments(s *server.MCPServer, resolver ConfigResolver, audit AuditFunc) {
	const toolName = "downloadEmailAttachments"
	tool := mcp.NewTool("downloadEmailAttachments",
		mcp.WithDescription("按 UID 下载当前用户自己邮箱中的邮件附件,保存到本机配置的附件下载目录。"),
		mcp.WithNumber("uid", mcp.Required(), mcp.Description("邮件 UID,来自 listEmails 返回结果")),
		mcp.WithString("folder", mcp.Description("邮箱文件夹,默认 INBOX")),
	)

	handler := func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		resolved, err := resolver(ctx, toolName)
		if err != nil {
			auditTool(ctx, audit, 0, toolName, "failure", err.Error())
			return mcp.NewToolResultError(fmt.Sprintf("权限校验失败: %v", err)), nil
		}
		result, err := service.DownloadEmailAttachments(resolved.Config, &service.AttachmentDownloadRequest{
			UID:       uint32(request.GetInt("uid", 0)),
			Folder:    request.GetString("folder", ""),
			OutputDir: resolved.OutputDir,
		})
		if err != nil {
			auditTool(ctx, audit, resolved.UserID, toolName, "failure", err.Error())
			return mcp.NewToolResultError(fmt.Sprintf("下载附件失败: %v", err)), nil
		}
		auditTool(ctx, audit, resolved.UserID, toolName, "success", fmt.Sprintf("uid=%d count=%d", result.UID, len(result.Attachments)))
		jsonBytes, _ := json.Marshal(result)
		return mcp.NewToolResultText(string(jsonBytes)), nil
	}

	s.AddTool(tool, handler)
}

// registerListFolders 注册 listFolders 工具.
func registerListFolders(s *server.MCPServer, resolver ConfigResolver, audit AuditFunc) {
	const toolName = "listFolders"
	tool := mcp.NewTool(toolName,
		mcp.WithDescription("列出当前本机已配置邮箱账号下所有可访问文件夹,包含可获取到的邮件总数和未读数。"),
	)

	handler := func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		resolved, err := resolver(ctx, toolName)
		if err != nil {
			auditTool(ctx, audit, 0, toolName, "failure", err.Error())
			return mcp.NewToolResultError(fmt.Sprintf("权限校验失败: %v", err)), nil
		}
		result, err := service.ListFolders(resolved.Config)
		if err != nil {
			auditTool(ctx, audit, resolved.UserID, toolName, "failure", err.Error())
			return mcp.NewToolResultError(fmt.Sprintf("列出文件夹失败: %v", err)), nil
		}
		auditTool(ctx, audit, resolved.UserID, toolName, "success", fmt.Sprintf("count=%d", len(result.Folders)))
		jsonBytes, _ := json.Marshal(result)
		return mcp.NewToolResultText(string(jsonBytes)), nil
	}

	s.AddTool(tool, handler)
}

func registerResolveSpecialFolders(s *server.MCPServer, resolver ConfigResolver, audit AuditFunc) {
	const toolName = "resolveSpecialFolders"
	tool := mcp.NewTool(toolName, mcp.WithDescription("识别当前邮箱中的 inbox/sent/drafts/trash/junk/archive 系统文件夹,只读不创建。"))
	handler := func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		resolved, err := resolver(ctx, toolName)
		if err != nil {
			auditTool(ctx, audit, 0, toolName, "failure", err.Error())
			return mcp.NewToolResultError(fmt.Sprintf("权限校验失败: %v", err)), nil
		}
		result, err := service.ResolveSpecialFolders(resolved.Config)
		if err != nil {
			auditTool(ctx, audit, resolved.UserID, toolName, "failure", err.Error())
			return mcp.NewToolResultError(fmt.Sprintf("识别系统文件夹失败: %v", err)), nil
		}
		jsonBytes, _ := json.Marshal(result)
		return mcp.NewToolResultText(string(jsonBytes)), nil
	}
	s.AddTool(tool, handler)
}

// registerListEmails 注册 listEmails 工具.
func registerListEmails(s *server.MCPServer, resolver ConfigResolver, audit AuditFunc) {
	const toolName = "listEmails"
	tool := mcp.NewTool("listEmails",
		mcp.WithDescription("列出/搜索邮件。支持按文件夹、关键字、发件人、主题、是否已读等条件过滤,默认返回最近 10 封邮件摘要。"),
		mcp.WithString("folder", mcp.Description("邮箱文件夹,默认 INBOX")),
		mcp.WithNumber("limit", mcp.Description("返回最大条数,默认 10,最大 100")),
		mcp.WithString("keyword", mcp.Description("关键字模糊匹配(主题/发件人/正文)")),
		mcp.WithString("from", mcp.Description("发件人邮箱精确匹配")),
		mcp.WithString("subject", mcp.Description("主题包含的关键字")),
		mcp.WithBoolean("unreadOnly", mcp.Description("是否仅未读,默认 false")),
		mcp.WithString("view", mcp.Description("视图,SUMMARY(摘要) 或 FULL(完整),默认 SUMMARY")),
	)

	handler := func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		resolved, err := resolver(ctx, toolName)
		if err != nil {
			auditTool(ctx, audit, 0, toolName, "failure", err.Error())
			return mcp.NewToolResultError(fmt.Sprintf("权限校验失败: %v", err)), nil
		}
		req := &service.ReceiveMailRequest{
			Folder:  request.GetString("folder", ""),
			Limit:   request.GetInt("limit", 0),
			Keyword: request.GetString("keyword", ""),
			From:    request.GetString("from", ""),
			Subject: request.GetString("subject", ""),
			View:    request.GetString("view", ""),
		}
		// mcp-go 的 GetBool 只能返回 bool,无法区分"未传参"和"传了 false"
		// 此处用 GetArguments 判断参数是否存在
		args := request.GetArguments()
		if _, ok := args["unreadOnly"]; ok {
			v := request.GetBool("unreadOnly", false)
			req.UnreadOnly = &v
		}

		result, err := service.ListEmails(resolved.Config, req)
		if err != nil {
			auditTool(ctx, audit, resolved.UserID, toolName, "failure", err.Error())
			return mcp.NewToolResultError(fmt.Sprintf("拉取邮件失败: %v", err)), nil
		}

		auditTool(ctx, audit, resolved.UserID, toolName, "success", fmt.Sprintf("count=%d", len(result)))
		jsonBytes, _ := json.Marshal(result)
		return mcp.NewToolResultText(string(jsonBytes)), nil
	}

	s.AddTool(tool, handler)
}

// registerListEmailsV2 注册 listEmailsV2 工具.
func registerListEmailsV2(s *server.MCPServer, resolver ConfigResolver, audit AuditFunc) {
	const toolName = "listEmailsV2"
	tool := mcp.NewTool(toolName,
		mcp.WithDescription("稳定分页列出/搜索邮件。支持文件夹、limit/cursor、中文或英文关键词、发件人、主题、未读、ISO 8601 时间范围、稳定排序和 SUMMARY/FULL 视图。"),
		mcp.WithString("folder", mcp.Description("邮箱文件夹,默认 INBOX")),
		mcp.WithNumber("limit", mcp.Description("返回最大条数,默认 50,最大 200")),
		mcp.WithString("cursor", mcp.Description("上一页返回的 opaque cursor,第一页不传")),
		mcp.WithString("keyword", mcp.Description("关键词,本地模糊匹配 subject/from/textBody/htmlBody,支持中文和英文大小写不敏感")),
		mcp.WithString("from", mcp.Description("发件人过滤,大小写不敏感包含匹配")),
		mcp.WithString("subject", mcp.Description("主题过滤,大小写不敏感包含匹配")),
		mcp.WithBoolean("unreadOnly", mcp.Description("是否仅未读,默认 false")),
		mcp.WithString("since", mcp.Description("ISO 8601 起始时间,包含该时间点,例如 2026-06-01T00:00:00+08:00")),
		mcp.WithString("before", mcp.Description("ISO 8601 结束时间,不包含该时间点,例如 2026-07-01T00:00:00+08:00")),
		mcp.WithString("sort", mcp.Description("排序: date_desc/date_asc/uid_desc/uid_asc,默认 date_desc")),
		mcp.WithString("view", mcp.Description("视图: SUMMARY 或 FULL,默认 SUMMARY")),
	)

	handler := func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		resolved, err := resolver(ctx, toolName)
		if err != nil {
			auditTool(ctx, audit, 0, toolName, "failure", err.Error())
			return mcp.NewToolResultError(fmt.Sprintf("权限校验失败: %v", err)), nil
		}
		req := &service.ListEmailsV2Request{
			Folder:  request.GetString("folder", ""),
			Limit:   request.GetInt("limit", 0),
			Cursor:  request.GetString("cursor", ""),
			Keyword: request.GetString("keyword", ""),
			From:    request.GetString("from", ""),
			Subject: request.GetString("subject", ""),
			Since:   request.GetString("since", ""),
			Before:  request.GetString("before", ""),
			Sort:    request.GetString("sort", ""),
			View:    request.GetString("view", ""),
		}
		args := request.GetArguments()
		if _, ok := args["unreadOnly"]; ok {
			v := request.GetBool("unreadOnly", false)
			req.UnreadOnly = &v
		}

		result, err := service.ListEmailsV2(resolved.Config, req)
		if err != nil {
			auditTool(ctx, audit, resolved.UserID, toolName, "failure", err.Error())
			return mcp.NewToolResultError(fmt.Sprintf("拉取邮件失败: %v", err)), nil
		}

		auditTool(ctx, audit, resolved.UserID, toolName, "success", fmt.Sprintf("count=%d hasMore=%t", len(result.Items), result.HasMore))
		jsonBytes, _ := json.Marshal(result)
		return mcp.NewToolResultText(string(jsonBytes)), nil
	}

	s.AddTool(tool, handler)
}

func registerSearchAllFolders(s *server.MCPServer, resolver ConfigResolver, audit AuditFunc) {
	const toolName = "searchAllFolders"
	tool := mcp.NewTool(toolName,
		mcp.WithDescription("跨所有可访问文件夹搜索邮件,参数同 listEmailsV2 但不接收 folder,返回项包含 folder。"),
		mcp.WithNumber("limit", mcp.Description("返回最大条数,默认 50,最大 200")),
		mcp.WithString("cursor", mcp.Description("上一页返回的 opaque cursor,第一页不传")),
		mcp.WithString("keyword", mcp.Description("关键词,匹配 subject/from/textBody/htmlBody")),
		mcp.WithString("from", mcp.Description("发件人过滤")),
		mcp.WithString("subject", mcp.Description("主题过滤")),
		mcp.WithBoolean("unreadOnly", mcp.Description("是否仅未读")),
		mcp.WithString("since", mcp.Description("ISO 8601 起始时间")),
		mcp.WithString("before", mcp.Description("ISO 8601 结束时间")),
		mcp.WithString("sort", mcp.Description("date_desc/date_asc/uid_desc/uid_asc")),
		mcp.WithString("view", mcp.Description("SUMMARY 或 FULL")),
	)
	handler := func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		resolved, err := resolver(ctx, toolName)
		if err != nil {
			auditTool(ctx, audit, 0, toolName, "failure", err.Error())
			return mcp.NewToolResultError(fmt.Sprintf("权限校验失败: %v", err)), nil
		}
		req := &service.SearchAllFoldersRequest{Limit: request.GetInt("limit", 0), Cursor: request.GetString("cursor", ""), Keyword: request.GetString("keyword", ""), From: request.GetString("from", ""), Subject: request.GetString("subject", ""), Since: request.GetString("since", ""), Before: request.GetString("before", ""), Sort: request.GetString("sort", ""), View: request.GetString("view", "")}
		if _, ok := request.GetArguments()["unreadOnly"]; ok {
			v := request.GetBool("unreadOnly", false)
			req.UnreadOnly = &v
		}
		result, err := service.SearchAllFolders(resolved.Config, req)
		if err != nil {
			auditTool(ctx, audit, resolved.UserID, toolName, "failure", err.Error())
			return mcp.NewToolResultError(fmt.Sprintf("跨文件夹搜索失败: %v", err)), nil
		}
		jsonBytes, _ := json.Marshal(result)
		return mcp.NewToolResultText(string(jsonBytes)), nil
	}
	s.AddTool(tool, handler)
}

func registerSearchEmails(s *server.MCPServer, resolver ConfigResolver, audit AuditFunc) {
	const toolName = "searchEmails"
	tool := mcp.NewTool(toolName,
		mcp.WithDescription("从本地 SQLite 邮件索引中快速搜索邮件。不会访问 IMAP；请先运行 index sync 或通过 GUI 同步索引。"),
		mcp.WithString("folder", mcp.Description("邮箱文件夹,为空则搜索全部已索引文件夹")),
		mcp.WithNumber("limit", mcp.Description("返回最大条数,默认 50,最大 200")),
		mcp.WithString("cursor", mcp.Description("上一页返回的 opaque cursor,第一页不传")),
		mcp.WithString("keyword", mcp.Description("关键词,匹配 subject/from/textBody/htmlBody/summary,支持中文和英文大小写不敏感")),
		mcp.WithString("from", mcp.Description("发件人过滤")),
		mcp.WithString("subject", mcp.Description("主题过滤")),
		mcp.WithBoolean("unreadOnly", mcp.Description("是否仅未读")),
		mcp.WithString("since", mcp.Description("ISO 8601 起始时间")),
		mcp.WithString("before", mcp.Description("ISO 8601 结束时间")),
		mcp.WithString("sort", mcp.Description("date_desc/date_asc/uid_desc/uid_asc")),
		mcp.WithString("view", mcp.Description("SUMMARY 或 FULL")),
	)
	handler := func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		resolved, err := resolver(ctx, toolName)
		if err != nil {
			auditTool(ctx, audit, 0, toolName, "failure", err.Error())
			return mcp.NewToolResultError(fmt.Sprintf("权限校验失败: %v", err)), nil
		}
		indexPath, err := resolveToolIndexPath(resolved.Config)
		if err != nil {
			auditTool(ctx, audit, resolved.UserID, toolName, "failure", err.Error())
			return mcp.NewToolResultError(err.Error()), nil
		}
		store, err := mailindex.Open(indexPath)
		if err != nil {
			auditTool(ctx, audit, resolved.UserID, toolName, "failure", err.Error())
			return mcp.NewToolResultError(fmt.Sprintf("打开本地索引失败: %v", err)), nil
		}
		defer store.Close()
		if err := store.Init(ctx); err != nil {
			auditTool(ctx, audit, resolved.UserID, toolName, "failure", err.Error())
			return mcp.NewToolResultError(fmt.Sprintf("初始化本地索引失败: %v", err)), nil
		}
		query := mailindex.Query{
			AccountID: firstNonEmptyLocal(resolved.Config.AccountID, "default"),
			Folder:    request.GetString("folder", ""),
			Limit:     request.GetInt("limit", 0),
			Cursor:    request.GetString("cursor", ""),
			Keyword:   request.GetString("keyword", ""),
			From:      request.GetString("from", ""),
			Subject:   request.GetString("subject", ""),
			Since:     request.GetString("since", ""),
			Before:    request.GetString("before", ""),
			Sort:      request.GetString("sort", ""),
			View:      request.GetString("view", ""),
		}
		if _, ok := request.GetArguments()["unreadOnly"]; ok {
			v := request.GetBool("unreadOnly", false)
			query.UnreadOnly = &v
		}
		result, err := store.Search(ctx, query)
		if err != nil {
			auditTool(ctx, audit, resolved.UserID, toolName, "failure", err.Error())
			return mcp.NewToolResultError(fmt.Sprintf("搜索本地索引失败: %v", err)), nil
		}
		auditTool(ctx, audit, resolved.UserID, toolName, "success", fmt.Sprintf("count=%d hasMore=%t", len(result.Items), result.HasMore))
		jsonBytes, _ := json.Marshal(result)
		return mcp.NewToolResultText(string(jsonBytes)), nil
	}
	s.AddTool(tool, handler)
}

// registerGetEmail 注册 getEmail 工具.
func registerGetEmail(s *server.MCPServer, resolver ConfigResolver, audit AuditFunc) {
	const toolName = "getEmail"
	tool := mcp.NewTool("getEmail",
		mcp.WithDescription("按 UID 读取单封邮件的完整正文与附件信息。"),
		mcp.WithNumber("uid", mcp.Required(), mcp.Description("邮件 UID,来自 listEmails 返回结果")),
		mcp.WithString("folder", mcp.Description("邮箱文件夹,默认 INBOX")),
		mcp.WithBoolean("markAsRead", mcp.Description("读取后是否标记为已读,默认 false")),
	)

	handler := func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		resolved, err := resolver(ctx, toolName)
		if err != nil {
			auditTool(ctx, audit, 0, toolName, "failure", err.Error())
			return mcp.NewToolResultError(fmt.Sprintf("权限校验失败: %v", err)), nil
		}
		req := &service.ReceiveMailRequest{
			UID:    uint32(request.GetInt("uid", 0)),
			Folder: request.GetString("folder", ""),
			View:   "FULL",
		}
		args := request.GetArguments()
		if _, ok := args["markAsRead"]; ok {
			v := request.GetBool("markAsRead", false)
			req.MarkAsRead = &v
		}

		result, err := service.GetEmailByUID(resolved.Config, req)
		if err != nil {
			auditTool(ctx, audit, resolved.UserID, toolName, "failure", err.Error())
			return mcp.NewToolResultError(fmt.Sprintf("读取邮件详情失败: %v", err)), nil
		}
		if result == nil {
			auditTool(ctx, audit, resolved.UserID, toolName, "success", "not_found")
			return mcp.NewToolResultText("未找到 UID 对应的邮件"), nil
		}

		auditTool(ctx, audit, resolved.UserID, toolName, "success", fmt.Sprintf("uid=%d", req.UID))
		jsonBytes, _ := json.Marshal(result)
		return mcp.NewToolResultText(string(jsonBytes)), nil
	}

	s.AddTool(tool, handler)
}

func registerPreviewOrganizePlan(s *server.MCPServer, resolver ConfigResolver, audit AuditFunc) {
	const toolName = "previewOrganizePlan"
	tool := mcp.NewTool(toolName,
		mcp.WithDescription("根据规则只读生成邮箱整理预案,返回 planId 和每条规则命中的邮件清单。"),
		mcp.WithArray("rules", mcp.Required(), mcp.Description("整理规则数组")),
		mcp.WithArray("folders", mcp.Description("可选文件夹列表,为空时搜索全部文件夹")),
		mcp.WithNumber("limitPerRule", mcp.Description("每条规则最大命中数,默认 200")),
	)
	handler := func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		resolved, err := resolver(ctx, toolName)
		if err != nil {
			auditTool(ctx, audit, 0, toolName, "failure", err.Error())
			return mcp.NewToolResultError(fmt.Sprintf("权限校验失败: %v", err)), nil
		}
		rules, err := parseOrganizeRules(request.GetArguments()["rules"])
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		folders := parseStringArray(request.GetArguments()["folders"])
		messages, err := collectOrganizePreviewMessages(resolved.Config, rules, folders, request.GetInt("limitPerRule", 0))
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("生成整理预案失败: %v", err)), nil
		}
		result := service.PreviewOrganizePlanFromMessages(nil, messages, rules, request.GetInt("limitPerRule", 0))
		jsonBytes, _ := json.Marshal(result)
		return mcp.NewToolResultText(string(jsonBytes)), nil
	}
	s.AddTool(tool, handler)
}

func registerCreateFolder(s *server.MCPServer, resolver ConfigResolver, audit AuditFunc) {
	const toolName = "createFolder"
	tool := mcp.NewTool(toolName,
		mcp.WithDescription("创建邮箱文件夹；已存在时返回成功并 alreadyExists=true。"),
		mcp.WithString("name", mcp.Required(), mcp.Description("文件夹名")),
		mcp.WithString("parentFolder", mcp.Description("父文件夹,可选")),
	)
	handler := func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		resolved, err := resolver(ctx, toolName)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("权限校验失败: %v", err)), nil
		}
		result, err := service.CreateFolder(resolved.Config, &service.CreateFolderRequest{Name: request.GetString("name", ""), ParentFolder: request.GetString("parentFolder", "")})
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("创建文件夹失败: %v", err)), nil
		}
		jsonBytes, _ := json.Marshal(result)
		return mcp.NewToolResultText(string(jsonBytes)), nil
	}
	s.AddTool(tool, handler)
}

func registerBulkMoveEmails(s *server.MCPServer, resolver ConfigResolver, audit AuditFunc) {
	registerBulkMailboxTool(s, resolver, audit, "bulkMoveEmails", "批量移动邮件,默认 dryRun=true。", service.MailboxActionMove)
}

func registerBulkDeleteEmails(s *server.MCPServer, resolver ConfigResolver, audit AuditFunc) {
	registerBulkMailboxTool(s, resolver, audit, "bulkDeleteEmails", "批量删除邮件,实际移动到垃圾箱,默认 dryRun=true。", service.MailboxActionDelete)
}

func registerBulkSetEmailReadStatus(s *server.MCPServer, resolver ConfigResolver, audit AuditFunc) {
	registerBulkMailboxTool(s, resolver, audit, "bulkSetEmailReadStatus", "批量标记邮件已读或未读。", service.MailboxActionSetReadStatus)
}

func registerArchiveEmails(s *server.MCPServer, resolver ConfigResolver, audit AuditFunc) {
	registerBulkMailboxTool(s, resolver, audit, "archiveEmails", "批量归档邮件,默认 dryRun=true。", service.MailboxActionMove)
}

func registerBulkMailboxTool(s *server.MCPServer, resolver ConfigResolver, audit AuditFunc, toolName, description string, action service.MailboxAction) {
	tool := mcp.NewTool(toolName,
		mcp.WithDescription(description),
		mcp.WithString("folder", mcp.Description("来源文件夹,默认 INBOX")),
		mcp.WithArray("uids", mcp.Required(), mcp.Description("邮件 UID 数组")),
		mcp.WithString("targetFolder", mcp.Description("目标文件夹")),
		mcp.WithString("trashFolder", mcp.Description("垃圾箱文件夹")),
		mcp.WithString("archiveFolder", mcp.Description("归档文件夹")),
		mcp.WithBoolean("read", mcp.Description("bulkSetEmailReadStatus 使用")),
		mcp.WithBoolean("dryRun", mcp.Description("是否预演,移动/删除/归档默认 true")),
	)
	handler := func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		resolved, err := resolver(ctx, toolName)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("权限校验失败: %v", err)), nil
		}
		uids, err := parseUIDs(request.GetArguments()["uids"])
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		req := &service.BulkMailboxRequest{Folder: request.GetString("folder", ""), TargetFolder: request.GetString("targetFolder", ""), TrashFolder: request.GetString("trashFolder", ""), ArchiveFolder: request.GetString("archiveFolder", ""), UIDs: uids}
		args := request.GetArguments()
		if _, ok := args["dryRun"]; ok {
			v := request.GetBool("dryRun", false)
			req.DryRun = &v
		}
		if _, ok := args["read"]; ok {
			v := request.GetBool("read", false)
			req.Read = &v
		}
		if toolName == "bulkSetEmailReadStatus" && req.Read == nil {
			return mcp.NewToolResultError("read 不能为空"), nil
		}
		var result *service.BulkMailboxResult
		switch toolName {
		case "bulkDeleteEmails":
			special, _ := service.ResolveSpecialFolders(resolved.Config)
			if special == nil {
				special = &service.SpecialFolders{}
			}
			result, err = service.BulkDeleteEmails(resolved.Config, req, *special)
		case "bulkSetEmailReadStatus":
			result, err = service.BulkSetEmailReadStatus(resolved.Config, req)
		case "archiveEmails":
			special, _ := service.ResolveSpecialFolders(resolved.Config)
			if special == nil {
				special = &service.SpecialFolders{}
			}
			result, err = service.ArchiveEmails(resolved.Config, req, *special)
		default:
			if action == service.MailboxActionMove && req.TargetFolder == "" {
				return mcp.NewToolResultError("targetFolder 不能为空"), nil
			}
			result, err = service.BulkMoveEmails(resolved.Config, req)
		}
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		jsonBytes, _ := json.Marshal(result)
		return mcp.NewToolResultText(string(jsonBytes)), nil
	}
	s.AddTool(tool, handler)
}

func registerApplyOrganizePlan(s *server.MCPServer, resolver ConfigResolver, audit AuditFunc) {
	const toolName = "applyOrganizePlan"
	tool := mcp.NewTool(toolName,
		mcp.WithDescription("执行整理预案或显式动作列表,返回每封邮件结果和回滚信息。"),
		mcp.WithString("planId", mcp.Description("previewOrganizePlan 返回的 planId")),
		mcp.WithArray("actions", mcp.Description("显式动作列表")),
		mcp.WithBoolean("dryRun", mcp.Description("是否预演,默认 false")),
	)
	handler := func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		resolved, err := resolver(ctx, toolName)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("权限校验失败: %v", err)), nil
		}
		actions, err := parseOrganizeActions(request.GetArguments()["actions"])
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		req := &service.ApplyOrganizePlanRequest{PlanID: request.GetString("planId", ""), Actions: actions}
		if _, ok := request.GetArguments()["dryRun"]; ok {
			v := request.GetBool("dryRun", false)
			req.DryRun = &v
		}
		result, err := service.ApplyOrganizePlan(resolved.Config, nil, req)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		jsonBytes, _ := json.Marshal(result)
		return mcp.NewToolResultText(string(jsonBytes)), nil
	}
	s.AddTool(tool, handler)
}

func registerSetEmailReadStatus(s *server.MCPServer, resolver ConfigResolver, audit AuditFunc) {
	const toolName = "setEmailReadStatus"
	tool := mcp.NewTool(toolName,
		mcp.WithDescription("按 UID 标记邮件为已读或未读。"),
		mcp.WithNumber("uid", mcp.Required(), mcp.Description("邮件 UID,来自 listEmails 返回结果")),
		mcp.WithString("folder", mcp.Description("邮箱文件夹,默认 INBOX")),
		mcp.WithBoolean("read", mcp.Required(), mcp.Description("true 标记已读,false 标记未读")),
	)

	handler := func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		resolved, err := resolver(ctx, toolName)
		if err != nil {
			auditTool(ctx, audit, 0, toolName, "failure", err.Error())
			return mcp.NewToolResultError(fmt.Sprintf("权限校验失败: %v", err)), nil
		}
		read := request.GetBool("read", false)
		result, err := service.SetEmailReadStatus(resolved.Config, &service.MailboxWriteRequest{
			UID:    uint32(request.GetInt("uid", 0)),
			Folder: request.GetString("folder", ""),
			Read:   &read,
		})
		if err != nil {
			auditTool(ctx, audit, resolved.UserID, toolName, "failure", err.Error())
			return mcp.NewToolResultError(fmt.Sprintf("标记邮件失败: %v", err)), nil
		}
		auditTool(ctx, audit, resolved.UserID, toolName, "success", fmt.Sprintf("uid=%d read=%t", result.UID, read))
		jsonBytes, _ := json.Marshal(result)
		return mcp.NewToolResultText(string(jsonBytes)), nil
	}

	s.AddTool(tool, handler)
}

func registerMoveEmail(s *server.MCPServer, resolver ConfigResolver, audit AuditFunc) {
	const toolName = "moveEmail"
	tool := mcp.NewTool(toolName,
		mcp.WithDescription("按 UID 将邮件移动到已有文件夹。"),
		mcp.WithNumber("uid", mcp.Required(), mcp.Description("邮件 UID,来自 listEmails 返回结果")),
		mcp.WithString("folder", mcp.Description("来源邮箱文件夹,默认 INBOX")),
		mcp.WithString("targetFolder", mcp.Required(), mcp.Description("目标邮箱文件夹,必须已存在")),
	)

	handler := func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		resolved, err := resolver(ctx, toolName)
		if err != nil {
			auditTool(ctx, audit, 0, toolName, "failure", err.Error())
			return mcp.NewToolResultError(fmt.Sprintf("权限校验失败: %v", err)), nil
		}
		result, err := service.MoveEmail(resolved.Config, &service.MailboxWriteRequest{
			UID:          uint32(request.GetInt("uid", 0)),
			Folder:       request.GetString("folder", ""),
			TargetFolder: request.GetString("targetFolder", ""),
		})
		if err != nil {
			auditTool(ctx, audit, resolved.UserID, toolName, "failure", err.Error())
			return mcp.NewToolResultError(fmt.Sprintf("移动邮件失败: %v", err)), nil
		}
		auditTool(ctx, audit, resolved.UserID, toolName, "success", fmt.Sprintf("uid=%d target=%s", result.UID, result.TargetFolder))
		jsonBytes, _ := json.Marshal(result)
		return mcp.NewToolResultText(string(jsonBytes)), nil
	}

	s.AddTool(tool, handler)
}

func registerDeleteEmail(s *server.MCPServer, resolver ConfigResolver, audit AuditFunc) {
	const toolName = "deleteEmail"
	tool := mcp.NewTool(toolName,
		mcp.WithDescription("按 UID 删除邮件。第一版删除会移动到垃圾箱,不支持硬删除。"),
		mcp.WithNumber("uid", mcp.Required(), mcp.Description("邮件 UID,来自 listEmails 返回结果")),
		mcp.WithString("folder", mcp.Description("来源邮箱文件夹,默认 INBOX")),
		mcp.WithString("trashFolder", mcp.Description("垃圾箱文件夹,默认 Trash")),
	)

	handler := func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		resolved, err := resolver(ctx, toolName)
		if err != nil {
			auditTool(ctx, audit, 0, toolName, "failure", err.Error())
			return mcp.NewToolResultError(fmt.Sprintf("权限校验失败: %v", err)), nil
		}
		result, err := service.DeleteEmail(resolved.Config, &service.MailboxWriteRequest{
			UID:          uint32(request.GetInt("uid", 0)),
			Folder:       request.GetString("folder", ""),
			TargetFolder: request.GetString("trashFolder", ""),
		})
		if err != nil {
			auditTool(ctx, audit, resolved.UserID, toolName, "failure", err.Error())
			return mcp.NewToolResultError(fmt.Sprintf("删除邮件失败: %v", err)), nil
		}
		auditTool(ctx, audit, resolved.UserID, toolName, "success", fmt.Sprintf("uid=%d trash=%s", result.UID, result.TargetFolder))
		jsonBytes, _ := json.Marshal(result)
		return mcp.NewToolResultText(string(jsonBytes)), nil
	}

	s.AddTool(tool, handler)
}

// splitCSV 按逗号分割字符串并去除空白.
func splitCSV(s string) []string {
	if s == "" {
		return nil
	}
	var result []string
	for _, part := range strings.Split(s, ",") {
		trimmed := strings.TrimSpace(part)
		if trimmed != "" {
			result = append(result, trimmed)
		}
	}
	return result
}

func sameEmail(a, b string) bool {
	return strings.EqualFold(strings.TrimSpace(a), strings.TrimSpace(b))
}

func auditTool(ctx context.Context, audit AuditFunc, userID int64, tool, status, detail string) {
	if audit != nil {
		audit(ctx, userID, tool, status, detail)
	}
}

func parseUIDs(value any) ([]uint32, error) {
	var uids []uint32
	switch v := value.(type) {
	case nil:
		return nil, fmt.Errorf("uids 不能为空")
	case []uint32:
		uids = append(uids, v...)
	case []int:
		for _, item := range v {
			if item <= 0 {
				return nil, fmt.Errorf("uid 必须大于 0")
			}
			uids = append(uids, uint32(item))
		}
	case []any:
		for _, item := range v {
			uid, err := parseUID(item)
			if err != nil {
				return nil, err
			}
			uids = append(uids, uid)
		}
	case string:
		for _, part := range splitCSV(v) {
			uid, err := parseUID(part)
			if err != nil {
				return nil, err
			}
			uids = append(uids, uid)
		}
	default:
		uid, err := parseUID(v)
		if err != nil {
			return nil, fmt.Errorf("uids 必须是 UID 数组: %w", err)
		}
		uids = append(uids, uid)
	}
	if len(uids) == 0 {
		return nil, fmt.Errorf("uids 不能为空")
	}
	return uids, nil
}

func parseUID(value any) (uint32, error) {
	switch v := value.(type) {
	case uint32:
		if v == 0 {
			return 0, fmt.Errorf("uid 必须大于 0")
		}
		return v, nil
	case int:
		if v <= 0 {
			return 0, fmt.Errorf("uid 必须大于 0")
		}
		return uint32(v), nil
	case int64:
		if v <= 0 || v > int64(^uint32(0)) {
			return 0, fmt.Errorf("uid 超出范围")
		}
		return uint32(v), nil
	case float64:
		if v <= 0 || v != float64(uint32(v)) {
			return 0, fmt.Errorf("uid 必须是正整数")
		}
		return uint32(v), nil
	case json.Number:
		parsed, err := strconv.ParseUint(v.String(), 10, 32)
		if err != nil || parsed == 0 {
			return 0, fmt.Errorf("uid 必须是正整数")
		}
		return uint32(parsed), nil
	case string:
		parsed, err := strconv.ParseUint(strings.TrimSpace(v), 10, 32)
		if err != nil || parsed == 0 {
			return 0, fmt.Errorf("uid 必须是正整数")
		}
		return uint32(parsed), nil
	default:
		return 0, fmt.Errorf("不支持的 uid 类型 %T", value)
	}
}

func parseStringArray(value any) []string {
	switch v := value.(type) {
	case nil:
		return nil
	case []string:
		return compactStrings(v)
	case []any:
		result := make([]string, 0, len(v))
		for _, item := range v {
			if s, ok := item.(string); ok {
				result = append(result, s)
			}
		}
		return compactStrings(result)
	case string:
		return splitCSV(v)
	default:
		return nil
	}
}

func compactStrings(values []string) []string {
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			result = append(result, value)
		}
	}
	return result
}

func parseOrganizeRules(value any) ([]service.OrganizeRule, error) {
	if value == nil {
		return nil, fmt.Errorf("rules 不能为空")
	}
	var raw []byte
	var err error
	if s, ok := value.(string); ok {
		raw = []byte(s)
	} else {
		raw, err = json.Marshal(value)
		if err != nil {
			return nil, fmt.Errorf("rules 解析失败: %w", err)
		}
	}
	var rules []service.OrganizeRule
	if err := json.Unmarshal(raw, &rules); err != nil {
		return nil, fmt.Errorf("rules 必须是规则数组: %w", err)
	}
	if len(rules) == 0 {
		return nil, fmt.Errorf("rules 不能为空")
	}
	for i := range rules {
		if strings.TrimSpace(rules[i].RuleID) == "" {
			rules[i].RuleID = fmt.Sprintf("rule-%d", i+1)
		}
		if strings.TrimSpace(rules[i].Action) == "" {
			rules[i].Action = "move"
		}
	}
	return rules, nil
}

func parseOrganizeActions(value any) ([]service.OrganizeAction, error) {
	if value == nil {
		return nil, nil
	}
	var raw []byte
	var err error
	if s, ok := value.(string); ok {
		if strings.TrimSpace(s) == "" {
			return nil, nil
		}
		raw = []byte(s)
	} else {
		raw, err = json.Marshal(value)
		if err != nil {
			return nil, fmt.Errorf("actions 解析失败: %w", err)
		}
	}
	var actions []service.OrganizeAction
	if err := json.Unmarshal(raw, &actions); err != nil {
		return nil, fmt.Errorf("actions 必须是动作数组: %w", err)
	}
	return actions, nil
}

func collectOrganizePreviewMessages(cfg *config.Config, rules []service.OrganizeRule, folders []string, limitPerRule int) ([]service.MailMessage, error) {
	if limitPerRule <= 0 {
		limitPerRule = 200
	}
	if len(folders) == 0 {
		list, err := service.ListFolders(cfg)
		if err != nil {
			return nil, err
		}
		for _, folder := range list.Folders {
			folders = append(folders, folder.Path)
		}
	}
	var messages []service.MailMessage
	for _, folder := range folders {
		for _, rule := range rules {
			cursor := ""
			collectedForRule := 0
			for {
				req := &service.ListEmailsV2Request{
					Folder:     folder,
					Limit:      200,
					Cursor:     cursor,
					Keyword:    rule.Keyword,
					From:       firstNonEmptyLocal(rule.FromEquals, rule.FromContains),
					Subject:    rule.SubjectContains,
					UnreadOnly: rule.UnreadOnly,
					Since:      rule.Since,
					Before:     rule.Before,
					Sort:       "date_desc",
					View:       "SUMMARY",
				}
				page, err := service.ListEmailsV2(cfg, req)
				if err != nil {
					return nil, err
				}
				for _, item := range page.Items {
					item.Folder = folder
					messages = append(messages, item)
					collectedForRule++
					if collectedForRule >= limitPerRule {
						break
					}
				}
				if collectedForRule >= limitPerRule || !page.HasMore || page.NextCursor == "" {
					break
				}
				cursor = page.NextCursor
			}
		}
	}
	return messages, nil
}

func firstNonEmptyLocal(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func resolveToolIndexPath(cfg *config.Config) (string, error) {
	if cfg != nil && strings.TrimSpace(cfg.Index.Path) != "" {
		return strings.TrimSpace(cfg.Index.Path), nil
	}
	return config.DefaultIndexPath()
}
