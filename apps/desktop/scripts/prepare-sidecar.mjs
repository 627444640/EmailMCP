import { execFileSync } from "node:child_process";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import { fileURLToPath } from "node:url";

const __dirname = path.dirname(fileURLToPath(import.meta.url));
const desktopDir = path.resolve(__dirname, "..");
const repoRoot = path.resolve(desktopDir, "../..");
const binaryDir = path.join(desktopDir, "src-tauri", "binaries");

const triples = {
  "win32-x64": "x86_64-pc-windows-msvc",
  "win32-arm64": "aarch64-pc-windows-msvc",
  "darwin-x64": "x86_64-apple-darwin",
  "darwin-arm64": "aarch64-apple-darwin",
  "linux-x64": "x86_64-unknown-linux-gnu",
  "linux-arm64": "aarch64-unknown-linux-gnu",
};

const key = `${process.platform}-${process.arch}`;
const triple = triples[key];
if (!triple) {
  throw new Error(`Unsupported platform for sidecar packaging: ${key}`);
}

fs.mkdirSync(binaryDir, { recursive: true });
const ext = process.platform === "win32" ? ".exe" : "";
const output = path.join(binaryDir, `email-mcp-${triple}${ext}`);

execFileSync("go", ["build", "-buildvcs=false", "-o", output, "."], {
  cwd: repoRoot,
  stdio: "inherit",
  env: {
    ...process.env,
    CGO_ENABLED: process.env.CGO_ENABLED ?? "0",
  },
});

console.log(`Prepared sidecar: ${output}`);
