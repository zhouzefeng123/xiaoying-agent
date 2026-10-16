@echo off
setlocal

echo [FULL BUILD] Starting Full Build (Backend + Client)...

REM 1. Build Backend (using existing script)
echo [BACKEND] Running package_prod.bat...
call package_prod.bat
if %ERRORLEVEL% NEQ 0 (
    echo [ERROR] Backend packaging failed.
    exit /b 1
)

REM 2. Build Client
echo [CLIENT] Building Client...
pushd ..\client

REM Check if node_modules exists to skip install if possible, but for full build usually we want to ensure deps
REM Always install to ensure devDependencies like rcedit are present
echo [CLIENT] Installing/Updating dependencies...
call npm install
if %ERRORLEVEL% NEQ 0 (
    echo [ERROR] Client dependency installation failed.
    popd
    exit /b 1
)

echo [VERSION] Bumping version...
call node scripts/bump-version.js
if %ERRORLEVEL% NEQ 0 (
    echo [ERROR] Version bump failed.
    popd
    exit /b 1
)

echo [CLIENT] Packaging Electron app (Build + Package)...
REM This runs 'tsc -b && vite build' then 'electron-builder'
call npm run package
if %ERRORLEVEL% NEQ 0 (
    echo [ERROR] Client packaging failed.
    popd
    exit /b 1
)

popd

echo.
echo [SUCCESS] Full build complete!
echo.
echo Artifacts locations:
echo - Backend Bundle: ..\release_bundle
echo - Client Installer: ..\client\release-v2
echo.
endlocal
