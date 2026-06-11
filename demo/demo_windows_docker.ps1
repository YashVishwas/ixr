#Requires -Version 5.1
# demo_windows_docker.ps1 — Docker-based ixr demo for Windows (linux/amd64 via Docker Desktop).
#
# Usage (from repo root):  .\demo\demo_windows_docker.ps1 [branch-name]
#
# If execution policy blocks the script:
#   Set-ExecutionPolicy -ExecutionPolicy RemoteSigned -Scope CurrentUser
#
# Requires: git, Docker Desktop for Windows, python3 (or python)
# API keys: set at least one of GROQ_API_KEY, CEREBRAS_API_KEY, MISTRAL_API_KEY,
#           SAMBANOVA_API_KEY, OPENAI_API_KEY, ANTHROPIC_API_KEY, GOOGLE_API_KEY

param([string]$BranchArg = "")

Set-StrictMode -Version Latest
$ErrorActionPreference = "Stop"

$RepoRoot      = (git -C (Split-Path $MyInvocation.MyCommand.Path) rev-parse --show-toplevel) -replace '/', '\'
$DemoDir       = Join-Path $RepoRoot "demo"
$Port          = 8088
$WorktreeBase  = Join-Path $env:TEMP "ixr-demo-windows-docker"
$ContainerName = "ixr-demo-windows"
$ImageTag      = "ixr-demo:windows"
$LogFile = Join-Path $env:TEMP "ixr-windows-docker.log"
$Script:Branch = ""
$Script:WorktreeBaseOverride = $WorktreeBase
$Script:DockerLogProcess = $null

function Write-Bold  { param($Text) Write-Host $Text -NoNewline }
function Write-Cyan  { param($Text) Write-Host $Text -ForegroundColor Cyan -NoNewline }
function Write-Green { param($Text) Write-Host $Text -ForegroundColor Green -NoNewline }
function Write-Yellow{ param($Text) Write-Host $Text -ForegroundColor Yellow -NoNewline }
function Write-Red   { param($Text) Write-Host $Text -ForegroundColor Red -NoNewline }

function Check-Platform {
  $os = [System.Environment]::OSVersion.Platform
  if ($os -ne [System.PlatformID]::Win32NT) {
    Write-Host ""; Write-Yellow "  Warning: not running on Windows. Use a bash Docker script instead."; Write-Host ""
    $ans = Read-Host "  Continue anyway? [y/N]"
    if ($ans -notmatch '^[Yy]$') { exit 0 }
  }
}

function Check-Docker {
  if (-not (Get-Command docker -ErrorAction SilentlyContinue)) {
    Write-Host "  " -NoNewline; Write-Red "Error: docker not found. Install Docker Desktop from https://docker.com"; Write-Host ""; exit 1
  }
  try { docker info 2>&1 | Out-Null } catch {
    Write-Host "  " -NoNewline; Write-Red "Error: Docker daemon not running. Start Docker Desktop and try again."; Write-Host ""; exit 1
  }
}

function Invoke-Cleanup {
  if ($Script:DockerLogProcess -and -not $Script:DockerLogProcess.HasExited) {
    try { $Script:DockerLogProcess.Kill() } catch {}
  }
  docker stop $ContainerName 2>$null
  docker rm   $ContainerName 2>$null
  $base = $Script:WorktreeBaseOverride
  if ((Test-Path $base) -and ($base -ne $RepoRoot)) {
    Write-Host "  Removing worktree $base..."
    git -C $RepoRoot worktree remove --force $base 2>$null
    if (Test-Path $base) { Remove-Item -Recurse -Force $base -ErrorAction SilentlyContinue }
  }
}

function Print-BranchInfo {
  Write-Host ""
  Write-Host "  " -NoNewline; Write-Bold "Available branches and their features:"; Write-Host ""
  Write-Host "  " -NoNewline; Write-Cyan "main"; Write-Host "         — OpenAI-compatible proxy, 12 provider adapters, auto-routing catalog"
  Write-Host "  " -NoNewline; Write-Cyan "phase-2"; Write-Host "      — +circuit breaker, intent parser, scoring engine, rate limiting"
  Write-Host "  " -NoNewline; Write-Cyan "phase-2_2"; Write-Host "    — +shadow testing, streaming, telemetry, config hot-reload"
  Write-Host "  " -NoNewline; Write-Cyan "phase-2_3"; Write-Host "    — +OTEL, Prometheus, semantic cache, Bedrock/Ollama, embeddings"
  Write-Host ""
  Write-Host "  " -NoNewline; Write-Yellow "Note:"; Write-Host " each branch is a superset of the previous."; Write-Host ""
}

function Select-Branch {
  $branches = @()
  $localLines = git -C $RepoRoot branch 2>$null
  foreach ($line in $localLines) {
    $b = $line.Trim(); if ($b.StartsWith('* ')) { $b = $b.Substring(2) }
    if ($b) { $branches += $b }
  }
  $remoteLines = git -C $RepoRoot branch -r 2>$null
  foreach ($line in $remoteLines) {
    $ref = $line.Trim() -replace '^remotes/origin/', ''
    if ($ref.StartsWith('HEAD')) { continue }
    if ($branches -notcontains $ref) { $branches += $ref }
  }
  Write-Host "  " -NoNewline; Write-Bold "Select a branch to demo:"; Write-Host ""; Write-Host ""
  for ($i = 0; $i -lt $branches.Count; $i++) { Write-Host ("    {0,2})  {1}" -f ($i+1), $branches[$i]) }
  Write-Host ""; $choice = Read-Host "  Enter number [default: main, q to quit]"
  if ($choice -eq 'q' -or $choice -eq 'Q') { Write-Host ""; Write-Green "  Bye."; Write-Host ""; exit 0 }
  elseif ([string]::IsNullOrWhiteSpace($choice)) { $Script:Branch = "main" }
  elseif ($choice -match '^\d+$' -and [int]$choice -ge 1 -and [int]$choice -le $branches.Count) {
    $Script:Branch = $branches[[int]$choice - 1]
  } else { $Script:Branch = "main" }
}

function Setup-Worktree {
  Write-Host ""; Write-Host "  " -NoNewline; Write-Bold "Setting up worktree for branch:"; Write-Host " " -NoNewline; Write-Cyan $Script:Branch; Write-Host ""
  $currentBranch = (git -C $RepoRoot branch --show-current 2>$null) -join ""
  if ($currentBranch -eq $Script:Branch) {
    Write-Host "  Branch is the current worktree — running from $RepoRoot"
    $Script:WorktreeBaseOverride = $RepoRoot; return
  }
  $Script:WorktreeBaseOverride = $WorktreeBase
  if (Test-Path $WorktreeBase) {
    git -C $RepoRoot worktree remove --force $WorktreeBase 2>$null
    if (Test-Path $WorktreeBase) { Remove-Item -Recurse -Force $WorktreeBase -ErrorAction SilentlyContinue }
  }
  $ref = $Script:Branch
  $check = git -C $RepoRoot rev-parse --verify $ref 2>$null
  if (-not $check) { $ref = "origin/$($Script:Branch)" }
  git -C $RepoRoot worktree add $WorktreeBase $ref 2>&1 | ForEach-Object { Write-Host "    $_" }
  Write-Host "  Worktree ready at $WorktreeBase"
}

function Build-Ixr {
  $base = $Script:WorktreeBaseOverride
  Write-Host ""; Write-Host "  " -NoNewline; Write-Bold "Building Docker image (linux/amd64)..."; Write-Host ""
  docker build --platform linux/amd64 -t $ImageTag $base 2>&1 | ForEach-Object { Write-Host "    $_" }
  Write-Host "  " -NoNewline; Write-Green "Image built."; Write-Host ""
}

function Write-DemoConfig {
  $base = $Script:WorktreeBaseOverride
  $yaml = @"
server:
  port: $Port

log_level: warn

auth:
  disable_auth: true

providers:
  openai:
    api_key: "$($env:OPENAI_API_KEY)"
  anthropic:
    api_key: "$($env:ANTHROPIC_API_KEY)"
  gemini:
    api_key: "$($env:GOOGLE_API_KEY)"
  gemma:
    api_key: "$($env:GOOGLE_API_KEY)"
  llama:
    api_key: "$($env:GROQ_API_KEY)"
  deepseek:
    api_key: "$($env:DEEPSEEK_API_KEY)"
  cerebras:
    api_key: "$($env:CEREBRAS_API_KEY)"
  mistral:
    api_key: "$($env:MISTRAL_API_KEY)"
  openrouter:
    api_key: "$($env:OPENROUTER_API_KEY)"
  sambanova:
    api_key: "$($env:SAMBANOVA_API_KEY)"
  github:
    api_key: "$($env:GITHUB_TOKEN)"
  zhipu:
    api_key: "$($env:ZHIPU_API_KEY)"

chains:
  fast-refine:
    models:
      - llama-3.3-70b-versatile
      - mistral-small-latest
    prompts:
      - ""
      - "Improve the previous answer: fix any inaccuracies and make it more concise."
  smart-qa:
    models:
      - llama-3.3-70b-versatile
      - gpt-oss-120b
    prompts:
      - ""
      - "Review the previous answer. Address any gaps, uncertainties, or errors. Provide a final, improved response."
  debate:
    models:
      - llama-3.3-70b-versatile
      - mistral-small-latest
      - gpt-oss-120b
    prompts:
      - ""
      - "Consider the previous answer critically. Offer a different perspective or correct any mistakes."
      - "Synthesize the two perspectives above into the best possible answer."
"@
  $yaml | Set-Content -Path (Join-Path $base "demo-ixr.yaml") -Encoding UTF8
}

function Start-IxrServer {
  $base = $Script:WorktreeBaseOverride
  $configPath = Join-Path $base "demo-ixr.yaml"
  Write-Host ""; Write-Host "  " -NoNewline; Write-Bold "Starting ixr container on port ${Port}..."; Write-Host ""

  docker run -d `
    --name $ContainerName `
    --platform linux/amd64 `
    -p "${Port}:${Port}" `
    -v "${configPath}:/demo-ixr.yaml:ro" `
    $ImageTag `
    -config /demo-ixr.yaml -port "$Port" | Out-Null

  $attempts = 0; $ready = $false
  while ($attempts -lt 20 -and -not $ready) {
    Start-Sleep -Milliseconds 500; $attempts++
    $running = docker inspect $ContainerName --format '{{.State.Running}}' 2>$null
    if ($running -ne 'true') {
      Write-Host "  " -NoNewline; Write-Red "Container failed to start. Logs:"; Write-Host ""
      docker logs $ContainerName 2>&1 | ForEach-Object { Write-Host "    $_" }; exit 1
    }
    try {
      Invoke-RestMethod -Uri "http://localhost:$Port/v1/chat/completions" `
        -Method Post -ContentType "application/json" `
        -Body '{"model":"_probe_","messages":[]}' -ErrorAction Stop | Out-Null
      $ready = $true
    } catch {}
  }
  Write-Host "  " -NoNewline; Write-Green "ixr running in Docker ($ContainerName) -> http://localhost:${Port}"; Write-Host ""
  $Script:DockerLogProcess = Start-Process "docker" -ArgumentList @("logs", "-f", $ContainerName) `
    -RedirectStandardOutput $LogFile -NoNewWindow -PassThru
}

function Get-Python {
  foreach ($cmd in @("python3","python","py")) {
    try { $ver = & $cmd --version 2>&1; if ($ver -match 'Python 3') { return $cmd } } catch {}
  }
  Write-Host "  " -NoNewline; Write-Red "Error: Python 3 not found."; Write-Host ""; exit 1
}

try {
  Write-Host ""
  Write-Host "  ╔═════════════════════════════════════════════════╗"
  Write-Host "  ║   ixr  —  interactive demo (Windows · Docker)   ║"
  Write-Host "  ╚═════════════════════════════════════════════════╝"

  Check-Platform; Check-Docker; Print-BranchInfo

  if ($BranchArg) { $Script:Branch = $BranchArg; Write-Host "  Using branch: " -NoNewline; Write-Cyan $Script:Branch; Write-Host "" }
  else { Select-Branch }

  Setup-Worktree; Build-Ixr; Write-DemoConfig; Start-IxrServer

  Write-Host ""; Write-Host "  ─────────────────────────────────────────────"
  Write-Host "    Running demo scenarios..."; Write-Host "  ─────────────────────────────────────────────"; Write-Host ""

  $python = Get-Python
  & $python (Join-Path $DemoDir "run_demo.py") --port $Port --branch $Script:Branch --log $LogFile

  Write-Host ""; Write-Host "  " -NoNewline; Write-Green "Demo complete."; Write-Host ""
  Write-Host "  Container logs: docker logs $ContainerName"; Write-Host ""
} finally {
  Invoke-Cleanup
}
