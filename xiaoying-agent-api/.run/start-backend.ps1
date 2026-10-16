# Local dev launcher for the AuraMate Go backend.
# Keeps the OpenVINO embedding runtime wiring in one place so `go run`/exe
# restarts behave the same way every time.

$ErrorActionPreference = "Stop"

$apiDir = Split-Path -Parent $PSScriptRoot
$runtime = "C:\Users\32644\AppData\Local\Programs\AuraMate\resources\openvino_embed"

$env:AURAMATE_OPENVINO_EMBED_BINARY_PATH = Join-Path $runtime "ov-embed.exe"
$env:AURAMATE_OPENVINO_EMBED_MODEL_DIR = Join-Path $runtime "models\paraphrase-multilingual-MiniLM-L12-v2"
$env:AURAMATE_OPENVINO_EMBED_DEVICE = "CPU"

$port = if ($args.Count -ge 1) { $args[0] } else { 3000 }
$exe = Join-Path $apiDir "auramate-dev.exe"

if (-not (Test-Path $exe)) {
  throw "auramate-dev.exe not found at $exe. Build it with: go build -o auramate-dev.exe ."
}

Set-Location $apiDir
& $exe server --port $port
