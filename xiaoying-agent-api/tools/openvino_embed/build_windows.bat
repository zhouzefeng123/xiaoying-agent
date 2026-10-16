@echo off
setlocal

echo [BUILD] Building OpenVINO embedding helper...

set "CMAKE_BIN=cmake"
where cmake >nul 2>nul
if %ERRORLEVEL% NEQ 0 (
    if exist "C:\Program Files\CMake\bin\cmake.exe" set "CMAKE_BIN=C:\Program Files\CMake\bin\cmake.exe"
    if exist "C:\Program Files (x86)\Microsoft Visual Studio\2022\BuildTools\Common7\IDE\CommonExtensions\Microsoft\CMake\CMake\bin\cmake.exe" set "CMAKE_BIN=C:\Program Files (x86)\Microsoft Visual Studio\2022\BuildTools\Common7\IDE\CommonExtensions\Microsoft\CMake\CMake\bin\cmake.exe"
)

"%CMAKE_BIN%" --version >nul 2>nul
if %ERRORLEVEL% NEQ 0 (
    echo [ERROR] CMake not found. Install CMake or add it to PATH.
    exit /b 1
)

set GENAI_DIR=%OpenVINOGenAI_DIR%
if "%GENAI_DIR%"=="" set GENAI_DIR=%OPENVINO_GENAI_DIR%

if "%GENAI_DIR%"=="" (
    echo [ERROR] Please set OpenVINOGenAI_DIR or OPENVINO_GENAI_DIR before building.
    exit /b 1
)

set "CMAKE_ARGS=-DOpenVINOGenAI_DIR=%GENAI_DIR%"
if defined NLOHMANN_JSON_INCLUDE_DIR set "CMAKE_ARGS=%CMAKE_ARGS% -DNLOHMANN_JSON_INCLUDE_DIR=%NLOHMANN_JSON_INCLUDE_DIR%"

"%CMAKE_BIN%" -S . -B build %CMAKE_ARGS%
if %ERRORLEVEL% NEQ 0 (
    echo [ERROR] CMake configure failed.
    exit /b 1
)

"%CMAKE_BIN%" --build build --config Release
if %ERRORLEVEL% NEQ 0 (
    echo [ERROR] CMake build failed.
    exit /b 1
)

if not exist dist mkdir dist

if exist build\Release\ov-embed.exe copy /Y build\Release\ov-embed.exe dist\ >nul
if exist build\ov-embed.exe copy /Y build\ov-embed.exe dist\ >nul

if defined OPENVINO_RUNTIME_BIN xcopy /Y /Q "%OPENVINO_RUNTIME_BIN%\*.dll" dist\ >nul
if defined OPENVINO_GENAI_RUNTIME_BIN xcopy /Y /Q "%OPENVINO_GENAI_RUNTIME_BIN%\*.dll" dist\ >nul

if exist model (
    if exist dist\model rmdir /S /Q dist\model
    xcopy /E /I /Q model dist\model >nul
)

copy /Y README.md dist\README.md >nul

echo [SUCCESS] OpenVINO embedding helper staged to backend\tools\openvino_embed\dist
echo [INFO] If the helper fails at runtime, copy the required OpenVINO/OpenVINO GenAI DLL files into the dist directory.

endlocal