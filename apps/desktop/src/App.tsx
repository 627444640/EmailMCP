import { useEffect, useMemo, useRef, useState, type ReactNode } from "react";
import { invoke } from "@tauri-apps/api/core";
import { listen } from "@tauri-apps/api/event";
import { open } from "@tauri-apps/plugin-dialog";
import {
  Activity,
  CheckCircle2,
  Database,
  FolderOpen,
  KeyRound,
  Mail,
  Plus,
  RefreshCw,
  Save,
  ScrollText,
  ShieldCheck,
  TerminalSquare,
  Trash2,
  XCircle,
} from "lucide-react";
import type { LucideIcon } from "lucide-react";
import type { DoctorStatus, IndexStatus, IndexSyncResult, InstallResult, LocalConfig, MailboxConfig, Section } from "./types";
import appIcon from "./assets/app-icon-512.png";

const defaultConfig: LocalConfig = {
  accountId: "default",
  mailbox: {
    email: "",
    smtpHost: "",
    smtpPort: 465,
    smtpSsl: true,
    smtpFrom: "",
    imapHost: "",
    imapPort: 993,
    imapSsl: true,
  },
  folders: {
    attachmentDownloadDir: "",
    allowedAttachmentDirs: [],
  },
  index: {
    enabled: true,
    path: "",
  },
  permissions: {
    read: true,
    write: true,
    send: true,
    downloadAttachments: true,
  },
  autostart: false,
};

const navItems: Array<{ id: Section; label: string; icon: LucideIcon }> = [
  { id: "mailbox", label: "邮箱配置", icon: Mail },
  { id: "folders", label: "文件目录", icon: FolderOpen },
  { id: "permissions", label: "权限控制", icon: ShieldCheck },
  { id: "index", label: "邮件索引", icon: Database },
  { id: "codex", label: "Codex 集成", icon: TerminalSquare },
  { id: "status", label: "状态诊断", icon: Activity },
  { id: "logs", label: "运行日志", icon: ScrollText },
];

const checkLabels: Record<string, string> = {
  config: "本地配置",
  smtpSecret: "SMTP 授权码",
  imapSecret: "IMAP 授权码",
  attachmentDir: "附件目录",
  codexConfig: "Codex 配置",
  index: "本地索引",
  network: "网络检查",
  smtpConnection: "SMTP 连接",
  imapConnection: "IMAP 连接",
};

type LogEntry = {
  at: string;
  text: string;
};

type Notice = {
  kind: "success" | "error" | "info";
  text: string;
};

function mergeConfig(value: LocalConfig): LocalConfig {
  return {
    ...defaultConfig,
    ...value,
    mailbox: { ...defaultConfig.mailbox, ...(value.mailbox ?? {}) },
    folders: {
      ...defaultConfig.folders,
      ...(value.folders ?? {}),
      allowedAttachmentDirs: value.folders?.allowedAttachmentDirs ?? [],
    },
    index: { ...defaultConfig.index, ...(value.index ?? {}) },
    permissions: { ...defaultConfig.permissions, ...(value.permissions ?? {}) },
  };
}

function errorText(error: unknown): string {
  if (error instanceof Error) {
    return error.message;
  }
  return String(error);
}

export default function App() {
  const [active, setActive] = useState<Section>("mailbox");
  const [config, setConfig] = useState<LocalConfig>(defaultConfig);
  const [smtpSecret, setSmtpSecret] = useState("");
  const [imapSecret, setImapSecret] = useState("");
  const [doctor, setDoctor] = useState<DoctorStatus | null>(null);
  const [indexStatus, setIndexStatus] = useState<IndexStatus | null>(null);
  const [lastIndexSync, setLastIndexSync] = useState<IndexSyncResult | null>(null);
  const [indexLimit, setIndexLimit] = useState(200);
  const [indexFullBodies, setIndexFullBodies] = useState(false);
  const [backupPath, setBackupPath] = useState("");
  const [busy, setBusy] = useState(false);
  const [logs, setLogs] = useState<LogEntry[]>([]);
  const [notice, setNotice] = useState<Notice | null>(null);
  const noticeTimer = useRef<number | null>(null);

  const statusText = useMemo(() => {
    if (!doctor) {
      return "未诊断";
    }
    return doctor.ok ? "正常" : "需要处理";
  }, [doctor]);

  useEffect(() => {
    void loadConfig();
    const unlisten = listen("run-doctor", () => {
      void runDoctor();
      setActive("status");
    });
    return () => {
      void unlisten.then((dispose) => dispose());
      if (noticeTimer.current) {
        window.clearTimeout(noticeTimer.current);
      }
    };
  }, []);

  function pushLog(text: string) {
    setLogs((current) => [{ at: new Date().toLocaleString(), text }, ...current].slice(0, 80));
  }

  function showNotice(text: string, kind: Notice["kind"] = "info") {
    setNotice({ text, kind });
    if (noticeTimer.current) {
      window.clearTimeout(noticeTimer.current);
    }
    noticeTimer.current = window.setTimeout(() => setNotice(null), 3600);
  }

  async function loadConfig() {
    setBusy(true);
    try {
      const loaded = await invoke<LocalConfig>("load_config");
      const autostart = await invoke<boolean>("get_autostart").catch(() => loaded.autostart ?? false);
      setConfig(mergeConfig({ ...loaded, autostart }));
      pushLog("已加载本地配置");
      showNotice("已加载本地配置", "success");
    } catch (error) {
      setConfig(defaultConfig);
      pushLog(`加载配置失败：${errorText(error)}`);
      showNotice(`加载配置失败：${errorText(error)}`, "error");
    } finally {
      setBusy(false);
    }
  }

  async function saveConfig() {
    setBusy(true);
    try {
      await invoke("save_config", { config });
      if (smtpSecret.trim()) {
        await invoke("set_secret", { kind: "smtp", value: smtpSecret });
      }
      if (imapSecret.trim()) {
        await invoke("set_secret", { kind: "imap", value: imapSecret });
      }
      setSmtpSecret("");
      setImapSecret("");
      pushLog("已保存邮箱配置");
      await runDoctor(false, false);
      showNotice("配置已保存", "success");
    } catch (error) {
      pushLog(`保存配置失败：${errorText(error)}`);
      showNotice(`保存失败：${errorText(error)}`, "error");
    } finally {
      setBusy(false);
    }
  }

  async function runDoctor(switchPage = true, notify = true) {
    setBusy(true);
    try {
      const status = await invoke<DoctorStatus>("run_doctor");
      setDoctor(status);
      pushLog(status.ok ? "诊断完成：正常" : "诊断完成：存在失败项");
      if (notify) {
        showNotice(status.ok ? "诊断完成：正常" : "诊断完成：存在失败项", status.ok ? "success" : "error");
      }
      if (switchPage) {
        setActive("status");
      }
    } catch (error) {
      pushLog(`诊断失败：${errorText(error)}`);
      if (notify) {
        showNotice(`诊断失败：${errorText(error)}`, "error");
      }
    } finally {
      setBusy(false);
    }
  }

  async function installCodex() {
    setBusy(true);
    try {
      const result = await invoke<InstallResult>("install_codex");
      if (result.backupPath) {
        setBackupPath(result.backupPath);
      }
      pushLog(result.changed ? "已写入 Codex MCP 配置" : "Codex MCP 配置已存在");
      await runDoctor(false, false);
      showNotice(result.changed ? "已写入 Codex MCP 配置" : "Codex MCP 配置已存在", "success");
    } catch (error) {
      pushLog(`写入 Codex 配置失败：${errorText(error)}`);
      showNotice(`写入 Codex 配置失败：${errorText(error)}`, "error");
    } finally {
      setBusy(false);
    }
  }

  async function refreshIndexStatus(notify = true) {
    setBusy(true);
    try {
      const status = await invoke<IndexStatus>("index_status");
      setIndexStatus(status);
      pushLog(`索引状态：${status.messageCount} 封邮件`);
      if (notify) {
        showNotice("索引状态已刷新", "success");
      }
    } catch (error) {
      pushLog(`索引状态失败：${errorText(error)}`);
      if (notify) {
        showNotice(`索引状态失败：${errorText(error)}`, "error");
      }
    } finally {
      setBusy(false);
    }
  }

  async function syncIndex() {
    setBusy(true);
    try {
      await invoke("save_config", { config });
      const result = await invoke<IndexSyncResult>("index_sync", {
        limitPerFolder: indexLimit,
        fullBodies: indexFullBodies,
      });
      setLastIndexSync(result);
      pushLog(`索引同步完成：${result.indexedMessages} 封邮件`);
      await refreshIndexStatus(false);
      showNotice("索引同步完成", "success");
    } catch (error) {
      pushLog(`索引同步失败：${errorText(error)}`);
      showNotice(`索引同步失败：${errorText(error)}`, "error");
    } finally {
      setBusy(false);
    }
  }

  async function restoreCodexBackup() {
    if (!backupPath.trim()) {
      pushLog("恢复失败：缺少备份路径");
      showNotice("恢复失败：缺少备份路径", "error");
      return;
    }
    setBusy(true);
    try {
      await invoke("restore_codex_backup", { backupPath });
      pushLog("已恢复 Codex 配置备份");
      showNotice("已恢复 Codex 配置备份", "success");
      await runDoctor(false);
    } catch (error) {
      pushLog(`恢复备份失败：${errorText(error)}`);
      showNotice(`恢复备份失败：${errorText(error)}`, "error");
    } finally {
      setBusy(false);
    }
  }

  async function chooseDownloadDir() {
    const selected = await open({ directory: true, multiple: false });
    if (typeof selected === "string") {
      updateConfig({ folders: { ...config.folders, attachmentDownloadDir: selected } });
    }
  }

  async function addAllowedDir() {
    const selected = await open({ directory: true, multiple: false });
    if (typeof selected === "string" && !config.folders.allowedAttachmentDirs.includes(selected)) {
      updateConfig({
        folders: {
          ...config.folders,
          allowedAttachmentDirs: [...config.folders.allowedAttachmentDirs, selected],
        },
      });
    }
  }

  async function setAutostart(enabled: boolean) {
    const previous = config;
    const next = { ...config, autostart: enabled };
    setBusy(true);
    try {
      await invoke("save_config", { config: next });
      await invoke("set_autostart", { enabled });
      setConfig(next);
      pushLog(enabled ? "已开启托盘自启" : "已关闭托盘自启");
      showNotice(enabled ? "已开启托盘自启" : "已关闭托盘自启", "success");
    } catch (error) {
      await invoke("save_config", { config: previous }).catch(() => undefined);
      await invoke("set_autostart", { enabled: previous.autostart }).catch(() => undefined);
      setConfig(previous);
      pushLog(`修改自启失败：${errorText(error)}`);
      showNotice(`修改自启失败：${errorText(error)}`, "error");
    } finally {
      setBusy(false);
    }
  }

  function updateConfig(patch: Partial<LocalConfig>) {
    setConfig((current) => mergeConfig({ ...current, ...patch }));
  }

  function updateMailbox<K extends keyof MailboxConfig>(key: K, value: MailboxConfig[K]) {
    updateConfig({ mailbox: { ...config.mailbox, [key]: value } });
  }

  function removeAllowedDir(dir: string) {
    updateConfig({
      folders: {
        ...config.folders,
        allowedAttachmentDirs: config.folders.allowedAttachmentDirs.filter((item) => item !== dir),
      },
    });
  }

  return (
    <div className="shell">
      <aside className="sidebar">
        <div className="brand">
          <img className="brand-mark" src={appIcon} alt="" />
          <div>
            <strong>Email MCP</strong>
            <span>邮箱配置中心</span>
          </div>
        </div>

        <nav className="nav">
          {navItems.map((item) => {
            const Icon = item.icon;
            return (
              <button
                key={item.id}
                className={active === item.id ? "nav-item active" : "nav-item"}
                onClick={() => setActive(item.id)}
                title={item.label}
              >
                <Icon size={18} />
                <span>{item.label}</span>
              </button>
            );
          })}
        </nav>

        <div className="sidebar-footer">
          <span className={doctor?.ok ? "status-dot ok" : doctor ? "status-dot fail" : "status-dot"} />
          <span>{statusText}</span>
        </div>
      </aside>

      <main className="main">
        <header className="topbar">
          <div>
            <h1>{navItems.find((item) => item.id === active)?.label}</h1>
            <p>{config.mailbox.email || "default"}</p>
          </div>
          <div className="actions">
            <button className="button secondary" onClick={() => void loadConfig()} disabled={busy}>
              <RefreshCw size={16} />
              刷新
            </button>
            <button className="button primary" onClick={() => void saveConfig()} disabled={busy}>
              <Save size={16} />
              保存
            </button>
          </div>
        </header>

        {active === "mailbox" && (
          <section className="panel">
            <div className="grid two">
              <Field label="邮箱地址">
                <input
                  value={config.mailbox.email}
                  onChange={(event) => updateMailbox("email", event.currentTarget.value)}
                  placeholder="name@example.com"
                />
              </Field>
              <Field label="发件显示地址">
                <input
                  value={config.mailbox.smtpFrom ?? ""}
                  onChange={(event) => updateMailbox("smtpFrom", event.currentTarget.value)}
                  placeholder="默认使用邮箱地址"
                />
              </Field>
            </div>

            <div className="split">
              <div className="group">
                <h2>SMTP</h2>
                <Field label="Host">
                  <input
                    value={config.mailbox.smtpHost}
                    onChange={(event) => updateMailbox("smtpHost", event.currentTarget.value)}
                    placeholder="smtp.qq.com"
                  />
                </Field>
                <div className="grid two tight">
                  <Field label="Port">
                    <input
                      type="number"
                      min={1}
                      max={65535}
                      value={config.mailbox.smtpPort}
                      onChange={(event) => updateMailbox("smtpPort", Number(event.currentTarget.value || 0))}
                    />
                  </Field>
                  <Toggle
                    label="SSL"
                    checked={config.mailbox.smtpSsl}
                    onChange={(checked) => updateMailbox("smtpSsl", checked)}
                  />
                </div>
                <Field label="SMTP 授权码">
                  <input
                    type="password"
                    value={smtpSecret}
                    onChange={(event) => setSmtpSecret(event.currentTarget.value)}
                    placeholder="留空表示不更新"
                  />
                </Field>
              </div>

              <div className="group">
                <h2>IMAP</h2>
                <Field label="Host">
                  <input
                    value={config.mailbox.imapHost}
                    onChange={(event) => updateMailbox("imapHost", event.currentTarget.value)}
                    placeholder="imap.qq.com"
                  />
                </Field>
                <div className="grid two tight">
                  <Field label="Port">
                    <input
                      type="number"
                      min={1}
                      max={65535}
                      value={config.mailbox.imapPort}
                      onChange={(event) => updateMailbox("imapPort", Number(event.currentTarget.value || 0))}
                    />
                  </Field>
                  <Toggle
                    label="SSL"
                    checked={config.mailbox.imapSsl}
                    onChange={(checked) => updateMailbox("imapSsl", checked)}
                  />
                </div>
                <Field label="IMAP 授权码">
                  <input
                    type="password"
                    value={imapSecret}
                    onChange={(event) => setImapSecret(event.currentTarget.value)}
                    placeholder="留空表示不更新"
                  />
                </Field>
              </div>
            </div>

            <div className="panel-actions">
              <button className="button secondary" onClick={() => void runDoctor()} disabled={busy}>
                <Activity size={16} />
                运行诊断
              </button>
            </div>
          </section>
        )}

        {active === "folders" && (
          <section className="panel">
            <Field label="附件下载目录">
              <div className="inline-control">
                <input
                  value={config.folders.attachmentDownloadDir}
                  onChange={(event) =>
                    updateConfig({
                      folders: { ...config.folders, attachmentDownloadDir: event.currentTarget.value },
                    })
                  }
                />
                <button className="icon-button" onClick={() => void chooseDownloadDir()} title="选择目录">
                  <FolderOpen size={18} />
                </button>
              </div>
            </Field>

            <div className="list-head">
              <h2>发送附件白名单</h2>
              <button className="button secondary" onClick={() => void addAllowedDir()}>
                <Plus size={16} />
                添加目录
              </button>
            </div>

            <div className="dir-list">
              {config.folders.allowedAttachmentDirs.length === 0 && <div className="empty">未配置目录</div>}
              {config.folders.allowedAttachmentDirs.map((dir) => (
                <div className="dir-row" key={dir}>
                  <span>{dir}</span>
                  <button className="icon-button danger" onClick={() => removeAllowedDir(dir)} title="移除目录">
                    <Trash2 size={17} />
                  </button>
                </div>
              ))}
            </div>
          </section>
        )}

        {active === "permissions" && (
          <section className="panel">
            <div className="permission-list">
              <Toggle
                label="读取邮件"
                checked={config.permissions.read}
                onChange={(checked) => updateConfig({ permissions: { ...config.permissions, read: checked } })}
              />
              <Toggle
                label="写入邮件状态"
                checked={config.permissions.write}
                onChange={(checked) => updateConfig({ permissions: { ...config.permissions, write: checked } })}
              />
              <Toggle
                label="发送邮件"
                checked={config.permissions.send}
                onChange={(checked) => updateConfig({ permissions: { ...config.permissions, send: checked } })}
              />
              <Toggle
                label="下载附件"
                checked={config.permissions.downloadAttachments}
                onChange={(checked) =>
                  updateConfig({ permissions: { ...config.permissions, downloadAttachments: checked } })
                }
              />
            </div>
            <div className="divider" />
            <Toggle label="托盘开机自启" checked={config.autostart} onChange={(checked) => void setAutostart(checked)} />
          </section>
        )}

        {active === "index" && (
          <section className="panel">
            <div className="grid two">
              <Toggle
                label="启用本地索引"
                checked={config.index.enabled}
                onChange={(checked) => updateConfig({ index: { ...config.index, enabled: checked } })}
              />
              <Field label="每文件夹同步数量">
                <input
                  type="number"
                  min={1}
                  max={5000}
                  value={indexLimit}
                  onChange={(event) => setIndexLimit(Number(event.currentTarget.value || 0))}
                />
              </Field>
            </div>

            <Field label="索引文件路径">
              <input
                value={config.index.path ?? ""}
                onChange={(event) => updateConfig({ index: { ...config.index, path: event.currentTarget.value } })}
                placeholder="留空使用系统默认路径"
              />
            </Field>

            <div className="divider" />
            <Toggle label="索引完整正文" checked={indexFullBodies} onChange={setIndexFullBodies} />

            <div className="panel-actions">
              <button className="button secondary" onClick={() => void refreshIndexStatus()} disabled={busy}>
                <Activity size={16} />
                查看状态
              </button>
              <button className="button primary" onClick={() => void syncIndex()} disabled={busy || !config.index.enabled}>
                <Database size={16} />
                同步索引
              </button>
            </div>

            <div className="checks index-checks">
              {indexStatus ? (
                <>
                  <div className="check-row">
                    <CheckCircle2 className={indexStatus.initialized ? "check-icon ok" : "check-icon fail"} size={18} />
                    <strong>SQLite</strong>
                    <span>{indexStatus.path}</span>
                  </div>
                  <div className="check-row">
                    <CheckCircle2 className={indexStatus.ftsEnabled ? "check-icon ok" : "check-icon fail"} size={18} />
                    <strong>FTS</strong>
                    <span>{indexStatus.ftsEnabled ? "enabled" : "disabled"}</span>
                  </div>
                  <div className="check-row">
                    <CheckCircle2 className="check-icon ok" size={18} />
                    <strong>Messages</strong>
                    <span>{indexStatus.messageCount}</span>
                  </div>
                </>
              ) : (
                <div className="empty">未读取索引状态</div>
              )}
            </div>

            {lastIndexSync && (
              <div className="logs index-sync">
                {lastIndexSync.folders.map((folder) => (
                  <div className="log-row" key={folder.folder}>
                    <time>{folder.folder}</time>
                    <span>
                      {folder.indexedMessages}
                      {folder.error ? ` / ${folder.error}` : ""}
                    </span>
                  </div>
                ))}
              </div>
            )}
          </section>
        )}

        {active === "codex" && (
          <section className="panel">
            <div className="codex-actions">
              <button className="button primary" onClick={() => void installCodex()} disabled={busy}>
                <TerminalSquare size={16} />
                安装 MCP 配置
              </button>
              <button className="button secondary" onClick={() => void runDoctor()} disabled={busy}>
                <Activity size={16} />
                检测配置
              </button>
            </div>
            <Field label="备份路径">
              <input value={backupPath} onChange={(event) => setBackupPath(event.currentTarget.value)} />
            </Field>
            <button className="button secondary" onClick={() => void restoreCodexBackup()} disabled={busy}>
              <RefreshCw size={16} />
              恢复备份
            </button>
          </section>
        )}

        {active === "status" && (
          <section className="panel">
            <div className="status-summary">
              <span className={doctor?.ok ? "status-dot ok" : doctor ? "status-dot fail" : "status-dot"} />
              <strong>{statusText}</strong>
              <button className="button secondary" onClick={() => void runDoctor(false)} disabled={busy}>
                <RefreshCw size={16} />
                重新诊断
              </button>
            </div>
            <div className="checks">
              {doctor?.checks.map((check) => (
                <div className="check-row" key={check.name}>
                  {check.ok ? (
                    <CheckCircle2 className="check-icon ok" size={18} />
                  ) : (
                    <XCircle className="check-icon fail" size={18} />
                  )}
                  <strong>{checkLabels[check.name] ?? check.name}</strong>
                  <span>{check.detail ?? ""}</span>
                </div>
              )) ?? <div className="empty">未运行诊断</div>}
            </div>
          </section>
        )}

        {active === "logs" && (
          <section className="panel">
            <div className="logs">
              {logs.length === 0 && <div className="empty">暂无记录</div>}
              {logs.map((entry, index) => (
                <div className="log-row" key={`${entry.at}-${index}`}>
                  <time>{entry.at}</time>
                  <span>{entry.text}</span>
                </div>
              ))}
            </div>
          </section>
        )}
      </main>
      {notice && (
        <div className={`notice ${notice.kind}`} role="status" aria-live="polite">
          {notice.kind === "success" ? <CheckCircle2 size={18} /> : <XCircle size={18} />}
          <span>{notice.text}</span>
        </div>
      )}
    </div>
  );
}

function Field(props: { label: string; children: ReactNode }) {
  return (
    <label className="field">
      <span>{props.label}</span>
      {props.children}
    </label>
  );
}

function Toggle(props: { label: string; checked: boolean; onChange: (checked: boolean) => void }) {
  return (
    <label className="toggle-row">
      <span>{props.label}</span>
      <input type="checkbox" checked={props.checked} onChange={(event) => props.onChange(event.currentTarget.checked)} />
    </label>
  );
}
