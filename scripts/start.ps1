param([string]$Config = 'configs/localconverter.json')
$ErrorActionPreference = 'Stop'
$projectRoot = Split-Path $PSScriptRoot -Parent
Push-Location $projectRoot
try {
  $go = Get-Command go -ErrorAction SilentlyContinue
  if (!$go -and (Test-Path -LiteralPath 'D:/Program Files/Go/bin/go.exe')) { $go = Get-Item -LiteralPath 'D:/Program Files/Go/bin/go.exe' }
  if (!$go) { throw 'Go SDK not found. Reopen the terminal after installing Go.' }
  $goExecutable = if ($go.Source) { $go.Source } else { $go.FullName }
  New-Item -ItemType Directory -Path 'D:/projects/logs' -Force | Out-Null
  $buildLog = 'D:/projects/logs/localconverter-build-' + (Get-Date -Format 'yyyyMMdd-HHmmss') + '.log'
  $env:GOCACHE = Join-Path $projectRoot '.cache/go-build'
  & $goExecutable build -o bin/localconverter.exe ./cmd/localconverter *> $buildLog
  if ($LASTEXITCODE -ne 0) { throw "Build failed. See $buildLog. Stop any previous instance before replacing the executable." }
  Write-Output 'Open http://127.0.0.1:18090/debug (or the address configured in your JSON). Press Ctrl+C to stop.'
  & .\bin\localconverter.exe -config $Config
  if ($LASTEXITCODE -ne 0) { throw 'LocalConverter exited with an error. See D:/projects/logs.' }
} finally { Pop-Location }
