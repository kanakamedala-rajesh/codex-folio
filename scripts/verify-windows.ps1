[CmdletBinding()]
param(
    [string]$ExpectedRevision,
    [string]$ReportPath
)

$ErrorActionPreference = "Stop"
$repositoryRoot = (Resolve-Path (Join-Path $PSScriptRoot "..")).Path
Set-Location $repositoryRoot

if (-not $IsWindows -and $PSVersionTable.PSEdition -eq "Core") {
    throw "Run this script in native Windows PowerShell, not from WSL."
}
if ([System.Runtime.InteropServices.RuntimeInformation]::OSArchitecture -ne "X64") {
    throw "Windows AMD64 is required; found $([System.Runtime.InteropServices.RuntimeInformation]::OSArchitecture)."
}

$revision = (& git rev-parse --verify HEAD).Trim()
if ($LASTEXITCODE -ne 0) {
    throw "Run this script from a Git checkout."
}
if ($ExpectedRevision -and $revision -ne $ExpectedRevision) {
    throw "Expected revision $ExpectedRevision; found $revision."
}
if (& git status --porcelain) {
    throw "The Git working tree must be clean."
}

if (-not $ReportPath) {
    $ReportPath = Join-Path $repositoryRoot "build/evidence/milestone-4-windows-$revision.txt"
}
$reportDirectory = Split-Path -Parent $ReportPath
New-Item -ItemType Directory -Force $reportDirectory | Out-Null

function Write-Evidence([string]$Line) {
    $Line | Tee-Object -FilePath $ReportPath -Append
}

function Invoke-EvidenceCommand([string]$Name, [string]$Command, [string[]]$Arguments) {
    Write-Evidence ""
    Write-Evidence "[$Name] $Command $($Arguments -join ' ')"
    $output = & $Command @Arguments 2>&1
    $exitCode = $LASTEXITCODE
    $output | Tee-Object -FilePath $ReportPath -Append
    if ($exitCode -ne 0) {
        throw "$Name failed with exit code $exitCode. Evidence: $ReportPath"
    }
    Write-Evidence "[$Name] PASS"
}

Set-Content -Path $ReportPath -Value @(
    "CodexFolio Milestone 4 Windows AMD64 validation"
    "Timestamp: $([DateTimeOffset]::UtcNow.ToString('O'))"
    "Revision: $revision"
    "PowerShell: $($PSVersionTable.PSVersion)"
    "OS: $([System.Runtime.InteropServices.RuntimeInformation]::OSDescription)"
    "Architecture: $([System.Runtime.InteropServices.RuntimeInformation]::OSArchitecture)"
)

Invoke-EvidenceCommand "Focused Safe Continuation" "go" @(
    "test", "-count=1", "./cmd/codex-folio", "./internal/adapters/codex",
    "./internal/adapters/git", "./internal/continuation", "./internal/httpapi", "./internal/store"
)
Invoke-EvidenceCommand "Canonical repository verification" "node" @("scripts/verify.mjs")

Write-Evidence ""
Write-Evidence "RESULT: PASS"
Write-Host "Windows AMD64 validation passed. Evidence: $ReportPath"
