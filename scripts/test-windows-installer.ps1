# Checks of betterdesk.ps1's service set-up for the server with its web
# console, without Windows: the installer's functions are loaded from the
# script's syntax tree and the Windows-only commands (scheduled tasks,
# icacls, NSSM) are replaced by stand-ins.
#
#   pwsh -NoProfile -File scripts/test-windows-installer.ps1
$ErrorActionPreference = 'Stop'

$root = Split-Path -Parent $PSScriptRoot
$installer = Join-Path $root 'betterdesk.ps1'
$tokens = $null
$parseErrors = $null
$ast = [System.Management.Automation.Language.Parser]::ParseFile($installer, [ref]$tokens, [ref]$parseErrors)
if ($parseErrors.Count -gt 0) { throw "betterdesk.ps1 does not parse: $($parseErrors[0].Message)" }
foreach ($fn in $ast.FindAll({ $args[0] -is [System.Management.Automation.Language.FunctionDefinitionAst] }, $false)) {
    . ([scriptblock]::Create($fn.Extent.Text))
}

$script:failures = 0
function Fail([string]$Message) { Write-Host "FAIL: $Message"; $script:failures++ }

# Stand-ins for Windows and NSSM
$script:registeredTasks = @{}
$script:fakeNssm = $null
$script:nssmCalls = @()
function Get-NssmPath { return $script:fakeNssm }
function fake-nssm { $script:nssmCalls += , @($args | ForEach-Object { $_ } | ForEach-Object { "$_" }) }
function Set-ServiceLeastPrivilege { param($ServiceName, $NssmPath, $Paths) }
function icacls { }
function Unregister-ScheduledTask { param($TaskName, [switch]$Confirm, $ErrorAction) $script:registeredTasks.Remove($TaskName) }
function New-ScheduledTaskAction { param($Execute, $Argument, $WorkingDirectory) [pscustomobject]@{ Execute = $Execute; Arguments = $Argument } }
function New-ScheduledTaskTrigger { param([switch]$AtStartup) 'startup' }
function New-ScheduledTaskPrincipal { param($UserId, $LogonType, $RunLevel) $UserId }
function New-ScheduledTaskSettingsSet { param([switch]$AllowStartIfOnBatteries, [switch]$DontStopIfGoingOnBatteries, $RestartCount, $RestartInterval, $ExecutionTimeLimit) [pscustomobject]@{ ExecutionTimeLimit = $ExecutionTimeLimit } }
function Register-ScheduledTask { param($TaskName, $Action, $Trigger, $Principal, $Settings, $Description, $Xml) $script:registeredTasks[$TaskName] = $Settings }
function Get-ScheduledTask { param($TaskName, $ErrorAction) if ($script:registeredTasks.ContainsKey($TaskName)) { [pscustomobject]@{ TaskName = $TaskName } } }
function Get-Service { param($Name, $ErrorAction) $null }

function New-TestInstall {
    $dir = Join-Path ([System.IO.Path]::GetTempPath()) ("bd-ps1-" + [guid]::NewGuid().ToString('N'))
    New-Item -ItemType Directory -Path (Join-Path $dir 'server'), (Join-Path $dir 'console') -Force | Out-Null
    $script:RUSTDESK_PATH = Join-Path $dir 'server'
    $script:CONSOLE_PATH = Join-Path $dir 'console'
    $script:ScriptDir = $dir
    $script:SERVER_SERVICE = 'BetterDeskServer'
    $script:CONSOLE_SERVICE = 'BetterDeskConsole'
    $script:HBBS_SERVICE = 'BetterDeskSignal'
    $script:HBBR_SERVICE = 'BetterDeskRelay'
    $script:GO_API_PORT = '21114'
    $script:AUTO_MODE = $true
    $script:RELAY_SERVERS = '203.0.113.7'
    $script:RELAY_MODE = 'auto'
    $script:USE_POSTGRESQL = $false
    $script:FRESH_INSTALL = $false
    $script:RUN_AS_ROOT = $false
    $script:LOG_FILE = Join-Path $dir 'test.log'
    $env:CONNECTION_MODE_SET = '1'
    $script:registeredTasks = @{}
    return $dir
}

# UP14: without NSSM the server runs as a scheduled task. Task Scheduler
# does not start a task again after a clean exit, so the launcher loops and
# only then tells the server it is supervised.
function Test-TaskLauncherLoops {
    $dir = New-TestInstall
    Setup-Services | Out-Null
    $launcher = Join-Path $script:RUSTDESK_PATH 'start-betterdesk-server.cmd'
    if (-not (Test-Path $launcher)) { Fail 'no launcher written'; return }
    $text = Get-Content $launcher -Raw
    if ($text -notmatch '(?m)^:run\r?$') { Fail 'the launcher has no loop label' }
    if ($text -notmatch '(?m)^goto run\r?$') { Fail 'the launcher does not start the server again' }
    if ($text -notmatch '(?m)^set "BETTERDESK_SUPERVISOR=task"\r?$') { Fail 'the launcher does not set BETTERDESK_SUPERVISOR=task' }
    if ($text -notmatch [regex]::Escape('set "BETTERDESK_ENV_FILE=' + (Join-Path $script:CONSOLE_PATH '.env') + '"')) { Fail 'the launcher does not name the env file' }
    if ($text -match 'CONSOLE_PORT=0') { Fail 'the launcher keeps the console off' }
    $settings = $script:registeredTasks['BetterDeskServer']
    if (-not $settings) { Fail 'no task registered' }
    elseif ($settings.ExecutionTimeLimit -ne [TimeSpan]::Zero) { Fail 'the task has a time limit, which would stop the loop' }
    Remove-Item -Recurse -Force $dir
}

# BN4: an update rewrites the launcher of a task install made while the
# Node.js console ran (CONSOLE_PORT=0, no loop).
function Test-UpdateRewritesOldLauncher {
    $dir = New-TestInstall
    $launcher = Join-Path $script:RUSTDESK_PATH 'start-betterdesk-server.cmd'
    $exe = Join-Path $script:RUSTDESK_PATH 'betterdesk-server.exe'
    Set-Content -Path $launcher -Encoding ASCII -Value @(
        '@echo off',
        'set "DB_URL=C:\BetterDesk\db_v2.sqlite3"',
        'set "CONSOLE_PORT=0"',
        "`"$exe`" -mode all -relay-servers 203.0.113.7 -api-port 21114"
    )
    $script:registeredTasks['BetterDeskServer'] = 'old'
    Update-ServerServiceDefinition | Out-Null
    $text = Get-Content $launcher -Raw
    if ($text -match 'CONSOLE_PORT=0') { Fail 'the update kept CONSOLE_PORT=0 in the launcher' }
    if ($text -notmatch '(?m)^goto run\r?$') { Fail 'the update did not give the launcher its loop' }
    if ($text -notmatch [regex]::Escape('set "DB_URL=C:\BetterDesk\db_v2.sqlite3"')) { Fail 'the update lost the launcher settings' }
    if ($text -notmatch [regex]::Escape("`"$exe`" -mode all -relay-servers 203.0.113.7 -api-port 21114")) { Fail 'the update changed the server arguments' }
    Remove-Item -Recurse -Force $dir
}

# RC1: the NSSM service tells the server that NSSM starts it again
# (BETTERDESK_SUPERVISOR=nssm) and restarts it after any exit.
function Test-NssmServiceIsSupervised {
    $dir = New-TestInstall
    $script:fakeNssm = 'fake-nssm'
    $script:nssmCalls = @()
    Setup-Services | Out-Null
    $script:fakeNssm = $null
    $envCall = $script:nssmCalls | Where-Object { $_[0] -eq 'set' -and $_[2] -eq 'AppEnvironmentExtra' } | Select-Object -Last 1
    if (-not $envCall) { Fail 'the NSSM service gets no environment' }
    else {
        if ($envCall -notcontains 'BETTERDESK_SUPERVISOR=nssm') { Fail "the NSSM service has no BETTERDESK_SUPERVISOR=nssm: $($envCall -join ' ')" }
        if ($envCall -contains 'CONSOLE_PORT=0') { Fail 'the NSSM service keeps the console off' }
    }
    $exitCall = $script:nssmCalls | Where-Object { $_[0] -eq 'set' -and $_[2] -eq 'AppExit' } | Select-Object -Last 1
    if (-not $exitCall -or ($exitCall -join ' ') -notmatch 'AppExit Default Restart$') { Fail 'NSSM does not restart the server after it ends' }
    Remove-Item -Recurse -Force $dir
}

Test-TaskLauncherLoops
Test-UpdateRewritesOldLauncher
Test-NssmServiceIsSupervised
if ($script:failures -gt 0) { exit 1 }
Write-Host 'Windows installer checks passed'
