# Email MCP

Email MCP is a local desktop email MCP server for Codex. Each user installs and runs the service on their own computer; there is no remote HTTP service, shared token store, registration flow, or administrator approval page.

The desktop app is a configuration center. Codex talks directly to the local Go sidecar through stdio, so the MCP tools can work even when the GUI is closed.

## Architecture

- Go sidecar: `email-mcp mcp` exposes the stdio MCP server for Codex.
- Go config CLI: `email-mcp config ...` manages local non-secret config and OS credential-store secrets.
- Go doctor CLI: `email-mcp doctor --json` reports local readiness.
- Go index CLI: `email-mcp index ...` manages the local SQLite/FTS mailbox index.
- Desktop GUI: `apps/desktop` is a Tauri + React settings center.

## MCP Tools

- `listEmails`
- `listFolders`
- `listEmailsV2`
- `resolveSpecialFolders`
- `searchEmails`
- `searchAllFolders`
- `getEmail`
- `downloadEmailAttachments`
- `sendEmail`
- `createFolder`
- `setEmailReadStatus`
- `moveEmail`
- `deleteEmail`
- `bulkMoveEmails`
- `bulkDeleteEmails`
- `bulkSetEmailReadStatus`
- `archiveEmails`
- `previewOrganizePlan`
- `applyOrganizePlan`

Invoice filtering, attachment review, amount extraction, and report writing are Codex tasks. The MCP only provides primitive mailbox operations and controlled attachment download. Mailbox organization should be previewed first with `previewOrganizePlan` or the bulk tools' default dry-run behavior, then executed only after confirmation.

## Desktop Install

Prebuilt internal packages should be distributed from GitHub Releases, not GitHub Packages. GitHub Packages is for package registries such as npm, NuGet, Maven, RubyGems, or container images.

### Windows

Build the installer on Windows:

```powershell
cd apps/desktop
npm install
npm run tauri:build
```

The installer is written to:

```text
apps/desktop/src-tauri/target/release/bundle/nsis/
```

### macOS

macOS `.dmg` and `.app` packages must be built on macOS.

Recommended path: use GitHub Actions.

1. Push this repository to GitHub.
2. Open **Actions**.
3. Run **Build macOS package**.
4. Download the `email-mcp-macos` artifact.
5. Open the `.dmg` and drag `Email MCP.app` into `/Applications`.

Local build path from a Mac:

```bash
xcode-select --install
brew install node go rust
cargo install tauri-cli --version "^2" --locked

cd apps/desktop
npm ci
npm run tauri:build
```

The output is written under:

```text
apps/desktop/src-tauri/target/release/bundle/dmg/
apps/desktop/src-tauri/target/release/bundle/macos/
```

For unsigned internal builds, macOS may block the first launch. Use Control-click -> **Open**, or approve it from **System Settings -> Privacy & Security**.

## Publishing Releases

This repository includes a manual GitHub Actions release workflow:

```text
.github/workflows/release.yml
```

To publish desktop packages:

1. Open the GitHub repository.
2. Go to **Actions**.
3. Run **Release desktop packages**.
4. Enter a version such as `0.1.0`.
5. Keep `prerelease=true` for unsigned internal builds.
6. Wait for the Windows and macOS jobs to finish.
7. Open **Releases** and download the generated installer files.

The workflow builds:

- Windows NSIS setup executable.
- macOS dmg.
- macOS zipped `.app`.

The release workflow temporarily applies the input version to the desktop package metadata during CI. Source files in the repository are not modified by the workflow.

## Codex Integration

After installing the desktop app:

1. Open Email MCP.
2. Configure mailbox host, port, account, and SMTP/IMAP authorization codes.
3. Configure attachment download and allowed send-attachment directories.
4. Enable the required permissions.
5. Open the **Codex** page and install the MCP configuration.

The desktop app writes this block into Codex config and creates a backup first:

```toml
[mcp_servers.email]
command = "<installed email-mcp path>"
args = ["mcp"]
```

## Development

Go sidecar:

```powershell
go test -count=1 ./...
go build -buildvcs=false -o email-mcp-service.exe .
.\email-mcp-service.exe mcp
.\email-mcp-service.exe config path
.\email-mcp-service.exe doctor --json
.\email-mcp-service.exe index status
.\email-mcp-service.exe index sync --limit-per-folder 200
@'
{"keyword":"invoice","limit":20}
'@ | .\email-mcp-service.exe index search
```

Desktop app:

```powershell
cd apps/desktop
npm install
npm run tauri:dev
npm run tauri:build
```

Desktop builds require Node.js, Go, and Rust/Cargo. The Tauri build script runs `npm run prepare:sidecar`, which compiles the Go sidecar into `apps/desktop/src-tauri/binaries/email-mcp-<target-triple>`.

## Config And Secrets

- Windows config: `%APPDATA%\EmailMCP\config.json`
- macOS config: `~/Library/Application Support/EmailMCP/config.json`
- SMTP/IMAP authorization codes are stored in the OS credential store.
- Local mailbox index: the OS cache directory under `EmailMCP/mail-index.db`, unless a custom path is configured.
- `.env` is only a development fallback for running the Go sidecar directly.

## Safety Boundaries

- No HTTP listener is started by this version.
- No Bearer token is used; the local OS user is the authority boundary.
- Attachment downloads are written only to the configured download directory.
- Sending attachments can read only configured whitelist directories.
- `deleteEmail` moves messages to Trash; hard delete is not exposed.
- Bulk move/delete/archive tools default to dry-run mode.
- The GUI does not display or log SMTP/IMAP authorization codes.

## Repository Hygiene

Do not commit local secrets, local mailbox data, generated binaries, installer output, logs, or private mailbox reports.

The repository intentionally ignores:

- `.env`
- `*.db`, `*.db-shm`, `*.db-wal`
- `*.exe`, `*.exe~`
- `logs/`
- `docs/`
- `.agents/`, `.codegraph/`, `.uploads/`, `.superpowers/`
- `apps/desktop/node_modules/`
- `apps/desktop/dist/`
- `apps/desktop/src-tauri/target/`
- `apps/desktop/src-tauri/binaries/email-mcp-*`

Before pushing, run:

```powershell
git status --short
git diff --cached --name-only
```

Only source code, configuration templates, icons, lockfiles, tests, and GitHub workflow files should be committed.
