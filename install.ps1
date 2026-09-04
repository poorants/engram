# engram CLIENT installer for Windows — the binary, the MCP server, the skill
# and its hooks.
#
# There are two installers and they set up two different machines:
#
#   install.sh / install.ps1   the CLIENT — every person, every machine
#   server/setup.sh            the STORE  — once, on one Linux/macOS host
#
# This is the client one. The store is Postgres in Docker and is not supported
# on Windows; a Windows machine is a client of a store running elsewhere.
#
#   irm https://raw.githubusercontent.com/poorants/engram/main/install.ps1 | iex
#
# Give it the store and the whole client setup finishes in one command. `iex`
# cannot take parameters, so pass them as environment variables before the pipe:
#
#   $env:ENGRAM_STORE_URL = 'http://host:8081'
#   $env:ENGRAM_TOKEN     = '<write token>'
#   irm https://raw.githubusercontent.com/poorants/engram/main/install.ps1 | iex
#
# Or download it and use real parameters:
#
#   irm .../install.ps1 -OutFile install.ps1
#   .\install.ps1 -Store http://host:8081 -Token <t>
#
# This is the PowerShell twin of install.sh: same release, same steps, same
# result. They exist separately because a Windows user piping a .sh into sh is a
# worse first five minutes than having the right one-liner for their shell.

[CmdletBinding()]
param(
  [string] $Store      = $env:ENGRAM_STORE_URL,
  [string] $Token      = $env:ENGRAM_TOKEN,
  [string] $Author     = $env:ENGRAM_AUTHOR,
  [string] $Version    = $env:ENGRAM_VERSION,
  [string] $InstallDir = $env:ENGRAM_INSTALL_DIR,
  [string] $Repo       = $env:ENGRAM_REPO,
  # Binary only: skip the skill, the capture hooks and the MCP registration.
  [switch] $NoClaude
)

$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest

if (-not $Repo)       { $Repo = 'poorants/engram' }
if (-not $InstallDir) { $InstallDir = Join-Path $env:LOCALAPPDATA 'engram\bin' }

function Die($msg) { Write-Host "engram install: $msg" -ForegroundColor Red; exit 1 }

# PowerShell 5.1 defaults to TLS 1.0, which GitHub refuses. Windows 10 ships 5.1.
[Net.ServicePointManager]::SecurityProtocol = [Net.SecurityProtocolType]::Tls12

# ------------------------------------------------------- 1. the binary --------
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

$asset = "engram_${Version}_windows_${arch}.zip"
$base  = "https://github.com/$Repo/releases/download/$Version"
$tmp   = Join-Path ([IO.Path]::GetTempPath()) ("engram-" + [Guid]::NewGuid().ToString('N'))
New-Item -ItemType Directory -Path $tmp -Force | Out-Null

$storeOk    = $false
$claudeWired = $false

try {
  Write-Host "engram $Version (windows/$arch)"
  try {
    Invoke-WebRequest -Uri "$base/$asset" -OutFile (Join-Path $tmp $asset) -UseBasicParsing
  } catch {
    Die "could not download $base/$asset"
  }

  # Verify the download. A truncated or tampered binary that runs anyway is
  # worse than one that fails here, because it fails later and looks like a bug.
  try {
    Invoke-WebRequest -Uri "$base/SHA256SUMS" -OutFile (Join-Path $tmp 'SHA256SUMS') -UseBasicParsing
    $line = Select-String -Path (Join-Path $tmp 'SHA256SUMS') -Pattern ([regex]::Escape($asset) + '$') -ErrorAction SilentlyContinue
    if ($line) {
      $expected = ($line.Line -split '\s+')[0]
      $actual   = (Get-FileHash -Path (Join-Path $tmp $asset) -Algorithm SHA256).Hash.ToLower()
      if ($actual -ne $expected.ToLower()) { Die "checksum mismatch for $asset - refusing to install" }
    }
  } catch {
    if ($_.Exception.Message -like '*refusing to install*') { throw }
    # No SHA256SUMS on this release: install.sh does not fail on that either.
  }

  Expand-Archive -Path (Join-Path $tmp $asset) -DestinationPath $tmp -Force
  $exe = Join-Path $tmp 'engram.exe'
  if (-not (Test-Path $exe)) { Die 'the archive did not contain engram.exe' }

  New-Item -ItemType Directory -Path $InstallDir -Force | Out-Null
  $engram = Join-Path $InstallDir 'engram.exe'
  # Replace by rename so a running process is never overwritten in place — on
  # Windows the write would simply fail with the file locked.
  Move-Item -Path $exe -Destination "$engram.new" -Force
  Move-Item -Path "$engram.new" -Destination $engram -Force

  Write-Host "installed: $engram"
  & $engram version

  # Persist PATH for future sessions, and fix it in this one so the MCP command
  # registered below resolves when a session later launches it.
  $userPath = [Environment]::GetEnvironmentVariable('Path', 'User')
  if ("$userPath" -notlike "*$InstallDir*") {
    [Environment]::SetEnvironmentVariable('Path', "$userPath;$InstallDir", 'User')
    Write-Host "added to your PATH: $InstallDir (new terminals pick it up automatically)"
  }
  if ($env:Path -notlike "*$InstallDir*") { $env:Path = "$env:Path;$InstallDir" }

  # -------------------------------------------------------- 2. the store ------
  if ($Store) {
    Write-Host ""
    Write-Host "designating the store: $Store"
    $setArgs = @('store', 'set', $Store)
    if ($Token)  { $setArgs += @('--token',  $Token) }
    if ($Author) { $setArgs += @('--author', $Author) }
    & $engram @setArgs
    # doctor proves two facts - the store answers, and this machine can write to
    # it. A setup that stops at `store set` looks finished and fails on the
    # first save, at the end of a session, which is the worst moment to find out.
    & $engram store doctor
    if ($LASTEXITCODE -eq 0) {
      $storeOk = $true
    } else {
      Write-Host ""
      Write-Host "warning: store doctor did not pass (exit $LASTEXITCODE) - the binary is" -ForegroundColor Yellow
      Write-Host "         installed, but this machine cannot use the store yet." -ForegroundColor Yellow
      Write-Host "         https://github.com/$Repo/blob/main/docs/troubleshooting.md"
    }
  }

  # ---------------------------- 3. the skill, the hooks and the MCP server ----
  # Best effort by design: a failure here leaves a working binary, and every
  # step is a command the person can run again by hand.
  $claude = Get-Command claude -ErrorAction SilentlyContinue
  if (-not $NoClaude -and $claude) {
    Write-Host ""
    Write-Host "wiring engram into Claude Code..."
    & claude plugin marketplace add $Repo *> $null
    & claude plugin install engram@engram *> $null
    $pluginOk = ($LASTEXITCODE -eq 0)
    # --scope user: the brain is not a property of one checkout. Registering it
    # per-project means the tools vanish the first time someone opens a
    # different repo, which reads as engram being broken.
    & claude mcp add --scope user engram -- engram mcp *> $null
    $mcpOk = ($LASTEXITCODE -eq 0)

    if ($pluginOk) { Write-Host "  skill + capture hooks: installed" }
    else { Write-Host "  skill: could not install - run: claude plugin install engram@engram" -ForegroundColor Yellow }
    if ($mcpOk) { Write-Host "  brain_* MCP tools: registered (user scope)" }
    else { Write-Host "  MCP: could not register - run: claude mcp add --scope user engram -- engram mcp" -ForegroundColor Yellow }
    $claudeWired = ($pluginOk -and $mcpOk)
  } elseif (-not $NoClaude) {
    Write-Host ""
    Write-Host "note: the claude CLI was not found, so the skill and the MCP server were"
    Write-Host "      not registered. After installing Claude Code, run:"
    Write-Host "        claude plugin marketplace add $Repo"
    Write-Host "        claude plugin install engram@engram"
    Write-Host "        claude mcp add --scope user engram -- engram mcp"
  }

  # ------------------------------------------------------ what is left --------
  Write-Host ""
  if ($storeOk -and $claudeWired) {
    Write-Host 'Done. A session can search and save now - try: engram search "anything"'
  } else {
    Write-Host "Next:"
    if (-not $storeOk) {
      Write-Host "  engram store set http://<host>:8081 --token <write token>"
      Write-Host "  engram store doctor"
    }
    if (-not $Store) {
      Write-Host ""
      Write-Host "No store yet? The store is Postgres in Docker and runs on Linux or macOS,"
      Write-Host "not Windows. Somebody brings one up once, for everyone:"
      Write-Host "  https://github.com/$Repo/blob/main/server/README.md"
    }
  }

  # The binary is installed and usable; a store that is not up yet, or a Claude
  # CLI that was not found, is not an install failure. install.sh exits 0 here
  # too, and the twins have to agree — a caller that checks the exit status
  # would otherwise see Windows installs "fail" for a store nobody started yet.
  # Without this the script would inherit $LASTEXITCODE from `store doctor`.
  exit 0
} finally {
  Remove-Item -Recurse -Force $tmp -ErrorAction SilentlyContinue
}
