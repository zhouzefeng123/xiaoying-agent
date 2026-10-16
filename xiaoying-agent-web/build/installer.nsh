!include "MUI2.nsh"
!include "LogicLib.nsh"
!include "FileFunc.nsh"
!include "nsProcess.nsh"
!include "StdUtils.nsh"

; Insert required macros for parameter parsing
!insertmacro GetParameters
!insertmacro GetOptions

!ifndef BUILD_UNINSTALLER
  Var is_overlay_install

  ; -----------------------------------------------------------------
  ; Override isUpdated macro to use our custom detection logic
  ; This ensures that electron-builder's internal skipPageIfUpdated
  ; (used for License and Directory pages) respects our detection.
  ; -----------------------------------------------------------------
  !macro _isUpdated_fixed _a _b _t _f
    StrCmp $is_overlay_install "true" `${_t}` `${_f}`
  !macroend

  !ifdef isUpdated
    !undef isUpdated
  !endif
  !define isUpdated `"" isUpdated_fixed ""`

  ; -----------------------------------------------------------------
  ; Hook for Welcome Page
  ; Note: We must wrap the Abort check in a Function because this macro 
  ; is inserted in the global page declaration area.
  ; -----------------------------------------------------------------
  !macro customWelcomePage
    Function SkipWelcomeIfOverlay
      ; Force window to foreground
      BringToFront
      ; Always skip welcome page
      Abort
    FunctionEnd
    !define MUI_PAGE_CUSTOMFUNCTION_PRE SkipWelcomeIfOverlay
    !insertmacro MUI_PAGE_WELCOME
  !macroend

  ; -----------------------------------------------------------------
  ; Hook for Install Mode Page
  ; Note: This macro is inserted INSIDE the page's Pre function 
  ; by electron-builder's multiUserUi.nsh, so we can use Abort directly.
  ; -----------------------------------------------------------------
  !macro customInstallMode
    ; Always skip install mode page
    Abort
  !macroend

  ; -----------------------------------------------------------------
  ; Hook for Finish Page
  ; -----------------------------------------------------------------
  !macro customFinishPage
    Function SkipFinishIfOverlay
        ; Check for restart flag
        IfFileExists "$TEMP\auramate_restart_flag" 0 +3
          Delete "$TEMP\auramate_restart_flag"
          ; Manually launch application if StartApp is not available or resolution fails
          ${StdUtils.ExecShellAsUser} $0 "$launchLink" "open" ""
          Quit
        Abort
    FunctionEnd
    !define MUI_PAGE_CUSTOMFUNCTION_PRE SkipFinishIfOverlay
    !insertmacro MUI_PAGE_FINISH
  !macroend

  ; -----------------------------------------------------------------
  ; Override default app running check
  ; This prevents the "App cannot be closed" dialog by electron-builder
  ; and performs a silent kill instead.
  ; -----------------------------------------------------------------
  !macro customCheckAppRunning
    DetailPrint "Checking and killing running applications..."
    ; Aggressively close the running application
    ; Loop for up to 15 seconds to ensure processes are dead
    StrCpy $R1 0
    loop_kill_check:
      IntOp $R1 $R1 + 1
      
      ; Try to kill both processes
      ${nsProcess::KillProcess} "${APP_EXECUTABLE_FILENAME}" $R0
      ${nsProcess::KillProcess} "auramate-server.exe" $R0
      
      ; Force kill with taskkill for stubborn processes (hidden window)
      nsExec::Exec 'taskkill /F /IM "${APP_EXECUTABLE_FILENAME}" /T'
      Pop $R0
      nsExec::Exec 'taskkill /F /IM "auramate-server.exe" /T'
      Pop $R0
      
      ; Sleep 500ms
      Sleep 500
      
      ; Check if main app is still running
      ${nsProcess::FindProcess} "${APP_EXECUTABLE_FILENAME}" $R0
      StrCmp $R0 "0" loop_continue_wait
      
      ; Check if server is still running
      ${nsProcess::FindProcess} "auramate-server.exe" $R0
      StrCmp $R0 "0" loop_continue_wait
      
      ; Both are gone, break loop
      Goto loop_done_kill
      
    loop_continue_wait:
      ; If we have waited 30 times (30 * 500ms = 15s), give up
      StrCmp $R1 "30" loop_done_kill
      Goto loop_kill_check
      
    loop_done_kill:
      ; Extra safety sleep to release file locks
      Sleep 1000
      
      ; CRITICAL: Force set output variable to 0 (not running)
      ; This fools any subsequent checks by electron-builder if they exist
      StrCpy $R0 "0"
  !macroend

  ; -----------------------------------------------------------------
  ; Custom Init - Detects if this is an overlay install/update
  ; -----------------------------------------------------------------
  !macro customInit
    ; Initialize our custom flags
    StrCpy $is_overlay_install "false"
    SetSilent normal

    ; 1. Check if explicitly in update mode via parameter (standard electron-builder behavior)
    Push $R0
    Push $R0
    Push $R1
    ${GetParameters} $R0
    ClearErrors
    ${GetOptions} $R0 "--updated" $R1
    IfErrors +3 0
      StrCpy $is_overlay_install "true"
      Goto skip_detection
    Pop $R1
    Pop $R0

    ; =================================================================
    ; DEBUGGING SECTION - START
    ; =================================================================
    ; Enable this block to debug path detection issues
    ; MessageBox MB_OK "DEBUG MODE: Checking Registry for previous installation...$\nAPP_GUID: ${APP_GUID}"

    ; 2. Detect existing installation in HKCU (User Mode) - GUID
    ReadRegStr $R2 HKCU "Software\Microsoft\Windows\CurrentVersion\Uninstall\${APP_GUID}" "UninstallString"
    ReadRegStr $R1 HKCU "Software\Microsoft\Windows\CurrentVersion\Uninstall\${APP_GUID}" "InstallLocation"
    ; MessageBox MB_OK "DEBUG HKCU GUID:$\nUninstall: $R2$\nLocation: $R1"
    
    StrCmp $R2 "" check_hkcu_appid
      StrCmp $R1 "" 0 has_install_location
        ; If InstallLocation is empty, try to parse it from UninstallString
        ; UninstallString usually looks like: "C:\Path\To\Uninstall XiaoYing.exe" /currentuser
        ; We need to strip the quotes and the executable name
        StrCpy $R1 $R2
        ; Remove quotes if present
        StrCpy $R0 $R1 1
        StrCmp $R0 '"' 0 +2
          StrCpy $R1 $R1 "" 1
        
        ; Find the last backslash to get the directory
        ${GetParent} "$R1" $INSTDIR
        
        StrCmp $INSTDIR "" check_hkcu_appid
        StrCpy $is_overlay_install "true"
        ; MessageBox MB_OK "DEBUG: Parsed from UninstallString!$\nTarget: $INSTDIR"
        Goto skip_detection

      has_install_location:
        StrCpy $INSTDIR $R1
        StrCpy $is_overlay_install "true"
        ; MessageBox MB_OK "DEBUG: Found in HKCU GUID!$\nTarget: $INSTDIR"
        Goto skip_detection

    check_hkcu_appid:
    ; 3. Detect by AppID in HKCU (User Mode) - AppID
    ReadRegStr $R2 HKCU "Software\Microsoft\Windows\CurrentVersion\Uninstall\com.auramate.app" "UninstallString"
    ReadRegStr $R1 HKCU "Software\Microsoft\Windows\CurrentVersion\Uninstall\com.auramate.app" "InstallLocation"
    ; MessageBox MB_OK "DEBUG HKCU AppID:$\nUninstall: $R2$\nLocation: $R1"

    StrCmp $R2 "" check_hklm_guid
      StrCmp $R1 "" check_hklm_guid
        StrCpy $INSTDIR $R1
        StrCpy $is_overlay_install "true"
        ; MessageBox MB_OK "DEBUG: Found in HKCU AppID!$\nTarget: $INSTDIR"
        Goto skip_detection

    check_hklm_guid:
    ; 4. Detect in HKLM (Machine Mode) - GUID
    ReadRegStr $R2 HKLM "Software\Microsoft\Windows\CurrentVersion\Uninstall\${APP_GUID}" "UninstallString"
    ReadRegStr $R1 HKLM "Software\Microsoft\Windows\CurrentVersion\Uninstall\${APP_GUID}" "InstallLocation"
    ; MessageBox MB_OK "DEBUG HKLM GUID:$\nUninstall: $R2$\nLocation: $R1"

    StrCmp $R2 "" check_hklm_appid
      StrCmp $R1 "" check_hklm_appid
        StrCpy $INSTDIR $R1
        StrCpy $is_overlay_install "true"
        ; MessageBox MB_OK "DEBUG: Found in HKLM GUID!$\nTarget: $INSTDIR"
        Goto skip_detection

    check_hklm_appid:
    ; 5. Detect by AppID in HKLM (Machine Mode) - AppID
    ReadRegStr $R2 HKLM "Software\Microsoft\Windows\CurrentVersion\Uninstall\com.auramate.app" "UninstallString"
    ReadRegStr $R1 HKLM "Software\Microsoft\Windows\CurrentVersion\Uninstall\com.auramate.app" "InstallLocation"
    ; MessageBox MB_OK "DEBUG HKLM AppID:$\nUninstall: $R2$\nLocation: $R1"

    StrCmp $R2 "" done_checks
      StrCmp $R1 "" done_checks
        StrCpy $INSTDIR $R1
        StrCpy $is_overlay_install "true"
        ; MessageBox MB_OK "DEBUG: Found in HKLM AppID!$\nTarget: $INSTDIR"

    done_checks:
    ; MessageBox MB_OK "DEBUG Check Finished.\nOverlay: $is_overlay_install$\nFinal INSTDIR: $INSTDIR"
    ; =================================================================
    ; DEBUGGING SECTION - END
    ; =================================================================
    skip_detection:
    BringToFront

    ; =================================================================
    ; Aggressively close the running application (Early Stage)
    ; This is a duplicate of customCheckAppRunning to ensure processes
    ; are dead before any UI or checks run.
    ; =================================================================
    StrCpy $R1 0
    loop_kill_check_init:
      IntOp $R1 $R1 + 1
      
      ; Try to kill both processes
      ${nsProcess::KillProcess} "${APP_EXECUTABLE_FILENAME}" $R0
      ${nsProcess::KillProcess} "auramate-server.exe" $R0
      
      ; Force kill with taskkill
      nsExec::Exec 'taskkill /F /IM "${APP_EXECUTABLE_FILENAME}" /T'
      Pop $R0
      nsExec::Exec 'taskkill /F /IM "auramate-server.exe" /T'
      Pop $R0

      ; NUCLEAR OPTION: Use PowerShell to kill ANY process running from the install directory
      ; Only run if INSTDIR is set (which it should be now!)
      StrCmp $INSTDIR "" skip_nuclear_kill
        ; Use double dollars for PowerShell variable to prevent NSIS from interpreting it
        nsExec::Exec 'powershell -NoProfile -NonInteractive -Command "Get-CimInstance -ClassName Win32_Process | Where-Object { $$_.Path -and $$_.Path.StartsWith($\'$INSTDIR$\') } | Stop-Process -Force -ErrorAction SilentlyContinue"'
        Pop $R0
      skip_nuclear_kill:
      
      Sleep 500
      
      ; Check processes
      ${nsProcess::FindProcess} "${APP_EXECUTABLE_FILENAME}" $R0
      StrCmp $R0 "0" loop_continue_wait_init
      
      ${nsProcess::FindProcess} "auramate-server.exe" $R0
      StrCmp $R0 "0" loop_continue_wait_init
      
      ; Try to delete the executable to verify it's gone and release locks
      ; This is the ultimate check - if we can delete it, it's definitely not running/locked
      StrCmp $INSTDIR "" skip_delete_check
        ClearErrors
        Delete "$INSTDIR\${APP_EXECUTABLE_FILENAME}"
        IfErrors loop_continue_wait_init
      skip_delete_check:
      
      Goto loop_done_kill_init
      
    loop_continue_wait_init:
      ; Wait up to 15s
      StrCmp $R1 "30" loop_done_kill_init
      Goto loop_kill_check_init
      
    loop_done_kill_init:
      Sleep 1000
    ; =================================================================

    ; =================================================================
    ; PREVENT UNINSTALLER FROM RUNNING
    ; To preserve user data (database/config), we must prevent the old 
    ; uninstaller from running, as it might wipe the directory.
    ; We do this by temporarily hiding the UninstallString from the registry.
    ; The new installation will overwrite files and recreate registry keys.
    ; =================================================================
    StrCmp $is_overlay_install "true" 0 skip_hide_uninstaller
      ; We don't know exactly which key was used, so we try to nuke the UninstallString from all potential locations.
      ; This forces electron-builder to skip the uninstallation step.
      
      DeleteRegValue HKCU "Software\Microsoft\Windows\CurrentVersion\Uninstall\${APP_GUID}" "UninstallString"
      DeleteRegValue HKLM "Software\Microsoft\Windows\CurrentVersion\Uninstall\${APP_GUID}" "UninstallString"
      DeleteRegValue HKCU "Software\Microsoft\Windows\CurrentVersion\Uninstall\com.auramate.app" "UninstallString"
      DeleteRegValue HKLM "Software\Microsoft\Windows\CurrentVersion\Uninstall\com.auramate.app" "UninstallString"
      
      DetailPrint "Bypassed uninstaller to preserve user data."
    skip_hide_uninstaller:

    ; =================================================================
    ; BACKUP PACKAGED CONFIGURATION TEMPLATE (.auramate.json)
    ; =================================================================
    StrCmp $INSTDIR "" skip_config_backup
      IfFileExists "$INSTDIR\resources\.auramate.json" 0 skip_config_backup
        CopyFiles /SILENT "$INSTDIR\resources\.auramate.json" "$TEMP\.auramate.json.bak"
        DetailPrint "Backed up .auramate.json to temp"
    skip_config_backup:

    ; =================================================================
    ; BACKUP DATA (New Logic - MOVED AFTER KILL)
    ; Now that processes are dead, it is safe to backup database files.
    ; =================================================================
    ; Logic removed as we rely on registry deletion method to bypass uninstaller
    
    ; If detected, we can log or do other things.
    ; The page skipping is handled by the macros above checking $is_overlay_install.
  !macroend

  !macro customInstall
    ; =================================================================
    ; RESTORE PACKAGED CONFIGURATION TEMPLATE (.auramate.json)
    ; =================================================================
    IfFileExists "$TEMP\.auramate.json.bak" 0 skip_config_restore
      CopyFiles /SILENT "$TEMP\.auramate.json.bak" "$INSTDIR\resources\.auramate.json"
      Delete "$TEMP\.auramate.json.bak"
      DetailPrint "Restored .auramate.json from backup"
    skip_config_restore:
  !macroend
!endif

!ifdef BUILD_UNINSTALLER
  !macro customUnInstall
    IfFileExists "$APPDATA\${PRODUCT_NAME}" 0 skip_remove_user_data
      RMDir /r "$APPDATA\${PRODUCT_NAME}"
      DetailPrint "Removed user data directory: $APPDATA\${PRODUCT_NAME}"
    skip_remove_user_data:
    IfFileExists "$APPDATA\client" 0 skip_remove_legacy_user_data
      RMDir /r "$APPDATA\client"
      DetailPrint "Removed legacy user data directory: $APPDATA\client"
    skip_remove_legacy_user_data:
  !macroend
!endif
