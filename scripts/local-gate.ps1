param(
    [switch]$DryRun,
    [switch]$PreflightOnly,
    [string]$ReportDir
)

$ErrorActionPreference = "Stop"
$repoRoot = Split-Path -Parent $PSScriptRoot
Set-Location -LiteralPath (Join-Path $repoRoot "neodesk-server")

$arguments = @("run", "./cmd/repotool", "local-gate")
if ($DryRun) { $arguments += "--dry-run" }
if ($PreflightOnly) { $arguments += "--preflight-only" }
if ($ReportDir) {
    $arguments += "--report-dir"
    $arguments += $ReportDir
}

& go @arguments
exit $LASTEXITCODE
