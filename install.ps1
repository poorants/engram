# engram binary installer for Windows — puts the one engram.exe the plugin's
# capture hooks run into %LOCALAPPDATA%\engram\bin.
#
# The brain is remote: a session uses the `engram` MCP server (engram serve)
# over HTTP with its own login, and this machine holds no store address and no
# token. The binary does one thing here — `engram hook` — and never touches the
# network. Registering the MCP server and installing the plugin are not this
# script's job; the server's /mcp__engram__setup prompt does them and runs this.
#
#   irm https://raw.githubusercontent.com/poorants/engram/main/install.ps1 | iex
#
# Re-running it is the upgrade; the same version is a no-op. `iex` cannot take
# parameters, so pass them as environment variables before the pipe
# (ENGRAM_VERSION, ENGRAM_INSTALL_DIR, ENGRAM_REPO), or download the script and
# use -Version / -InstallDir / -Repo / -Force.
#
# This is the PowerShell twin of install.sh: same release, same steps, same
# result — different code with the same intent, so a fix to one is checked
# against the other.

[CmdletBinding()]
param(
  [string] $Version    = $env:ENGRAM_VERSION,
  [string] $InstallDir = $env:ENGRAM_INSTALL_DIR,
  [string] $Repo       = $env:ENGRAM_REPO,
  [switch] $Force,
  # Accepted and ignored: the installer no longer wires Claude Code, so
  # "binary only" is all it ever does. Old invocations keep working.
  [switch] $NoClaude
)

$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest

if (-not $Repo)       { $Repo = 'poorants/engram' }
if (-not $InstallDir) { $InstallDir = Join-Path $env:LOCALAPPDATA 'engram\bin' }

function Die($msg) { Write-Host "engram install: $msg" -ForegroundColor Red; exit 1 }

# PowerShell 5.1 defaults to TLS 1.0, which GitHub refuses. Windows 10 ships 5.1.
[Net.ServicePointManager]::SecurityProtocol = [Net.SecurityProtocolType]::Tls12

# ------------------------------------------------------- 0. preflight ---------
switch ($env:PROCESSOR_ARCHITECTURE) {
  'AMD64' { $arch = 'amd64' }
  'ARM64' { $arch = 'arm64' }
  'x86'   { Die '32-bit Windows is not supported (releases carry amd64 and arm64)' }
  default { Die "unsupported architecture: $env:PROCESSOR_ARCHITECTURE" }
}

if (-not $Version) {
  # Follow the /releases/latest redirect and read the tag out of where it lands.
  # That is deliberate: the obvious alternative, the GitHub API, is rate-limited
  # per IP for unauthenticated callers, which fails on exactly the shared office
  # network where several people install on the same day.
  try {
    $resp = Invoke-WebRequest -Uri "https://github.com/$Repo/releases/latest" -MaximumRedirection 0 -ErrorAction SilentlyContinue -UseBasicParsing
    $location = $resp.Headers.Location
  } catch {
    $location = $_.Exception.Response.Headers.Location
  }
  if ("$location" -match '/tag/(.+)$') { $Version = $Matches[1] }
  if (-not $Version) { Die 'could not determine the latest release; pass -Version vX.Y.Z' }
}

$engram = Join-Path $InstallDir 'engram.exe'

# ------------------------------------------------------- 1. the binary --------
# The same version is not downloaded again: install = upgrade = check has to be
# one command, so it can simply be run again whenever in doubt.
$current = ''
if (Test-Path $engram) {
  try { $current = ((& $engram version 2>$null) -split '\s+')[1] } catch { $current = '' }
}

if ($current -and $current -eq $Version -and -not $Force) {
  Write-Host "engram $Version is already installed: $engram (-Force to download again)"
} else {
  $asset = "engram_${Version}_windows_${arch}.zip"
  $base  = "https://github.com/$Repo/releases/download/$Version"
  $tmp   = Join-Path ([IO.Path]::GetTempPath()) ("engram-" + [Guid]::NewGuid().ToString('N'))
  New-Item -ItemType Directory -Path $tmp -Force | Out-Null
  try {
    if ($current) { Write-Host "upgrading engram $current -> $Version (windows/$arch)" }
    else { Write-Host "engram $Version (windows/$arch)" }
    try {
      Invoke-WebRequest -Uri "$base/$asset" -OutFile (Join-Path $tmp $asset) -UseBasicParsing
    } catch {
      Die "could not download $base/$asset"
    }

    # Verify the download. A truncated or tampered binary that runs anyway is
    # worse than one that fails here, because it fails later and looks like a bug.
    try {
      Invoke-WebRequest -Uri "$base/SHA256SUMS" -OutFile (Join-Path $tmp 'SHA256SUMS') -UseBasicParsing
    } catch {
      Die "could not download $base/SHA256SUMS"
    }
    $line = Select-String -Path (Join-Path $tmp 'SHA256SUMS') -Pattern (' ' + [regex]::Escape($asset) + '$') -ErrorAction SilentlyContinue
    if (-not $line) { Die "SHA256SUMS has no entry for $asset" }
    $expected = (@($line)[0].Line -split '\s+')[0]
    $actual   = (Get-FileHash -Path (Join-Path $tmp $asset) -Algorithm SHA256).Hash.ToLower()
    if ($actual -ne $expected.ToLower()) { Die "checksum mismatch for $asset - refusing to install" }

    Expand-Archive -Path (Join-Path $tmp $asset) -DestinationPath $tmp -Force
    $exe = Join-Path $tmp 'engram.exe'
    if (-not (Test-Path $exe)) { Die 'the archive did not contain engram.exe' }

    New-Item -ItemType Directory -Path $InstallDir -Force | Out-Null
    # Windows will not let a running executable be deleted or overwritten — a
    # hook may be running engram.exe at this very moment. What Windows does
    # allow is *renaming* that running file: the process keeps its handle and
    # keeps working, and the name is freed.
    #
    # So the old binary moves aside first and the new one takes the name it
    # vacated. The reverse — moving the new file straight onto the live name —
    # is what cannot work, with or without -Force: -Force deletes the
    # destination first, and deleting the locked file is the one thing Windows
    # refuses.
    #
    # Sweep what earlier upgrades parked here, then pick a name nothing holds.
    # Both are best effort: a copy still held by a running process refuses to
    # go, and the next upgrade sweeps it.
    Get-ChildItem "$engram.old*" -ErrorAction SilentlyContinue |
      ForEach-Object { Remove-Item $_.FullName -Force -ErrorAction SilentlyContinue }
    $parked = "$engram.old"
    if (Test-Path $parked) { $parked = "$engram.old-" + [Guid]::NewGuid().ToString('N').Substring(0, 8) }
    if (Test-Path $engram) { Move-Item -Path $engram -Destination $parked }
    try {
      Move-Item -Path $exe -Destination $engram
    } catch {
      # Put the working binary back. A failed upgrade should cost the new
      # version, never the one that was already running fine.
      if (Test-Path $parked) { Move-Item -Path $parked -Destination $engram -Force }
      Die "could not install to $engram — $($_.Exception.Message)"
    }
    Remove-Item $parked -Force -ErrorAction SilentlyContinue
    Write-Host "installed: $engram"
  } finally {
    Remove-Item -Recurse -Force $tmp -ErrorAction SilentlyContinue
  }
}
& $engram version

# Persist PATH for future sessions: the hooks find engram there.
$userPath = [Environment]::GetEnvironmentVariable('Path', 'User')
if ("$userPath" -notlike "*$InstallDir*") {
  [Environment]::SetEnvironmentVariable('Path', "$userPath;$InstallDir", 'User')
  Write-Host "added to your PATH: $InstallDir (new terminals pick it up automatically)"
}

# ------------------------------------------------ 2. 0.x leftovers ------------
# Up to 0.11 the client kept a store address and a token on this machine and
# registered a local stdio MCP server. None of it is read any more. The files
# are only pointed out, never deleted: store.token is a credential, and whether
# it is still used elsewhere is the owner's call.
$cfgRoot = $env:ENGRAM_CONFIG_DIR
if (-not $cfgRoot) { $cfgRoot = $env:CLAUDE_CONFIG_DIR }
if (-not $cfgRoot) { $cfgRoot = Join-Path $HOME '.claude' }
foreach ($name in 'config.json', 'store.token') {
  $f = Join-Path (Join-Path $cfgRoot 'engram') $name
  if (Test-Path $f) {
    Write-Host ""
    Write-Host "note: $f is left over from the old client and is no longer read." -ForegroundColor Yellow
    Write-Host "      Remove it when nothing else uses it (store.token is the store's credential)."
  }
}

if (Get-Command claude -ErrorAction SilentlyContinue) {
  # claude writes ordinary progress to stderr, and under
  # $ErrorActionPreference = 'Stop' Windows PowerShell 5.1 turns a native
  # command's stderr into a terminating error even when it is redirected.
  # This check is advice, so nothing in it may be fatal.
  $ErrorActionPreference = 'Continue'
  try {
    $reg = (& claude mcp get engram 2>$null) -join "`n"
  } catch {
    $reg = ''
  } finally {
    $ErrorActionPreference = 'Stop'
  }
  if ($reg -match '(?i)type:\s*stdio|command:\s*engram') {
    Write-Host ""
    Write-Host "note: engram is still registered as a local (stdio) MCP server, which this" -ForegroundColor Yellow
    Write-Host "      binary no longer implements. Replace it with the remote server:"
    Write-Host "        claude mcp remove engram -s user"
    Write-Host "        claude mcp add --transport http --scope user --callback-port 33418 engram https://<host>/mcp"
  }
}

Write-Host ""
Write-Host "Done. In Claude Code, /mcp__engram__setup checks the rest (the MCP"
Write-Host "registration and the plugin); restart Claude Code after it."
exit 0
