param([switch]$Force)
$ErrorActionPreference = 'Stop'
$projectRoot = [IO.Path]::GetFullPath((Split-Path $PSScriptRoot -Parent))
$toolRoot = Join-Path $projectRoot 'tools'
$downloadRoot = Join-Path $toolRoot '.downloads'
New-Item -ItemType Directory -Path $downloadRoot -Force | Out-Null

# Portable, pinned packages. Nothing is installed globally or added to PATH.
$packages = @(
  @{
    Name = 'mediamtx'; URL = 'https://github.com/bluenviron/mediamtx/releases/download/v1.21.2/mediamtx_v1.21.2_windows_amd64.zip'
    SHA256 = 'A84224F5A490855684E62BA6FB71E80EA2A5E16AED61883BB58380290E93E467'
    Executable = 'mediamtx.exe'
  },
  @{
    Name = 'ffmpeg'; URL = 'https://www.gyan.dev/ffmpeg/builds/ffmpeg-release-essentials.zip'
    SHA256 = '60F467265B1E312373DBCD92200C2618A74850F98D3D078E94296BB3FA2047BA'
    Executable = 'ffmpeg.exe'
  }
)
foreach ($package in $packages) {
  $destination = Join-Path $toolRoot $package.Name
  if (!$Force -and (Test-Path -LiteralPath (Join-Path $destination $package.Executable))) {
    Write-Output "$($package.Name) already exists. Use -Force to reinstall."
    continue
  }
  $archive = Join-Path $downloadRoot ($package.Name + '-pinned.zip')
  Invoke-WebRequest -Uri $package.URL -OutFile $archive -TimeoutSec 240
  if ((Get-FileHash -LiteralPath $archive -Algorithm SHA256).Hash -ne $package.SHA256) {
    throw "$($package.Name) checksum mismatch. Package alias may have changed; review the version and official checksum before updating this script."
  }
  $extraction = Join-Path $downloadRoot ($package.Name + '-verified')
  Expand-Archive -LiteralPath $archive -DestinationPath $extraction -Force
  New-Item -ItemType Directory -Path $destination -Force | Out-Null
  foreach ($name in @($package.Executable, 'ffprobe.exe', 'LICENSE')) {
    $file = Get-ChildItem -LiteralPath $extraction -Recurse -File -Filter $name | Select-Object -First 1
    if ($file) { Copy-Item -LiteralPath $file.FullName -Destination $destination -Force }
  }
  if (!(Test-Path -LiteralPath (Join-Path $destination $package.Executable))) { throw "$($package.Name) executable missing" }
  Write-Output "$($package.Name) installed after SHA256 verification."
}
