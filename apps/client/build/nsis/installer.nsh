; S6.5a — Windows SCM service registration for the privilege helper. The NSIS
; installer runs elevated (perMachine), so it registers + starts the helper as an
; auto-start SYSTEM service at install and removes it at uninstall. UNSIGNED is fine:
; Windows SCM does not require Authenticode for user-mode service binaries (only
; kernel drivers do) — SmartScreen only gates first launch (see docs/install.md).
;
; FINALIZE ON THE WINDOWS BOX (tomorrow): confirm the service starts + the helper
; reads TUNNEX_INSTALL_DIR from the machine environment, wintun.dll resolves next to
; the exe, and the residue smoke (sc query = not found + WFP/adapter gone) passes.

!macro customInstall
  nsExec::ExecToLog 'sc create tunnex-helper binPath= "$INSTDIR\resources\helper\tunnex-helper.exe" start= auto DisplayName= "Tunnex Helper"'
  ; Trust the installed app as the caller (helper caller-auth) via a machine env var
  ; the service inherits at start.
  WriteRegExpandStr HKLM "SYSTEM\CurrentControlSet\Control\Session Manager\Environment" "TUNNEX_INSTALL_DIR" "$INSTDIR"
  nsExec::ExecToLog 'sc start tunnex-helper'
!macroend

!macro customUnInstall
  nsExec::ExecToLog 'sc stop tunnex-helper'
  nsExec::ExecToLog 'sc delete tunnex-helper'
  DeleteRegValue HKLM "SYSTEM\CurrentControlSet\Control\Session Manager\Environment" "TUNNEX_INSTALL_DIR"
!macroend
