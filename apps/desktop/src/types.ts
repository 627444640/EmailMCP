export type Section = "mailbox" | "folders" | "permissions" | "index" | "codex" | "status" | "logs";

export interface MailboxConfig {
  email: string;
  smtpHost: string;
  smtpPort: number;
  smtpSsl: boolean;
  smtpFrom?: string;
  imapHost: string;
  imapPort: number;
  imapSsl: boolean;
}

export interface FolderConfig {
  attachmentDownloadDir: string;
  allowedAttachmentDirs: string[];
}

export interface PermissionConfig {
  read: boolean;
  write: boolean;
  send: boolean;
  downloadAttachments: boolean;
}

export interface LocalConfig {
  accountId: string;
  mailbox: MailboxConfig;
  folders: FolderConfig;
  index: IndexConfig;
  permissions: PermissionConfig;
  autostart: boolean;
}

export interface IndexConfig {
  enabled: boolean;
  path?: string;
}

export interface DoctorCheck {
  name: string;
  ok: boolean;
  detail?: string;
}

export interface DoctorStatus {
  ok: boolean;
  checks: DoctorCheck[];
}

export interface InstallResult {
  changed: boolean;
  backupPath?: string;
}

export interface IndexStatus {
  path: string;
  initialized: boolean;
  ftsEnabled: boolean;
  messageCount: number;
}

export interface IndexSyncFolderResult {
  folder: string;
  indexedMessages: number;
  error?: string;
}

export interface IndexSyncResult {
  indexPath: string;
  accountId: string;
  indexedMessages: number;
  folders: IndexSyncFolderResult[];
}
