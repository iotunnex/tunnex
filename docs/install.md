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

## 2. macOS — install the `.pkg`

Tunnex ships as a **`.pkg` installer**. It installs the app to **/Applications** and sets up
the privileged helper during install (one admin prompt) — so you are NOT prompted on first
Connect, and the app runs from a fixed path (no "caller not trusted" issues).

1. Open **`Tunnex-<version>.pkg`**. Unsigned, so Gatekeeper warns:
   - **macOS 14 and earlier:** right-click the `.pkg` → **Open** → **Open**.
   - **macOS 15 (Sequoia)+:** try to open it once (blocked), then **System Settings →
     Privacy & Security** → **"Tunnex… was blocked"** → **Open Anyway**.
   - **No-warning path:** download the `.pkg` with `curl -LO "<url>"` (curl downloads aren't
     quarantined), then `open Tunnex-<version>.pkg`.
2. Step through the installer. It asks for your **password once** — that's Tunnex installing
   its VPN helper (a small root component that manages the WireGuard tunnel + kill-switch).
3. Launch **Tunnex** from **/Applications**.

> Always install with the `.pkg` and run from /Applications. Running the `.app` from
> Downloads/Desktop can trigger macOS App Translocation (a random read-only path), which
> breaks the helper's caller check — the app will tell you to move it to Applications.

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

- **macOS:** quit Tunnex and drag **Tunnex.app** to the Trash — that's it. The helper
  notices its app is gone and **removes itself within ~90 seconds** (releases the
  kill-switch, restores `pf.conf`, deletes its files, unloads the daemon). No script.
  - Immediate removal (optional): `sudo bash scripts/macos-uninstall.sh` (from the repo).
- **Windows:** Settings → Apps → **Tunnex** → Uninstall. The uninstaller stops and removes
  the helper service.

---

## Why unsigned?

Signing (Apple notarization + a Windows EV certificate) is a later milestone tied to public
distribution. Until then these steps are the trade-off for an early build. A signed release
will install with no warnings and enable automatic updates.
