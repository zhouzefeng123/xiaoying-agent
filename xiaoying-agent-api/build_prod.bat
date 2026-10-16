@echo off
setlocal

echo [BUILD] Starting Production Build...

echo [1/2] Compiling Binary (Prod Mode)...
REM We use -tags prod to activate admin_windows_prod.go and disable admin_windows_dev.go
REM -ldflags="-s -w": Reduce binary size, strip debug info
go build -tags prod -ldflags="-s -w" -o auramate.exe main.go
if %ERRORLEVEL% NEQ 0 (
    echo [ERROR] Build failed.
    exit /b 1
)

echo [DEBUG] Build finished. Checking for auramate.exe...
if exist auramate.exe (
    echo [DEBUG] auramate.exe found.
) else (
    echo [ERROR] auramate.exe NOT found!
    dir
)

echo [SUCCESS] Build complete: auramate.exe
echo This binary runs without requiring Administrator privileges (UAC disabled).
endlocal
