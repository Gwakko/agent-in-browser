# agent-in-browser installer for Windows (PowerShell 5+).
#
#   irm https://raw.githubusercontent.com/gwakko/agent-in-browser/main/scripts/install.ps1 | iex
#   & ([scriptblock]::Create((irm .../install.ps1))) -Version v0.1.0 -Setup claude-code
param(
  [string]$Version = $(if ($env:AIB_VERSION) { $env:AIB_VERSION } else { "latest" }),
  [string]$Dir = $(if ($env:AIB_DIR) { $env:AIB_DIR } else { Join-Path $env:LOCALAPPDATA "Programs\agent-in-browser" }),
  [string]$Setup = $env:AIB_SETUP,
  [string]$Skill = $env:AIB_SKILL
)
$ErrorActionPreference = "Stop"
$Repo = "gwakko/agent-in-browser"
$Bin = "agent-in-browser"

$arch = if ([System.Environment]::Is64BitOperatingSystem) {
  if ($env:PROCESSOR_ARCHITECTURE -eq "ARM64") { "arm64" } else { "amd64" }
} else { throw "32-bit Windows is not supported" }

if ($Version -eq "latest") {
  $Version = (Invoke-RestMethod "https://api.github.com/repos/$Repo/releases/latest").tag_name
}
$num = $Version.TrimStart("v")
$asset = "${Bin}_${num}_windows_${arch}.zip"
$base = "https://github.com/$Repo/releases/download/$Version"
$tmp = Join-Path ([System.IO.Path]::GetTempPath()) ("aib-" + [guid]::NewGuid())
New-Item -ItemType Directory -Path $tmp | Out-Null

Write-Host "==> downloading $asset ($Version)"
Invoke-WebRequest "$base/$asset" -OutFile (Join-Path $tmp $asset)
Invoke-WebRequest "$base/checksums.txt" -OutFile (Join-Path $tmp "checksums.txt")

$expected = (Select-String -Path (Join-Path $tmp "checksums.txt") -Pattern " $([regex]::Escape($asset))$").Line.Split(" ")[0]
$actual = (Get-FileHash (Join-Path $tmp $asset) -Algorithm SHA256).Hash.ToLower()
if ($expected -ne $actual) { throw "checksum mismatch for $asset" }

Expand-Archive (Join-Path $tmp $asset) -DestinationPath $tmp -Force
New-Item -ItemType Directory -Force -Path $Dir | Out-Null
Copy-Item (Join-Path $tmp "$Bin.exe") (Join-Path $Dir "$Bin.exe") -Force
Copy-Item (Join-Path $tmp "$Bin.exe") (Join-Path $Dir "aib.exe") -Force
Remove-Item $tmp -Recurse -Force

$userPath = [Environment]::GetEnvironmentVariable("Path", "User")
if (-not ($userPath -split ";" | Where-Object { $_ -eq $Dir })) {
  [Environment]::SetEnvironmentVariable("Path", "$userPath;$Dir", "User")
  $env:Path += ";$Dir"
  Write-Host "==> added $Dir to your user PATH (restart terminals to pick it up)"
}

$exe = Join-Path $Dir "$Bin.exe"
Write-Host "==> installed $exe — $(& $exe version)"
& $exe browsers
if ($Setup) { & $exe setup $Setup --write }
if ($Skill) { & $exe skill install --for $Skill }

Write-Host ""
Write-Host "Next steps:"
Write-Host "  $Bin setup claude-code --write     # or: cursor, codex, gemini, windsurf, claude-desktop"
Write-Host "  $Bin skill install --for claude"
Write-Host "  $Bin call navigate url=example.com"
