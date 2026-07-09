# Installing Tunnex (desktop)

The S6.5a builds are **unsigned** — no Apple Developer ID, no Windows code-signing
certificate yet (that's a later milestone). The apps are safe, but macOS Gatekeeper and
Windows SmartScreen will warn on first launch because they can't verify a signature.
Here's how to install past those warnings, and how to verify you got the real file.

---

## 1. Verify your download (both platforms)

Every release ships a `SHA256SUMS` file. Check the installer you downloaded matches the
hash published on the release page (this is how you trust an unsigned build):

- **macOS:** `shasum -a 256 Tunnex-<version>-universal.dmg`
- **Windows (PowerShell):** `Get-FileHash .\Tunnex-Setup-<version>.exe -Algorithm SHA256`

Compare the output to the matching line in `SHA256SUMS`. If it doesn't match, don't run it.

---

## 2. macOS — get past Gatekeeper

Double-clicking an unsigned `.dmg`/app shows **"Tunnex can't be opened because Apple
cannot check it for malicious software."** Pick whichever applies:

- **Recommended (no warning at all): install via `curl`.** Files downloaded with `curl`
  aren't quarantined, so they open normally:
  ```
  curl -L -o Tunnex.dmg "<release-download-url>"
  open Tunnex.dmg          # drag Tunnex to Applications
  ```
- **macOS 14 (Sonoma) and earlier:** right-click (Control-click) the app in Applications
  → **Open** → **Open** in the dialog. Approves this one app permanently.
- **macOS 15 (Sequoia) and later:** the Control-click bypass was removed. Try to open it
  once (it will be blocked), then go **System Settings → Privacy & Security**, scroll to
  **"Tunnex was blocked…"**, click **Open Anyway**, and confirm.
- **Escape hatch (terminal):** `xattr -dr com.apple.quarantine /Applications/Tunnex.app`

### First connect: the helper install prompt
The first time you click **Connect**, macOS shows a **password prompt** — Tunnex is
installing its VPN helper (a small root component that manages the WireGuard tunnel and
the kill-switch). Enter your password once; it won't ask again on later connects.

---

## 3. Windows — get past SmartScreen

Running the unsigned `.exe` shows **"Windows protected your PC."** Click **More info** →
**Run anyway**. (SmartScreen warns on any installer without an established signing
reputation; that goes away once the app is signed in a later release.) The installer is
elevated (UAC) and registers the Tunnex helper service during install.

---

## 4. Connect

Launch Tunnex → enter your organization's server URL → sign in → **Connect**. A successful
tunnel shows **Connected** with your assigned IP; traffic for your org network now routes
through Tunnex. Use the **tray/menu-bar icon** to connect/disconnect without the window.

---

## 5. Uninstall (clean removal)

- **macOS:** quit Tunnex, drag **Tunnex.app** to the Trash, then remove the helper:
  ```
  sudo scripts/macos-uninstall.sh     # from the repo, or the steps it runs:
  # sudo launchctl bootout system /Library/LaunchDaemons/io.tunnex.helper.plist
  # sudo rm -f /Library/LaunchDaemons/io.tunnex.helper.plist
  # sudo rm -rf /usr/local/tunnex /var/run/tunnex
  # sudo cp /etc/pf.conf.tunnex-bak /etc/pf.conf   # restore pf (if present)
  ```
- **Windows:** Settings → Apps → **Tunnex** → Uninstall. The uninstaller stops and removes
  the helper service.

---

## Why unsigned?

Signing (Apple notarization + a Windows EV certificate) is a later milestone tied to public
distribution. Until then these steps are the trade-off for an early build. A signed release
will install with no warnings and enable automatic updates.
