#!/bin/bash
# S6.5a — production macOS helper uninstall + residue check. Run with sudo. Removes the
# LaunchDaemon, the installed helper, the runtime dir, and restores /etc/pf.conf from the
# backup the installer made. The packaged-app residue smoke asserts this leaves nothing.
set -euo pipefail

LABEL=io.tunnex.helper
PLIST=/Library/LaunchDaemons/${LABEL}.plist
DIR=/usr/local/tunnex
RUN=/var/run/tunnex

echo ">> stop + unload the daemon"
launchctl bootout system "$PLIST" 2>/dev/null || launchctl unload "$PLIST" 2>/dev/null || true

echo ">> remove installed files"
rm -f "$PLIST"
rm -rf "$DIR" "$RUN"

echo ">> restore pf.conf (remove the tunnex anchor reference)"
if [ -f /etc/pf.conf.tunnex-bak ]; then
  cp /etc/pf.conf.tunnex-bak /etc/pf.conf
  rm -f /etc/pf.conf.tunnex-bak
  pfctl -f /etc/pf.conf 2>/dev/null || true
fi
pfctl -a tunnex -F all 2>/dev/null || true

echo
echo "=== RESIDUE CHECK (all must be gone) ==="
test ! -f "$PLIST" && echo "  plist: removed OK" || echo "  plist STILL PRESENT <-- FAIL"
launchctl print "system/${LABEL}" >/dev/null 2>&1 && echo "  daemon STILL LOADED <-- FAIL" || echo "  daemon: not loaded OK"
test ! -e "$DIR" && echo "  $DIR: removed OK" || echo "  $DIR STILL PRESENT <-- FAIL"
test ! -e "$RUN/helper.sock" && echo "  socket: gone OK" || echo "  socket STILL PRESENT <-- FAIL"
grep -q 'anchor "tunnex"' /etc/pf.conf 2>/dev/null && echo "  pf.conf anchor STILL PRESENT <-- FAIL" || echo "  pf.conf: clean OK"
echo ">> uninstall done"
