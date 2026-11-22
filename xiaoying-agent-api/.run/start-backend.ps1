# Local dev launcher for the AuraMate Go backend.
# Keeps the OpenVINO embedding runtime wiring in one place so `go run`/exe
# restarts behave the same way every time.

$ErrorActionPreference = "Stop"

$apiDir = Split-Path -Parent $PSScriptRoot

# Load the local mail configuration without evaluating file contents as code.
# Explicit process environment variables take precedence over the local file.
$mailEnvPath = Join-Path $apiDir '.env'
if (Test-Path -LiteralPath $mailEnvPath) {
  foreach ($mailEnvLine in Get-Content -LiteralPath $mailEnvPath -Encoding UTF8) {
    if ($mailEnvLine -match '^\s*(AURAMATE_(?:SMTP_[A-Z_]+|AUTH_ALLOWED_EMAILS|AUTH_EMAIL|COOKIE_SECURE))\s*=(.*)$') {
      $mailEnvName = $matches[1]
      $mailEnvValue = $matches[2].Trim()
      if ($mailEnvValue.Length -ge 2 -and (($mailEnvValue.StartsWith('"') -and $mailEnvValue.EndsWith('"')) -or ($mailEnvValue.StartsWith("'") -and $mailEnvValue.EndsWith("'")))) {
        $mailEnvValue = $mailEnvValue.Substring(1, $mailEnvValue.Length - 2)
      }
      if ([string]::IsNullOrEmpty([Environment]::GetEnvironmentVariable($mailEnvName, 'Process'))) {
        [Environment]::SetEnvironmentVariable($mailEnvName, $mailEnvValue, 'Process')
      }
    }
  }
}

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
