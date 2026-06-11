#Requires -Version 5.1
# demo_windows.ps1 — Interactive ixr demo for Windows (x86_64).
#
# Usage (from repo root):  .\demo\demo_windows.ps1 [branch-name]
#
# If execution policy blocks the script, run once as admin:
#   Set-ExecutionPolicy -ExecutionPolicy RemoteSigned -Scope CurrentUser
#
# Requires: git, go >=1.21, python3 (or python)
# API keys: set at least one of GROQ_API_KEY, CEREBRAS_API_KEY, MISTRAL_API_KEY,
#           SAMBANOVA_API_KEY, OPENAI_API_KEY, ANTHROPIC_API_KEY, GOOGLE_API_KEY

param(
    [string]$BranchArg = ""
)

Set-StrictMode -Version Latest
$ErrorActionPreference = "Stop"

$RepoRoot   = (git -C (Split-Path $MyInvocation.MyCommand.Path) rev-parse --show-toplevel) -replace '/', '\'
$DemoDir    = Join-Path $RepoRoot "demo"
$Port       = 8084
$WorktreeBase = Join-Path $env:TEMP "ixr-demo-windows"
$Script:ServerProcess = $null
$Script:Branch = ""

# ── colour helpers ────────────────────────────────────────────────────────────
function Write-Bold  { param($Text) Write-Host $Text -NoNewline }
function Write-Cyan  { param($Text) Write-Host $Text -ForegroundColor Cyan -NoNewline }
function Write-Green { param($Text) Write-Host $Text -ForegroundColor Green -NoNewline }
function Write-Yellow{ param($Text) Write-Host $Text -ForegroundColor Yellow -NoNewline }
function Write-Red   { param($Text) Write-Host $Text -ForegroundColor Red -NoNewline }

# ── platform check ────────────────────────────────────────────────────────────
function Check-Platform {
    $os = [System.Environment]::OSVersion.Platform
    if ($os -ne [System.PlatformID]::Win32NT) {
        Write-Host ""
        Write-Yellow "  Warning: detected non-Windows platform. Use demo_silicon.sh or demo_linux.sh instead."
        Write-Host ""
        $ans = Read-Host "  Continue anyway? [y/N]"
        if ($ans -notmatch '^[Yy]$') { exit 0 }
    }
}

# ── cleanup ───────────────────────────────────────────────────────────────────
function Invoke-Cleanup {
    if ($Script:ServerProcess -and -not $Script:ServerProcess.HasExited) {
        Write-Host ""
        Write-Host "  Stopping ixr server (PID $($Script:ServerProcess.Id))..."
        try { $Script:ServerProcess.Kill() } catch {}
    }
    if ((Test-Path $WorktreeBase) -and ($WorktreeBase -ne $RepoRoot)) {
        Write-Host "  Removing worktree $WorktreeBase..."
        git -C $RepoRoot worktree remove --force $WorktreeBase 2>$null
        if (Test-Path $WorktreeBase) { Remove-Item -Recurse -Force $WorktreeBase -ErrorAction SilentlyContinue }
    }
}

# ── branch selection ──────────────────────────────────────────────────────────
function Print-BranchInfo {
    Write-Host ""
    Write-Host "  " -NoNewline; Write-Bold "Available branches and their features:"
    Write-Host ""
    Write-Host "  " -NoNewline; Write-Cyan "main"
    Write-Host "         — OpenAI-compatible proxy, 12 provider adapters, auto-routing catalog"
    Write-Host "  " -NoNewline; Write-Cyan "phase-2"
    Write-Host "      — +circuit breaker, intent parser, scoring engine, rate limiting, routing filter"
    Write-Host "  " -NoNewline; Write-Cyan "phase-2_2"
    Write-Host "    — +shadow testing, streaming (SSE), telemetry plugin, config hot-reload,"
    Write-Host "                     secrets, tenant management, executor with retry/fallback"
    Write-Host "  " -NoNewline; Write-Cyan "phase-2_3"
    Write-Host "    — +observability (OTEL traces, Prometheus metrics, request ID), semantic cache,"
    Write-Host "                     Bedrock/Ollama/llama.cpp/local providers, embeddings + images endpoints,"
    Write-Host "                     full tool-calling spec, bus adapters, schema registry"
    Write-Host ""
    Write-Host "  " -NoNewline; Write-Yellow "Note:"; Write-Host " each branch is a superset of the previous."
    Write-Host ""
}

function Select-Branch {
    $branches = @()

    # Local branches
    $localLines = git -C $RepoRoot branch 2>$null
    foreach ($line in $localLines) {
        $b = $line.Trim()
        if ($b.StartsWith('* ')) { $b = $b.Substring(2) }
        if ($b) { $branches += $b }
    }

    # Remote branches not already local
    $remoteLines = git -C $RepoRoot branch -r 2>$null
    foreach ($line in $remoteLines) {
        $ref = $line.Trim() -replace '^remotes/origin/', ''
        if ($ref.StartsWith('HEAD')) { continue }
        if ($branches -notcontains $ref) { $branches += $ref }
    }

    Write-Host "  " -NoNewline; Write-Bold "Select a branch to demo:"
    Write-Host ""
    for ($i = 0; $i -lt $branches.Count; $i++) {
        Write-Host ("    {0,2})  {1}" -f ($i + 1), $branches[$i])
    }
    Write-Host ""
    $choice = Read-Host "  Enter number [default: main, q to quit]"

    if ($choice -eq 'q' -or $choice -eq 'Q' -or $choice -eq 'quit') {
        Write-Host ""
        Write-Green "  Bye."
        Write-Host ""
        exit 0
    } elseif ([string]::IsNullOrWhiteSpace($choice)) {
        $Script:Branch = "main"
    } elseif ($choice -match '^\d+$' -and [int]$choice -ge 1 -and [int]$choice -le $branches.Count) {
        $Script:Branch = $branches[[int]$choice - 1]
    } else {
        $Script:Branch = "main"
    }
}

# ── worktree setup ────────────────────────────────────────────────────────────
function Setup-Worktree {
    Write-Host ""
    Write-Host "  " -NoNewline; Write-Bold "Setting up worktree for branch:"; Write-Host " " -NoNewline; Write-Cyan $Script:Branch; Write-Host ""

    $currentBranch = (git -C $RepoRoot branch --show-current 2>$null) -join ""
    if ($currentBranch -eq $Script:Branch) {
        Write-Host "  Branch is the current worktree — running from $RepoRoot"
        $Script:WorktreeBaseOverride = $RepoRoot
        return
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

# ── build ─────────────────────────────────────────────────────────────────────
function Build-Ixr {
    $base = $Script:WorktreeBaseOverride
    Write-Host ""
    Write-Host "  " -NoNewline; Write-Bold "Building ixr (windows/amd64)..."; Write-Host ""
    $env:GOOS = "windows"; $env:GOARCH = "amd64"
    Push-Location $base
    try {
        go build -o (Join-Path $base "ixr-bin.exe") ./cmd/ixr/ 2>&1 | ForEach-Object { Write-Host "    $_" }
    } finally {
        Pop-Location
        Remove-Item Env:\GOOS -ErrorAction SilentlyContinue
        Remove-Item Env:\GOARCH -ErrorAction SilentlyContinue
    }
    Write-Host "  " -NoNewline; Write-Green "Build complete."; Write-Host ""
}

# ── config ────────────────────────────────────────────────────────────────────
function Write-DemoConfig {
    $base = $Script:WorktreeBaseOverride
    # Single-quote here-string: no variable expansion, so ${VAR} stays literal.
    $yaml = @'
server:
  port: PORT_PLACEHOLDER

log_level: warn

auth:
  disable_auth: true

providers:
  openai:
    api_key: ${OPENAI_API_KEY}
  anthropic:
    api_key: ${ANTHROPIC_API_KEY}
  gemini:
    api_key: ${GOOGLE_API_KEY}
  gemma:
    api_key: ${GOOGLE_API_KEY}
  llama:
    api_key: ${GROQ_API_KEY}
  deepseek:
    api_key: ${DEEPSEEK_API_KEY}
  cerebras:
    api_key: ${CEREBRAS_API_KEY}
  mistral:
    api_key: ${MISTRAL_API_KEY}
  openrouter:
    api_key: ${OPENROUTER_API_KEY}
  sambanova:
    api_key: ${SAMBANOVA_API_KEY}
  github:
    api_key: ${GITHUB_TOKEN}
  zhipu:
    api_key: ${ZHIPU_API_KEY}
  ollama:
    base_url: ${OLLAMA_BASE_URL}
  llamacpp:
    base_url: ${LLAMACPP_BASE_URL}

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
'@
    $yaml = $yaml -replace 'PORT_PLACEHOLDER', $Port
    $yaml | Set-Content -Path (Join-Path $base "demo-ixr.yaml") -Encoding UTF8
}

# ── server start ──────────────────────────────────────────────────────────────
function Start-IxrServer {
    $base = $Script:WorktreeBaseOverride
    Write-Host ""
    Write-Host "  " -NoNewline; Write-Bold "Starting ixr on port ${Port}..."; Write-Host ""

    $logFile    = Join-Path $base "ixr.log"
    $logErrFile = Join-Path $base "ixr-err.log"
    $binary     = Join-Path $base "ixr-bin.exe"
    $config     = Join-Path $base "demo-ixr.yaml"

    $Script:ServerProcess = Start-Process `
        -FilePath $binary `
        -ArgumentList @("-config", $config, "-port", "$Port") `
        -RedirectStandardOutput $logFile `
        -RedirectStandardError  $logErrFile `
        -PassThru -NoNewWindow

    # Wait up to 10 s for the server to be ready
    $attempts = 0
    $ready = $false
    while ($attempts -lt 20 -and -not $ready) {
        Start-Sleep -Milliseconds 500
        $attempts++
        if ($Script:ServerProcess.HasExited) {
            Write-Host "  " -NoNewline; Write-Red "Server failed to start. Logs:"; Write-Host ""
            if (Test-Path $logFile)    { Get-Content $logFile    | ForEach-Object { Write-Host "    $_" } }
            if (Test-Path $logErrFile) { Get-Content $logErrFile | ForEach-Object { Write-Host "    $_" } }
            exit 1
        }
        try {
            $body = '{"model":"_probe_","messages":[]}'
            Invoke-RestMethod -Uri "http://localhost:$Port/v1/chat/completions" `
                -Method Post -ContentType "application/json" -Body $body -ErrorAction Stop | Out-Null
            $ready = $true
        } catch {}
    }

    if (-not $Script:ServerProcess.HasExited) {
        Write-Host "  " -NoNewline
        Write-Green "ixr running (PID $($Script:ServerProcess.Id)) -> http://localhost:${Port}"
        Write-Host ""
    }
}

# ── find python ───────────────────────────────────────────────────────────────
function Get-Python {
    foreach ($cmd in @("python3", "python", "py")) {
        try {
            $ver = & $cmd --version 2>&1
            if ($ver -match 'Python 3') { return $cmd }
        } catch {}
    }
    Write-Host "  " -NoNewline; Write-Red "Error: Python 3 not found. Install from https://python.org"; Write-Host ""
    exit 1
}

# ── main ──────────────────────────────────────────────────────────────────────
try {
    Write-Host ""
    Write-Host "  ╔══════════════════════════════════════════╗"
    Write-Host "  ║    ixr  —  interactive demo (Windows)    ║"
    Write-Host "  ╚══════════════════════════════════════════╝"

    Check-Platform
    Print-BranchInfo

    if ($BranchArg) {
        $Script:Branch = $BranchArg
        Write-Host "  Using branch: " -NoNewline; Write-Cyan $Script:Branch; Write-Host ""
    } else {
        Select-Branch
    }

    Setup-Worktree
    Build-Ixr
    Write-DemoConfig
    Start-IxrServer

    Write-Host ""
    Write-Host "  ─────────────────────────────────────────────"
    Write-Host "    Running demo scenarios..."
    Write-Host "  ─────────────────────────────────────────────"
    Write-Host ""

    $python = Get-Python
    $base = $Script:WorktreeBaseOverride
    & $python (Join-Path $DemoDir "run_demo.py") --port $Port --branch $Script:Branch --log (Join-Path $base "ixr.log")

    Write-Host ""
    Write-Host "  " -NoNewline; Write-Green "Demo complete."; Write-Host ""
    Write-Host "  Server log: $(Join-Path $base 'ixr.log')"
    Write-Host ""

} finally {
    Invoke-Cleanup
}
