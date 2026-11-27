# The Windows service on a real Windows machine (CI's windows-latest runner,
# run as administrator; it changes the machine, so never run it on yours):
# install/neodesk.ps1 -Auto installs the binary given with -Binary as the
# service NeoDeskServer, then the checks below run against the service
# manager. Last, an install made the old way (a scheduled task under SYSTEM
# with its launcher) is updated and becomes the service, and the uninstall
# removes it.
#
#   powershell -NoProfile -ExecutionPolicy Bypass -File scripts\test-windows-service.ps1 -Binary <neodesk-server.exe>
param([Parameter(Mandatory = $true)][string]$Binary)
$ErrorActionPreference = 'Stop'

$root = Split-Path -Parent $PSScriptRoot
$installer = Join-Path $root 'install\neodesk.ps1'
$name = 'NeoDeskServer'
$serverDir = 'C:\NeoDesk'
$exe = Join-Path $serverDir 'neodesk-server.exe'
$log = Join-Path $serverDir 'logs\server.log'
$script:failures = 0
function Fail([string]$Message) { Write-Host "FAIL: $Message" -ForegroundColor Red; $script:failures++ }
function Step([string]$Message) { Write-Host ""; Write-Host "== $Message" -ForegroundColor Cyan }

function Invoke-Installer([string[]]$Arguments) {
    $env:RELAY_SERVERS = '127.0.0.1'
    & powershell.exe -NoProfile -NonInteractive -ExecutionPolicy Bypass -File $installer @Arguments | Out-Host
    return $LASTEXITCODE
}

function Get-ServiceConfig { Get-CimInstance -ClassName Win32_Service -Filter "Name='$name'" }

function Wait-Until([scriptblock]$Condition, [int]$Seconds) {
    $deadline = (Get-Date).AddSeconds($Seconds)
    do {
        if (& $Condition) { return $true }
        Start-Sleep -Seconds 1
    } while ((Get-Date) -lt $deadline)
    return $false
}

function Test-ServerAnswers {
    & $exe check --api http://127.0.0.1:21114/api/health --port 127.0.0.1:21116 --port 127.0.0.1:21117 | Out-Null
    return ($LASTEXITCODE -eq 0)
}

function Get-ServerProcessId {
    $svc = Get-ServiceConfig
    if ($svc -and $svc.ProcessId) { return [int]$svc.ProcessId }
    return 0
}

function Get-LogText { if (Test-Path $log) { Get-Content $log -Raw } else { '' } }

Step 'Install with -Auto'
$code = Invoke-Installer @('-Auto', '-Binary', $Binary)
if ($code -ne 0) { Fail "the install ended with $code" }

Step 'The service as the service manager has it'
$svc = Get-ServiceConfig
if (-not $svc) { Fail 'no service NeoDeskServer'; exit 1 }
if ($svc.PathName -notlike "`"$exe`" -mode all *") { Fail "the service runs $($svc.PathName)" }
if ($svc.StartName -ne "NT SERVICE\$name") { Fail "the service runs as $($svc.StartName)" }
if ($svc.StartMode -ne 'Auto') { Fail "the service starts $($svc.StartMode)" }
if ($svc.DisplayName -ne 'NeoDesk Server') { Fail "the display name is $($svc.DisplayName)" }
if (Get-ScheduledTask -TaskName $name -ErrorAction SilentlyContinue) { Fail 'the install registered a scheduled task' }
$environment = (Get-ItemProperty -Path "HKLM:\SYSTEM\CurrentControlSet\Services\$name" -Name Environment).Environment
if ($environment -notcontains 'NEODESK_SUPERVISOR=windows-service') { Fail "the service's environment: $($environment -join ' ')" }
$failure = (& sc.exe qfailure $name) -join "`n"
if ($failure -notmatch 'RESTART') { Fail "no restart after a failure: $failure" }
$flag = (& sc.exe qfailureflag $name) -join "`n"
if ($flag -notmatch 'TRUE') { Fail "an exit with an error status does not count as a failure: $flag" }

Step 'It runs under its own account and answers'
if (-not (Wait-Until { (Get-Service $name).Status -eq 'Running' -and (Test-ServerAnswers) } 90)) { Fail 'the service does not answer' }
$owner = Invoke-CimMethod -InputObject (Get-CimInstance Win32_Process -Filter "ProcessId=$(Get-ServerProcessId)") -MethodName GetOwner
if ("$($owner.Domain)\$($owner.User)" -ne "NT SERVICE\$name") { Fail "the server process runs as $($owner.Domain)\$($owner.User)" }
if ((Get-LogText) -notmatch 'NeoDesk Server') { Fail "the server writes no log to $log" }
$acl = (& icacls (Join-Path $serverDir '.api_key')) -join "`n"
if ($acl -notmatch "NT SERVICE\\$name" -or $acl -match 'BUILTIN\\Users') { Fail "the API key's ACL: $acl" }

Step 'A stop of the service stops the server cleanly and it stays stopped'
$before = (Get-LogText).Length
Stop-Service -Name $name
$after = (Get-LogText).Substring($before)
foreach ($line in 'Service stop requested, shutting down', 'Server stopped', "Windows service $name stopped (exit status 0)") {
    if (-not $after.Contains($line)) { Fail "the log of the stop has no '$line': $after" }
}
Start-Sleep -Seconds 8
if ((Get-Service $name).Status -ne 'Stopped') { Fail "after a stop the service is $((Get-Service $name).Status): a clean stop counted as a failure" }

Step 'A crash is followed by a new start'
Start-Service -Name $name
if (-not (Wait-Until { Test-ServerAnswers } 90)) { Fail 'the service does not answer after its start' }
$pid1 = Get-ServerProcessId
Stop-Process -Id $pid1 -Force
if (-not (Wait-Until { $p = Get-ServerProcessId; $p -ne 0 -and $p -ne $pid1 -and (Test-ServerAnswers) } 60)) { Fail 'the service manager did not start the server again after it was ended' }

Step 'An install made the old way, a scheduled task under SYSTEM, becomes the service at its update'
$serverArgs = ($svc.PathName -replace '^"[^"]*"\s*', '')
Stop-Service -Name $name
& sc.exe delete $name | Out-Null
$launcher = Join-Path $serverDir 'start-neodesk-server.cmd'
$lines = @('@echo off')
foreach ($pair in $environment) { if ($pair -notlike 'NEODESK_SUPERVISOR=*') { $lines += "set `"$pair`"" } }
$lines += @('set "NEODESK_SUPERVISOR=task"', ':run', "`"$exe`" $serverArgs >> `"$log`" 2>&1", 'timeout /t 2 /nobreak >nul', 'goto run')
Set-Content -Path $launcher -Value $lines -Encoding ASCII
$action = New-ScheduledTaskAction -Execute 'cmd.exe' -Argument "/c `"$launcher`"" -WorkingDirectory $serverDir
$principal = New-ScheduledTaskPrincipal -UserId 'SYSTEM' -LogonType ServiceAccount -RunLevel Highest
$settings = New-ScheduledTaskSettingsSet -ExecutionTimeLimit ([TimeSpan]::Zero)
Register-ScheduledTask -TaskName $name -Action $action -Trigger (New-ScheduledTaskTrigger -AtStartup) -Principal $principal -Settings $settings | Out-Null
Start-ScheduledTask -TaskName $name
if (-not (Wait-Until { Test-ServerAnswers } 90)) { Fail 'the old-style task install does not answer' }
$code = Invoke-Installer @('-Update', '-Binary', $Binary)
if ($code -ne 0) { Fail "the update ended with $code" }
if (Get-ScheduledTask -TaskName $name -ErrorAction SilentlyContinue) { Fail 'the task stays after the update' }
if (Test-Path $launcher) { Fail 'the launcher stays after the update' }
$svc = Get-ServiceConfig
if (-not $svc -or $svc.StartName -ne "NT SERVICE\$name") { Fail "after the update the server is not the service under its account: $($svc.StartName)" }
elseif (($svc.PathName -replace '^"[^"]*"\s*', '') -ne $serverArgs) { Fail "the service lost the task's arguments: $($svc.PathName)" }
if (-not (Wait-Until { (Get-Service $name).Status -eq 'Running' -and (Test-ServerAnswers) } 90)) { Fail 'the service does not answer after the update' }
if (@(Get-Process -Name 'neodesk-server' -ErrorAction SilentlyContinue).Count -ne 1) { Fail 'more than one server runs after the update' }

Step 'The uninstall removes the service'
$code = Invoke-Installer @('-Uninstall', '-Auto', '-Purge')
if (Get-Service -Name $name -ErrorAction SilentlyContinue) { Fail 'the service stays after the uninstall' }
if (Get-Process -Name 'neodesk-server' -ErrorAction SilentlyContinue) { Fail 'a server still runs after the uninstall' }

if ($script:failures -gt 0) { Write-Host "$($script:failures) check(s) failed"; exit 1 }
Write-Host 'Windows service checks passed'
exit 0
