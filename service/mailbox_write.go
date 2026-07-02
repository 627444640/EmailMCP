package service

import (
	"fmt"

	"email-mcp-service/config"

	"github.com/emersion/go-imap"
)

// MailboxAction identifies a write operation.
type MailboxAction string

const (
	MailboxActionSetReadStatus MailboxAction = "setReadStatus"
	MailboxActionMove          MailboxAction = "move"
	MailboxActionDelete        MailboxAction = "delete"
)

// MailboxWriteRequest describes one IMAP write operation.
type MailboxWriteRequest struct {
	Action       MailboxAction `json:"action"`
	UID          uint32        `json:"uid"`
	Folder       string        `json:"folder"`
	TargetFolder string        `json:"targetFolder,omitempty"`
	Read         *bool         `json:"read,omitempty"`
	HardDelete   bool          `json:"hardDelete,omitempty"`
}

// MailboxWriteResult describes a completed write operation.
type MailboxWriteResult struct {
	Success      bool          `json:"success"`
	Action       MailboxAction `json:"action"`
	UID          uint32        `json:"uid"`
	Folder       string        `json:"folder"`
	TargetFolder string        `json:"targetFolder,omitempty"`
}

// SetEmailReadStatus marks one email read or unread.
func SetEmailReadStatus(cfg *config.Config, req *MailboxWriteRequest) (*MailboxWriteResult, error) {
	if req == nil {
		req = &MailboxWriteRequest{}
	}
	req.Action = MailboxActionSetReadStatus
	return executeMailboxWrite(cfg, req)
}

// MoveEmail moves one email to an existing mailbox folder.
func MoveEmail(cfg *config.Config, req *MailboxWriteRequest) (*MailboxWriteResult, error) {
	if req == nil {
		req = &MailboxWriteRequest{}
	}
	req.Action = MailboxActionMove
	return executeMailboxWrite(cfg, req)
}

// DeleteEmail moves one email to the trash folder. Hard delete is intentionally unsupported.
func DeleteEmail(cfg *config.Config, req *MailboxWriteRequest) (*MailboxWriteResult, error) {
	if req == nil {
		req = &MailboxWriteRequest{}
	}
	req.Action = MailboxActionDelete
	if req.TargetFolder == "" {
		req.TargetFolder = "Trash"
	}
	return executeMailboxWrite(cfg, req)
}

func validateMailboxWriteRequest(req *MailboxWriteRequest) error {
	if req == nil {
		return fmt.Errorf("request is required")
	}
	if req.UID == 0 {
		return fmt.Errorf("uid 不能为空")
	}
	if req.Folder == "" {
		req.Folder = "INBOX"
	}
	switch req.Action {
	case MailboxActionSetReadStatus:
		if req.Read == nil {
			return fmt.Errorf("read status is required")
		}
	case MailboxActionMove:
		if req.TargetFolder == "" {
			return fmt.Errorf("targetFolder 不能为空")
		}
	case MailboxActionDelete:
		if req.HardDelete {
			return fmt.Errorf("hard delete is not supported in v1")
		}
		if req.TargetFolder == "" {
			req.TargetFolder = "Trash"
		}
	default:
		return fmt.Errorf("unsupported mailbox action: %s", req.Action)
	}
	return nil
}

func executeMailboxWrite(cfg *config.Config, req *MailboxWriteRequest) (*MailboxWriteResult, error) {
	if err := validateMailboxWriteRequest(req); err != nil {
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
	if _, err = c.Select(req.Folder, false); err != nil {
		return nil, fmt.Errorf("选择文件夹失败(%s): %w", req.Folder, err)
	}

	seqSet := new(imap.SeqSet)
	seqSet.AddNum(req.UID)
	switch req.Action {
	case MailboxActionSetReadStatus:
		var item imap.StoreItem = imap.RemoveFlags
		if *req.Read {
			item = imap.AddFlags
		}
		if err := c.UidStore(seqSet, item, []interface{}{imap.SeenFlag}, nil); err != nil {
			return nil, fmt.Errorf("IMAP 标记已读状态失败: %w", err)
		}
	case MailboxActionMove, MailboxActionDelete:
		if err := c.UidMove(seqSet, req.TargetFolder); err != nil {
			return nil, fmt.Errorf("IMAP 移动邮件失败(%s): %w", req.TargetFolder, err)
		}
	}
	return &MailboxWriteResult{
		Success:      true,
		Action:       req.Action,
		UID:          req.UID,
		Folder:       req.Folder,
		TargetFolder: req.TargetFolder,
	}, nil
}
