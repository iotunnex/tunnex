import * as fs from "node:fs";
import * as path from "node:path";
import { execFile } from "node:child_process";

// S6.5a — the in-app privileged helper install (the "zero terminal" step). On macOS
// the packaged, UNSIGNED app can't use SMAppService (needs Developer ID → S6.5b), so
// on first connect it installs the bundled helper as a LaunchDaemon via ONE GUI admin
// prompt (osascript's `with administrator privileges` — non-deprecated, OS-mediated,
// caller-signature-irrelevant; proven by the S6.5a spike). Windows registers its SCM
// service at NSIS install time (elevated), so this is a no-op there.
//
// The plist MIRRORS scripts/macos-dev-install.sh (the path proven live in the S6.3
// smoke): same label, socket, and TUNNEX_INSTALL_DIR caller-auth env — only the helper
// source (bundled resource) and the trusted caller dir (the packaged app) differ.

const LABEL = "io.tunnex.helper";
const PLIST = `/Library/LaunchDaemons/${LABEL}.plist`;
const INSTALL_DIR = "/usr/local/tunnex"; // no spaces — keeps the root shell script simple
const HELPER_DEST = `${INSTALL_DIR}/tunnex-helper`;
const SOCKET = "/var/run/tunnex/helper.sock";

// helperInstalled is the cheap presence check (the daemon plist exists). Connect uses
// it to decide whether to run the one-time install before dialing the helper socket.
export function helperInstalled(platform: NodeJS.Platform = process.platform): boolean {
  if (platform !== "darwin") return true; // windows: installed by NSIS; nothing to do here
  return fs.existsSync(PLIST);
}

// stagedHelperPath is the bundled helper inside the app's resources (universal binary).
function stagedHelperPath(): string {
  return path.join(process.resourcesPath, "helper", "tunnex-helper");
}

// callerTrustDir is the directory the helper must trust as the caller: the packaged
// app's main executable dir (…/Tunnex.app/Contents/MacOS). The helper resolves the
// connecting process's exe via libproc and checks it is within TUNNEX_INSTALL_DIR.
function callerTrustDir(): string {
  return path.dirname(process.execPath);
}

// installScript is the root shell script run under the single admin prompt. It copies
// + ad-hoc-signs the helper (re-sign in place: a copied mach-o gets Killed:9 on Apple
// Silicon otherwise), references the pf anchor from /etc/pf.conf so the kill-switch is
// evaluated (backing up first), writes the LaunchDaemon plist, and bootstraps it.
function installScript(staged: string, trustDir: string): string {
  return [
    "set -e",
    `mkdir -p '${INSTALL_DIR}' /var/run/tunnex`,
    `cp '${staged}' '${HELPER_DEST}'`,
    `chown root:wheel '${HELPER_DEST}'`,
    `chmod 755 '${HELPER_DEST}'`,
    `codesign --force --sign - '${HELPER_DEST}'`,
    // pf anchor reference — mirrors the dev-install so the kill-switch anchor is live.
    `if ! grep -q 'anchor \\"tunnex\\"' /etc/pf.conf; then cp /etc/pf.conf /etc/pf.conf.tunnex-bak; printf '%s\\n' 'anchor \\"tunnex\\"' >> /etc/pf.conf; pfctl -f /etc/pf.conf 2>/dev/null || true; fi`,
    `cat > '${PLIST}' <<'PL'`,
    `<?xml version="1.0" encoding="UTF-8"?>`,
    `<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">`,
    `<plist version="1.0"><dict>`,
    `  <key>Label</key><string>${LABEL}</string>`,
    `  <key>ProgramArguments</key><array><string>${HELPER_DEST}</string></array>`,
    `  <key>EnvironmentVariables</key><dict>`,
    `    <key>TUNNEX_INSTALL_DIR</key><string>${trustDir}</string>`,
    `    <key>TUNNEX_HELPER_SOCKET</key><string>${SOCKET}</string>`,
    `  </dict>`,
    `  <key>RunAtLoad</key><true/>`,
    `  <key>KeepAlive</key><true/>`,
    `  <key>StandardErrorPath</key><string>/tmp/tunnex-helper.log</string>`,
    `</dict></plist>`,
    `PL`,
    `chown root:wheel '${PLIST}'`,
    `chmod 644 '${PLIST}'`,
    `launchctl bootout system '${PLIST}' 2>/dev/null || true`,
    `launchctl bootstrap system '${PLIST}'`,
  ].join("\n");
}

// runPrivileged runs a shell script as root via ONE native GUI admin prompt. Uses
// osascript's `do shell script … with administrator privileges` — no deprecated C API,
// works from an unsigned caller (spike-confirmed). Rejects on cancel / failure.
function runPrivileged(script: string, prompt: string): Promise<void> {
  // AppleScript string literal: escape backslashes then double-quotes.
  const escaped = script.replace(/\\/g, "\\\\").replace(/"/g, '\\"');
  const osa = `do shell script "${escaped}" with administrator privileges with prompt "${prompt}"`;
  return new Promise((resolve, reject) => {
    execFile("/usr/bin/osascript", ["-e", osa], (err, _stdout, stderr) => {
      if (err) {
        // -128 = user cancelled the auth dialog.
        const canceled = /-128|User canceled/i.test(stderr || String(err));
        reject(new Error(canceled ? "helper_install_canceled" : `helper_install_failed: ${stderr || err.message}`));
        return;
      }
      resolve();
    });
  });
}

// isTranslocated reports whether macOS is running the app from a randomized read-only
// App Translocation mount (unsigned + quarantined app launched from outside /Applications).
// The exec path there is EPHEMERAL — different every launch — so installing a helper that
// trusts it produces caller_untrusted on the next launch. The .pkg install path avoids
// this entirely (installs to /Applications, unquarantined); this guard catches the
// stray "ran the .app from Downloads" case with an actionable error instead.
function isTranslocated(): boolean {
  return process.platform === "darwin" && process.execPath.includes("/AppTranslocation/");
}

// ensureHelperInstalled installs the privileged helper if it isn't already. Idempotent
// and a no-op off macOS (and after the .pkg postinstall already installed it). Throws
// app_translocated / helper_install_canceled / helper_install_failed / helper_asset_missing
// so connect() can surface an actionable message.
export async function ensureHelperInstalled(): Promise<void> {
  if (process.platform !== "darwin") return;
  if (helperInstalled()) return; // the .pkg postinstall (or a prior run) already did it
  if (isTranslocated()) throw new Error("app_translocated"); // move to /Applications, reopen
  const staged = stagedHelperPath();
  if (!fs.existsSync(staged)) throw new Error("helper_asset_missing");
  await runPrivileged(installScript(staged, callerTrustDir()), "Tunnex needs to install its VPN helper.");
}
