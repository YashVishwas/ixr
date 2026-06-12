#Requires -Version 5.1
# demo_windows_embed.ps1 — Go-embed ixr demo for Windows.
# ixr runs as a goroutine inside a host Go application — same process, same binary.
#
# Usage:  .\demo\demo_windows_embed.ps1 [branch-name]
#
# If execution policy blocks the script:
#   Set-ExecutionPolicy -ExecutionPolicy RemoteSigned -Scope CurrentUser
#
# Requires: git, go >=1.21, python3 (or python)
# API keys: set at least one of GROQ_API_KEY, CEREBRAS_API_KEY, MISTRAL_API_KEY,
#           SAMBANOVA_API_KEY, OPENAI_API_KEY, ANTHROPIC_API_KEY, GOOGLE_API_KEY

param([string]$BranchArg = "")

Set-StrictMode -Version Latest
$ErrorActionPreference = "Stop"

$RepoRoot     = (git -C (Split-Path $MyInvocation.MyCommand.Path) rev-parse --show-toplevel) -replace '/', '\'
$DemoDir      = Join-Path $RepoRoot "demo"
$Port         = 8092
$WorktreeBase = Join-Path $env:TEMP "ixr-demo-windows-embed"
$Script:Branch = ""
$Script:WorktreeBaseOverride = $WorktreeBase
$Script:EmbedAppDir = ""
$Script:ServerProcess = $null

function Write-Bold  { param($Text) Write-Host $Text -NoNewline }
function Write-Cyan  { param($Text) Write-Host $Text -ForegroundColor Cyan -NoNewline }
function Write-Green { param($Text) Write-Host $Text -ForegroundColor Green -NoNewline }
function Write-Yellow{ param($Text) Write-Host $Text -ForegroundColor Yellow -NoNewline }
function Write-Red   { param($Text) Write-Host $Text -ForegroundColor Red -NoNewline }

function Check-Platform {
  $os = [System.Environment]::OSVersion.Platform
  if ($os -ne [System.PlatformID]::Win32NT) {
    Write-Host ""; Write-Yellow "  Warning: not running on Windows. Use a bash embed script instead."; Write-Host ""
    $ans = Read-Host "  Continue anyway? [y/N]"
    if ($ans -notmatch '^[Yy]$') { exit 0 }
  }
}

function Invoke-Cleanup {
  if ($Script:ServerProcess -and -not $Script:ServerProcess.HasExited) {
    Write-Host ""; Write-Host "  Stopping embed server (PID $($Script:ServerProcess.Id))..."
    try { $Script:ServerProcess.Kill() } catch {}
  }
  $base = $Script:WorktreeBaseOverride
  if ($Script:EmbedAppDir -and (Test-Path $Script:EmbedAppDir)) {
    Remove-Item -Recurse -Force $Script:EmbedAppDir -ErrorAction SilentlyContinue
  }
  $embedBin = Join-Path $base "embed-bin.exe"
  if (Test-Path $embedBin) { Remove-Item -Force $embedBin -ErrorAction SilentlyContinue }
  if ((Test-Path $base) -and ($base -ne $RepoRoot)) {
    Write-Host "  Removing worktree $base..."
    git -C $RepoRoot worktree remove --force $base 2>$null
    if (Test-Path $base) { Remove-Item -Recurse -Force $base -ErrorAction SilentlyContinue }
  }
}

function Print-BranchInfo {
  Write-Host ""; Write-Host "  " -NoNewline; Write-Bold "Available branches and their features:"; Write-Host ""; Write-Host ""
  Write-Host "  " -NoNewline; Write-Cyan "main"; Write-Host "         — OpenAI-compatible proxy, 12 provider adapters, auto-routing catalog"
  Write-Host "  " -NoNewline; Write-Cyan "phase-2"; Write-Host "      — +circuit breaker, intent parser, scoring engine, rate limiting"
  Write-Host "  " -NoNewline; Write-Cyan "phase-2_2"; Write-Host "    — +shadow testing, streaming, telemetry, config hot-reload"
  Write-Host "  " -NoNewline; Write-Cyan "phase-2_3"; Write-Host "    — +OTEL, Prometheus, semantic cache, Bedrock/Ollama, embeddings"
  Write-Host ""; Write-Host "  " -NoNewline; Write-Yellow "Note:"; Write-Host " each branch is a superset of the previous."; Write-Host ""
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
    $Script:WorktreeBaseOverride = $RepoRoot
    $Script:EmbedAppDir = Join-Path $RepoRoot "cmd\demo-embed"
    return
  }
  $Script:WorktreeBaseOverride = $WorktreeBase
  $Script:EmbedAppDir = Join-Path $WorktreeBase "cmd\demo-embed"
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

function Write-EmbedApp {
  $appDir = $Script:EmbedAppDir
  New-Item -ItemType Directory -Force -Path $appDir | Out-Null
  $mainGo = @'
// demo-embed: shows ixr running inside a host Go service — same binary, same process.
package main

import (
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"strconv"
	"time"

	ixr "github.com/YashVishwas/ixr/pkg/ixr"
)

func main() {
	port, _ := strconv.Atoi(os.Getenv("IXR_PORT"))
	if port == 0 {
		port = 8092
	}
	configFile := os.Getenv("IXR_CONFIG_FILE")

	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "host app healthy — ixr embedded on :%d\n", port)
	})
	go func() {
		ln, err := net.Listen("tcp", ":0")
		if err != nil {
			log.Printf("host app (optional): %v", err)
			return
		}
		appPort := ln.Addr().(*net.TCPAddr).Port
		log.Printf("host app on :%d — ixr embedded on :%d\n", appPort, port)
		http.Serve(ln, mux)
	}()

	time.Sleep(100 * time.Millisecond)

	if err := ixr.Start(
		ixr.WithPort(port),
		ixr.WithConfigFile(configFile),
	); err != nil {
		log.Fatalf("ixr: %v", err)
	}
}
'@
  $mainGo | Set-Content -Path (Join-Path $appDir "main.go") -Encoding UTF8
}

function Build-Ixr {
  $base = $Script:WorktreeBaseOverride
  Write-Host ""; Write-Host "  " -NoNewline; Write-Bold "Building embed demo (windows/amd64)..."; Write-Host ""
  Write-EmbedApp
  $env:GOOS = "windows"; $env:GOARCH = "amd64"
  Push-Location $base
  try {
    go build -o (Join-Path $base "embed-bin.exe") .\cmd\demo-embed\ 2>&1 | ForEach-Object { Write-Host "    $_" }
  } finally {
    Pop-Location
    Remove-Item Env:\GOOS -ErrorAction SilentlyContinue
    Remove-Item Env:\GOARCH -ErrorAction SilentlyContinue
  }
  Write-Host "  " -NoNewline; Write-Green "Build complete."; Write-Host ""
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
    api_key: "`$(`$env:OPENAI_API_KEY)"
  anthropic:
    api_key: "`$(`$env:ANTHROPIC_API_KEY)"
  gemini:
    api_key: "`$(`$env:GOOGLE_API_KEY)"
  gemma:
    api_key: "`$(`$env:GOOGLE_API_KEY)"
  llama:
    api_key: "`$(`$env:GROQ_API_KEY)"
  deepseek:
    api_key: "`$(`$env:DEEPSEEK_API_KEY)"
  cerebras:
    api_key: "`$(`$env:CEREBRAS_API_KEY)"
  mistral:
    api_key: "`$(`$env:MISTRAL_API_KEY)"
  openrouter:
    api_key: "`$(`$env:OPENROUTER_API_KEY)"
  sambanova:
    api_key: "`$(`$env:SAMBANOVA_API_KEY)"
  github:
    api_key: "`$(`$env:GITHUB_TOKEN)"
  zhipu:
    api_key: "`$(`$env:ZHIPU_API_KEY)"
chains:
  fast-refine:
    models: [llama-3.3-70b-versatile, mistral-small-latest]
    prompts: ["", "Improve the previous answer: fix any inaccuracies and make it more concise."]
  smart-qa:
    models: [llama-3.3-70b-versatile, gpt-oss-120b]
    prompts: ["", "Review the previous answer. Address any gaps, uncertainties, or errors."]
  debate:
    models: [llama-3.3-70b-versatile, mistral-small-latest, gpt-oss-120b]
    prompts: ["", "Consider the previous answer critically. Offer a different perspective.", "Synthesize the two perspectives above into the best possible answer."]
"@
  $yaml | Set-Content -Path (Join-Path $base "demo-ixr.yaml") -Encoding UTF8
}

function Start-IxrServer {
  $base = $Script:WorktreeBaseOverride
  $binary = Join-Path $base "embed-bin.exe"
  $config = Join-Path $base "demo-ixr.yaml"
  $logFile    = Join-Path $base "ixr.log"
  $logErrFile = Join-Path $base "ixr-err.log"

  Write-Host ""; Write-Host "  " -NoNewline; Write-Bold "Starting embed server on port ${Port}..."; Write-Host ""

  $env:IXR_PORT = "$Port"
  $env:IXR_CONFIG_FILE = $config
  $Script:ServerProcess = Start-Process `
    -FilePath $binary `
    -RedirectStandardOutput $logFile `
    -RedirectStandardError  $logErrFile `
    -PassThru -NoNewWindow
  Remove-Item Env:\IXR_PORT -ErrorAction SilentlyContinue
  Remove-Item Env:\IXR_CONFIG_FILE -ErrorAction SilentlyContinue

  $attempts = 0; $ready = $false
  while ($attempts -lt 20 -and -not $ready) {
    Start-Sleep -Milliseconds 500; $attempts++
    if ($Script:ServerProcess.HasExited) {
      Write-Host "  " -NoNewline; Write-Red "Server failed to start. Logs:"; Write-Host ""
      if (Test-Path $logFile)    { Get-Content $logFile    | ForEach-Object { Write-Host "    $_" } }
      if (Test-Path $logErrFile) { Get-Content $logErrFile | ForEach-Object { Write-Host "    $_" } }
      exit 1
    }
    try {
      Invoke-RestMethod -Uri "http://localhost:$Port/v1/chat/completions" `
        -Method Post -ContentType "application/json" `
        -Body '{"model":"_probe_","messages":[]}' -ErrorAction Stop | Out-Null
      $ready = $true
    } catch {}
  }
  Write-Host "  " -NoNewline; Write-Green "ixr embedded (PID $($Script:ServerProcess.Id)) -> http://localhost:${Port}"; Write-Host ""
}

function Get-Python {
  foreach ($cmd in @("python3","python","py")) {
    try { $ver = & $cmd --version 2>&1; if ($ver -match 'Python 3') { return $cmd } } catch {}
  }
  Write-Host "  " -NoNewline; Write-Red "Error: Python 3 not found."; Write-Host ""; exit 1
}

try {
  Write-Host ""
  Write-Host "  ╔══════════════════════════════════════════════════╗"
  Write-Host "  ║   ixr  —  interactive demo (Windows · Embed)     ║"
  Write-Host "  ╚══════════════════════════════════════════════════╝"
  Write-Host "  " -NoNewline; Write-Cyan "Embed mode:"; Write-Host " ixr runs as a goroutine inside the host app binary."; Write-Host ""

  Check-Platform; Print-BranchInfo

  if ($BranchArg) { $Script:Branch = $BranchArg; Write-Host "  Using branch: " -NoNewline; Write-Cyan $Script:Branch; Write-Host "" }
  else { Select-Branch }

  Setup-Worktree; Build-Ixr; Write-DemoConfig; Start-IxrServer

  Write-Host ""; Write-Host "  ─────────────────────────────────────────────"
  Write-Host "    Running demo scenarios..."; Write-Host "  ─────────────────────────────────────────────"; Write-Host ""

  $python = Get-Python
  $base = $Script:WorktreeBaseOverride
  & $python (Join-Path $DemoDir "run_demo.py") --port $Port --branch $Script:Branch --log (Join-Path $base "ixr.log")

  Write-Host ""; Write-Host "  " -NoNewline; Write-Green "Demo complete."; Write-Host ""
  Write-Host "  Server log: $(Join-Path $base 'ixr.log')"; Write-Host ""
} finally {
  Invoke-Cleanup
}
