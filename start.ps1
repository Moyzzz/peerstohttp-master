#Requires -Version 5.1
<#
    peerstohttp one-click launcher (Windows)

    Examples:
        .\start.ps1
        .\start.ps1 -Port 9000 -TorrentPort 51413 -Dir D:/bt-data
        .\start.ps1 -Background
        .\start.ps1 -Stop

    MinGW / MSYS2 toolchain is resolved in this order (no hardcoded default):
        1. -MingwBin "<path>"
        2. $env:PEERSTOHTTP_MINGW_BIN
        3. gcc.exe already resolvable on PATH (its directory is used)
        4. $env:CC pointing at a compiler (its directory is used)
        5. $env:MSYS2_ROOT / %MSYS2_HOME%
           (sub-dir mingw64 | ucrt64 | clang64 | mingw32 auto-detected)

    Notes:
        - Does NOT modify system environment variables.
        - PATH is prepended only inside this process, so the exe resolves its
          runtime DLLs from the toolchain dir without copying them into the repo.
        - Only when nothing matches, a warning is printed; no fallback path is guessed.
#>
[CmdletBinding()]
param(
    [int]$Port = 8484,
    [int]$TorrentPort = 6881,
    [string]$Dir = $env:PEERSTOHTTP_DIR,
    [switch]$Background,
    [switch]$Stop,
    [string]$Exe = $env:PEERSTOHTTP_EXE,
    [string]$MingwBin = $env:PEERSTOHTTP_MINGW_BIN
)

$ErrorActionPreference = "Stop"

$root = Split-Path -Parent $MyInvocation.MyCommand.Path
Set-Location $root

if (-not $Dir) { $Dir = "C:/bt-data" }
if (-not $Exe) { $Exe = "peerstohttp.exe" }
$procName = [System.IO.Path]::GetFileNameWithoutExtension($Exe)

function Write-Info($msg) { Write-Host "[INFO]  $msg" }
function Write-Ok($msg)   { Write-Host "[OK]    $msg" -ForegroundColor Green }
function Write-Warn($msg){ Write-Host "[WARN]  $msg" -ForegroundColor Yellow }
function Write-Err($msg)  { Write-Host "[ERROR] $msg" -ForegroundColor Red }

# Normalize a PATH-like entry: strip quotes/whitespace, drop trailing separator.
function Format-DirEntry($value) {
    if (-not $value) { return $null }
    $v = $value.Trim().Trim('"')
    if ($v.Length -gt 3) { $v = $v.TrimEnd('\') }
    if ($v) { return $v }
    return $null
}

function Resolve-MingwBin {
    param([string]$Explicit)

    $candidates = New-Object System.Collections.Generic.List[string]

    # 1) -MingwBin / $env:PEERSTOHTTP_MINGW_BIN
    $candidates.Add($Explicit)

    # 2) gcc already on PATH
    $gcc = Get-Command gcc.exe -CommandType Application -ErrorAction SilentlyContinue |
           Select-Object -First 1
    if ($gcc) { $candidates.Add((Split-Path -Parent $gcc.Source)) }

    # 3) $env:CC points at a compiler
    if ($env:CC) {
        try {
            $ccPath = (Resolve-Path -LiteralPath $env:CC -ErrorAction Stop).Path
            $candidates.Add((Split-Path -Parent $ccPath))
        } catch { }
    }

    # 4) MSYS2 root declared in environment
    foreach ($name in 'MSYS2_ROOT', 'MSYS2_HOME', 'MSYS2_PATH') {
        $msysRoot = [Environment]::GetEnvironmentVariable($name)
        if (-not $msysRoot) { continue }
        foreach ($sub in 'mingw64', 'ucrt64', 'clang64', 'mingw32') {
            $candidates.Add((Join-Path (Join-Path $msysRoot $sub) 'bin'))
        }
    }

    foreach ($raw in $candidates) {
        $c = Format-DirEntry $raw
        if (-not $c) { continue }
        if (Test-Path -LiteralPath (Join-Path $c 'gcc.exe')) { return $c }
    }
    return $null
}

# ---------------------------------------------------------------- stop
if ($Stop) {
    $procs = Get-Process $procName -ErrorAction SilentlyContinue
    if ($procs) {
        $procs | Stop-Process -Force
        Write-Ok "stopped $($procs.Count) process(es)"
    } else {
        Write-Info "no running $procName process"
    }
    exit 0
}

# ---------------------------------------------------------------- toolchain
$mingwBin = Resolve-MingwBin -Explicit $MingwBin

if ($mingwBin) {
    Write-Info "MinGW bin : $mingwBin"
    # Prepend to this process PATH: Windows resolves dependent DLLs from PATH.
    $env:PATH = $mingwBin + ";" + $env:PATH
} else {
    Write-Warn "MinGW toolchain not located, runtime DLL check skipped"
    Write-Host  '  Set one of these and retry:'
    Write-Host  '    $env:PEERSTOHTTP_MINGW_BIN = "D:\msys64\mingw64\bin"'
    Write-Host  '    $env:MSYS2_ROOT            = "D:\msys64"'
    Write-Host  '  ...or put the directory containing gcc.exe on PATH'
}

# ---------------------------------------------------------------- check exe
$exePath = if ([System.IO.Path]::IsPathRooted($Exe)) { $Exe } else { Join-Path $root $Exe }

if (-not (Test-Path $exePath)) {
    Write-Err "not found: $exePath"
    Write-Host ""
    Write-Host "Build it first (see DEPLOY.md 3.2):"

    $goBin = try {
        Split-Path -Parent (Get-Command go.exe -CommandType Application -ErrorAction Stop).Source
    } catch { "" }

    $pathHint = (@($goBin, $mingwBin) | Where-Object { $_ }) -join ';'
    if (-not $pathHint) { $pathHint = "<Go>\bin;<MinGW>\bin" }

    Write-Host ('  $env:PATH = "{0};" + $env:PATH' -f $pathHint)
    Write-Host  '  $env:CGO_ENABLED = "1"; $env:CC = "gcc"'
    Write-Host ('  go build -mod=vendor -o "{0}" .\cmd' -f $Exe)
    exit 1
}

# ---------------------------------------------------------------- runtime dlls
# Check only; never copy DLLs into the repository.
$required = @("libstdc++-6.dll", "libgcc_s_seh-1.dll", "libwinpthread-1.dll")
$missing  = @()

$searchDirs = New-Object System.Collections.Generic.List[string]
$searchDirs.Add($root)
foreach ($raw in ($env:PATH -split ';')) {
    $entry = Format-DirEntry $raw
    if ($entry -and (Test-Path -LiteralPath $entry)) { $searchDirs.Add($entry) }
}

foreach ($dll in $required) {
    $found = $false
    foreach ($entry in $searchDirs) {
        if (Test-Path -LiteralPath (Join-Path $entry $dll)) { $found = $true; break }
    }
    if (-not $found) { $missing += $dll }
}

if ($missing.Count -gt 0) {
    Write-Warn ("missing runtime DLL(s): " + ($missing -join ", "))
    if ($mingwBin) {
        Write-Warn "searched $mingwBin and PATH (ucrt64/clang64 variants ship differently named runtime DLLs)"
    } else {
        Write-Warn "the program may exit silently; see DEPLOY.md 3.2 step 4"
    }
}

# ---------------------------------------------------------------- download dir
if (-not (Test-Path $Dir)) {
    New-Item -ItemType Directory -Force -Path $Dir | Out-Null
    Write-Info "created download dir: $Dir"
}

# ---------------------------------------------------------------- port check
$inUse = $null
try {
    $inUse = Get-NetTCPConnection -LocalPort $Port -State Listen -ErrorAction SilentlyContinue
} catch { }

if ($inUse) {
    Write-Err "port $Port is already in use (PID $($inUse.OwningProcess -join ','))"
    Write-Info "choose another one: .\start.ps1 -Port 8485"
    exit 1
}

# ---------------------------------------------------------------- run
$exeArgs = @("-port=$Port", "-port-torr=$TorrentPort", "-dir=$Dir")

Write-Host ""
Write-Host "  HTTP        : http://127.0.0.1:$Port"
Write-Host "  Magnet URL  : http://127.0.0.1:$Port/list/m3u/mp4/-/magnet/magnet:?xt=urn:btih:<HASH>"
Write-Host "  Hash URL    : http://127.0.0.1:$Port/list/json/mp4/-/hash/<HASH>"
Write-Host "  Download    : $Dir"
Write-Host "  BT port     : $TorrentPort (tcp+udp)"
Write-Host ""

if ($Background) {
    Start-Process -FilePath $exePath -ArgumentList $exeArgs -WorkingDirectory $root -WindowStyle Hidden
    Start-Sleep -Seconds 3

    $p = Get-Process $procName -ErrorAction SilentlyContinue
    if ($p) {
        Write-Ok "running in background, PID $($p.Id -join ',')"
        Write-Host ""
        Write-Host "  Stop : .\start.ps1 -Stop"
    } else {
        Write-Err "failed to start; run without -Background to see the output"
        exit 1
    }
} else {
    Write-Info "running in foreground, press Ctrl+C to stop"
    Write-Host ""
    & $exePath @exeArgs
    exit $LASTEXITCODE
}
