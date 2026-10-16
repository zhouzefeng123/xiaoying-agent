@echo off
setlocal EnableDelayedExpansion

echo [PACKAGE] Starting Production Packaging...

REM 1. Build Binary
call build_prod.bat
if %ERRORLEVEL% NEQ 0 (
    echo [ERROR] Build failed. Aborting packaging.
    exit /b 1
)

REM 2. Prepare Release Directory
set RELEASE_DIR=..\release_bundle
if not exist %RELEASE_DIR% mkdir %RELEASE_DIR%

REM 3. Copy Binary
echo [COPY] Copying auramate.exe...
copy /Y auramate.exe %RELEASE_DIR%\ >nul

REM 4. Copy System Skills
echo [COPY] Copying system_skills...
if exist %RELEASE_DIR%\system_skills rmdir /S /Q %RELEASE_DIR%\system_skills
xcopy /E /I /Q system_skills %RELEASE_DIR%\system_skills >nul

REM 4.1 Copy OpenVINO Embedding Helper
echo [COPY] Copying openvino_embed...
if exist %RELEASE_DIR%\openvino_embed rmdir /S /Q %RELEASE_DIR%\openvino_embed
if exist tools\openvino_embed\dist (
    xcopy /E /I /Q tools\openvino_embed\dist %RELEASE_DIR%\openvino_embed >nul
) else (
    mkdir %RELEASE_DIR%\openvino_embed >nul 2>nul
    > %RELEASE_DIR%\openvino_embed\README.txt echo OpenVINO embedding helper has not been built yet.
)

REM 5. Copy Documentation & Scripts
echo [COPY] Copying documentation...
if exist docs\PACKAGING.md copy /Y docs\PACKAGING.md %RELEASE_DIR%\README_PACKAGING.md >nul
if exist .auramate.json (
    echo [CONFIG] Cleaning sensitive data from .auramate.json...
    powershell -Command "$json = Get-Content .auramate.json -Raw | ConvertFrom-Json; if ($json.PSObject.Properties.Match('auraAuth')) { $json.auraAuth.token = '' }; if ($json.PSObject.Properties.Match('main_model')) { $json.PSObject.Properties.Remove('main_model') }; if ($json.PSObject.Properties.Match('providers')) { foreach ($provider in $json.providers.PSObject.Properties) { if ($provider.Value -and $provider.Value.PSObject.Properties.Match('apiKey')) { $provider.Value.apiKey = '' } } }; $json | ConvertTo-Json -Depth 10 | Set-Content %RELEASE_DIR%\.auramate.json"
)

REM Create/Update README.txt for the bundle
(
echo XiaoYing Backend (Portable Edition^)
echo ===================================
echo.
echo Version: Latest
echo Build Date: %DATE% %TIME%
echo.
echo [Installation]
echo 1. Extract this folder to any location ^(e.g. D:\Software\XiaoYing^).
echo.
echo [Running]
echo - Double-click 'auramate.exe' to start interactive CLI.
echo - Double-click 'start_server.bat' to start the backend server.
echo.
echo [Data]
echo - Workspace: ./workspace ^(created automatically^)
echo - Database: ./database/database.db ^(created automatically on first run^)
echo.
echo [Note]
echo This is a portable version. No Administrator privileges or Service installation required.
) > %RELEASE_DIR%\README.txt

REM 6. Clean up (Database)
REM Ensure NO database files are present in the bundle
if exist %RELEASE_DIR%\database rmdir /S /Q %RELEASE_DIR%\database
if exist %RELEASE_DIR%\*.db del %RELEASE_DIR%\*.db
if exist %RELEASE_DIR%\*.db-shm del %RELEASE_DIR%\*.db-shm
if exist %RELEASE_DIR%\*.db-wal del %RELEASE_DIR%\*.db-wal

REM 7. Create empty directories
if not exist %RELEASE_DIR%\workspace mkdir %RELEASE_DIR%\workspace

REM Create placeholder files to ensure directories are copied
echo This directory stores workspace files. > %RELEASE_DIR%\workspace\README.txt

REM 8. Create start_server.bat
echo [CREATE] Creating start_server.bat...
(
echo @echo off
echo echo Starting XiaoYing Server...
echo auramate.exe server
echo pause
) > %RELEASE_DIR%\start_server.bat

echo.
echo [SUCCESS] Packaging complete.
pushd ..
echo Location: %CD%\release_bundle
popd
echo.
endlocal
