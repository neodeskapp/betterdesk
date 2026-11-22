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

# The console's language files, themes and images come from the binary:
# an update copies none from a web-nodejs folder an older release left in
# the checkout, and the console folder keeps its .env, data, installed
# fonts and language files.
function Test-ConsoleHomeCopiesNoConsoleFiles {
    $dir = New-TestInstall
    $old = Join-Path $dir 'web-nodejs'
    $console = $script:CONSOLE_PATH
    New-Item -ItemType Directory -Force -Path (Join-Path $old 'lang'), (Join-Path $old 'themes'), `
        (Join-Path $console 'data\transfers'), (Join-Path $console 'public\fonts'), (Join-Path $console 'lang') | Out-Null
    Set-Content -Path (Join-Path $old 'lang\en.json') -Value '{"old":true}'
    Set-Content -Path (Join-Path $old 'themes\dark.css') -Value 'old'
    Set-Content -Path (Join-Path $console '.env') -Value 'SESSION_SECRET=kept'
    Set-Content -Path (Join-Path $console 'data\transfers\a.bin') -Value 'file'
    Set-Content -Path (Join-Path $console 'public\fonts\inter.woff2') -Value 'font'
    Set-Content -Path (Join-Path $console 'lang\xx.json') -Value '{"custom":true}'
    $script:MINIMAL_MODE = $false
    $script:mergedFresh = $null
    function Merge-ConsoleEnv { param([bool]$FreshInstall) $script:mergedFresh = $FreshInstall; return $true }
    Setup-ConsoleHome | Out-Null
    if ($script:mergedFresh -ne $false) { Fail "an install with .env counted as fresh ($($script:mergedFresh))" }
    if (Test-Path (Join-Path $console 'lang\en.json')) { Fail "the update copied the checkout's web-nodejs\lang into the console folder" }
    if (Test-Path (Join-Path $console 'themes')) { Fail "the update copied the checkout's web-nodejs\themes into the console folder" }
    if ((Get-Content (Join-Path $console '.env') -Raw).Trim() -ne 'SESSION_SECRET=kept') { Fail 'the update changed .env' }
    if (-not (Test-Path (Join-Path $console 'data\transfers\a.bin'))) { Fail 'the update lost a file of data' }
    if (-not (Test-Path (Join-Path $console 'public\fonts\inter.woff2'))) { Fail 'the update lost an installed font' }
    if (-not (Test-Path (Join-Path $console 'lang\xx.json'))) { Fail 'the update lost a language file of the console folder' }
    Remove-Item -Recurse -Force $dir
}

# RV83: the migrate tool replaces an existing SQLite destination only with
# -overwrite. The menus pass it after the user agreed, and do not run the
# migration when the user declined.
function Test-MigrationMenusPassOverwrite {
    $dir = New-TestInstall
    $script:AUTO_MODE = $false
    $script:migrateCalls = @()
    function Get-MigrateBin { return 'fake-migrate' }
    function fake-migrate { $script:migrateCalls += ($args -join ' '); $global:LASTEXITCODE = 0 }
    function Invoke-MenuChoose { param($Title, $Subtitle, $Items, $Returns) $script:MENU_CHOICE = $script:testChoice; $script:menuItems = $Items }
    function Read-Host { param($Prompt) if ($script:answers.Count -gt 0) { $script:answers.Dequeue() } else { '' } }
    function Press-Enter { }
    function Clear-Host { }
    function Invoke-TestMenu([string]$Choice, [string[]]$Answers) {
        $script:migrateCalls = @()
        $script:testChoice = $Choice
        $script:answers = New-Object System.Collections.Queue
        foreach ($a in $Answers) { $script:answers.Enqueue($a) }
        Do-MigrateDatabase *> $null
        return @($script:migrateCalls | Where-Object { $_ -match '-mode (rust2go|nodejs2go|pg2sqlite|sqlite2pg)' })
    }

    $src = Join-Path $script:RUSTDESK_PATH 'db_v2.sqlite3'
    $dst = Join-Path $script:RUSTDESK_PATH 'betterdesk.sqlite3'
    Set-Content -Path $src -Value 'db'
    Set-Content -Path $dst -Value 'old'

    # RV81: sqlite2pg and pg2sqlite move the whole install, and the menu
    # says so. sqlite2pg replaces a PostgreSQL database with data in it only
    # after the user agreed.
    $runs = Invoke-TestMenu '2' @('', 'postgres://u@h/db', 'y')
    $offered = (@($script:menuItems | ForEach-Object { ($_ -split "`t")[0] }) -join '|')
    if ($offered -ne 'Rust -> Go|SQLite -> PostgreSQL|PostgreSQL -> SQLite|Backup|Back') { Fail "the migration menu offers: $offered" }
    if (-not ($script:menuItems -match '^SQLite -> PostgreSQL\tWhole install')) { Fail "the menu does not say SQLite -> PostgreSQL moves the whole install: $($script:menuItems)" }
    if (-not ($script:menuItems -match '^PostgreSQL -> SQLite\tWhole install')) { Fail "the menu does not say PostgreSQL -> SQLite moves the whole install: $($script:menuItems)" }
    if ("$runs" -ne "-mode sqlite2pg -src $src -dst postgres://u@h/db -overwrite") { Fail "sqlite2pg after an agreed replace ran as: $runs" }
    $runs = Invoke-TestMenu '2' @('', 'postgres://u@h/db', 'n')
    if ("$runs" -ne "-mode sqlite2pg -src $src -dst postgres://u@h/db") { Fail "sqlite2pg after a declined replace ran as: $runs" }

    $runs = Invoke-TestMenu '3' @('postgres://u@h/db', '', 'y')
    if ($runs -notcontains "-mode pg2sqlite -src postgres://u@h/db -dst $src -overwrite") { Fail "pg2sqlite after an agreed overwrite ran as: $runs" }
    $runs = Invoke-TestMenu '3' @('postgres://u@h/db', '', 'n')
    if ($runs.Count -ne 0) { Fail "pg2sqlite ran although the overwrite was declined: $runs" }

    $runs = Invoke-TestMenu '1' @('', '', 'y')
    if ($runs -notcontains "-mode rust2go -src $src -dst $dst -overwrite") { Fail "rust2go after an agreed overwrite ran as: $runs" }
    $runs = Invoke-TestMenu '1' @('', '', 'n')
    if ($runs.Count -ne 0) { Fail "rust2go ran although the overwrite was declined: $runs" }

    Remove-Item -Force $dst
    $runs = Invoke-TestMenu '1' @('', '')
    if ($runs -notcontains "-mode rust2go -src $src -dst $dst") { Fail "rust2go into a new file ran as: $runs" }
    $runs = Invoke-TestMenu '1' @('', 'postgres://u@h/db')
    if ($runs -notcontains "-mode rust2go -src $src -dst postgres://u@h/db") { Fail "rust2go to PostgreSQL ran as: $runs" }
    Remove-Item -Recurse -Force $dir
}

# RV81: the install's switch to PostgreSQL moves the whole SQLite install
# with sqlite2pg, passes -overwrite only after the user agreed (the
# installer does not create the database on Windows), and points .env at
# PostgreSQL only after the move succeeded; when it fails the install stays
# on its SQLite file.
function Test-PostgreSQLSwitchMovesTheWholeInstall {
    $dir = New-TestInstall
    $script:migrateCalls = @()
    $script:migrateExit = 0
    function Get-MigrateBin { return 'fake-migrate' }
    function fake-migrate {
        $script:migrateCalls += ($args -join ' ')
        $global:LASTEXITCODE = if ($args[1] -eq 'sqlite2pg') { $script:migrateExit } else { 0 }
    }
    function Read-Host { param($Prompt) if ($script:answers.Count -gt 0) { $script:answers.Dequeue() } else { '' } }
    $src = Join-Path $script:RUSTDESK_PATH 'db_v2.sqlite3'
    $envFile = Join-Path $script:CONSOLE_PATH '.env'
    $uri = 'postgres://u:p@h/db?sslmode=disable'
    Set-Content -Path $src -Value 'db'
    function Invoke-Switch([int]$Exit, [bool]$Auto, [string[]]$Answers) {
        $script:migrateCalls = @()
        $script:migrateExit = $Exit
        $script:answers = New-Object System.Collections.Queue
        foreach ($a in $Answers) { $script:answers.Enqueue($a) }
        Set-Content -Path $envFile -Value @('SESSION_SECRET=kept', 'DB_TYPE=postgres', "DATABASE_URL=$uri", "DB_URL=$uri")
        $script:USE_POSTGRESQL = $true
        $script:POSTGRESQL_URI = $uri
        $script:AUTO_MODE = $Auto
        Migrate-SQLiteToPostgreSQL *> $null
        return @($script:migrateCalls | Where-Object { $_ -match '-mode (sqlite2pg|nodejs2go)' })
    }

    $runs = Invoke-Switch 0 $true @()
    if ("$runs" -ne "-mode sqlite2pg -src $src -dst $uri") { Fail "the unattended switch ran as: $runs" }
    if (-not ($script:migrateCalls -contains "-mode backup -src $src")) { Fail "the switch made no backup first: $($script:migrateCalls)" }
    if (-not $script:USE_POSTGRESQL) { Fail 'after a move the install does not use PostgreSQL' }
    if ((Get-ConsoleEnvValue -Key 'DB_TYPE') -ne 'postgres' -or (Get-ConsoleEnvValue -Key 'DB_URL') -ne $uri -or (Get-ConsoleEnvValue -Key 'DATABASE_URL') -ne $uri) {
        Fail "after a move .env does not point at PostgreSQL: $(Get-Content $envFile)"
    }
    if ((Get-ConsoleEnvValue -Key 'SESSION_SECRET') -ne 'kept') { Fail 'the switch lost a line of .env' }

    $runs = Invoke-Switch 0 $false @('y', 'y')
    if ("$runs" -ne "-mode sqlite2pg -src $src -dst $uri -overwrite") { Fail "the switch after an agreed replace ran as: $runs" }
    $runs = Invoke-Switch 0 $false @('y', 'n')
    if ("$runs" -ne "-mode sqlite2pg -src $src -dst $uri") { Fail "the switch after a declined replace ran as: $runs" }

    # L60: a declined move keeps the install on SQLite
    $runs = Invoke-Switch 0 $false @('n')
    if ($runs.Count -ne 0) { Fail "the switch moved the data although the move was declined: $runs" }
    if ($script:USE_POSTGRESQL -or $script:POSTGRESQL_URI) { Fail "after a declined move the install still uses PostgreSQL ($($script:USE_POSTGRESQL), $($script:POSTGRESQL_URI))" }
    if ((Get-ConsoleEnvValue -Key 'DB_TYPE') -ne 'sqlite' -or (Get-ConsoleEnvValue -Key 'DB_URL') -ne $src -or (Get-ConsoleEnvValue -Key 'DATABASE_URL') -ne '') {
        Fail "after a declined move .env does not point at the SQLite file: $(Get-Content $envFile)"
    }

    $runs = Invoke-Switch 1 $true @()
    if ($script:USE_POSTGRESQL -or $script:POSTGRESQL_URI) { Fail "after a failed move the install still uses PostgreSQL ($($script:USE_POSTGRESQL), $($script:POSTGRESQL_URI))" }
    if ((Get-ConsoleEnvValue -Key 'DB_TYPE') -ne 'sqlite' -or (Get-ConsoleEnvValue -Key 'DB_URL') -ne $src -or (Get-ConsoleEnvValue -Key 'DATABASE_URL') -ne '') {
        Fail "after a failed move .env does not point at the SQLite file: $(Get-Content $envFile)"
    }
    $script:AUTO_MODE = $true
    Remove-Item -Recurse -Force $dir
}

# L31: releases come from the fork's repository. The defaults are set at
# the script's top level, so those lines are run here as they are.
function Test-ReleasesComeFromTheFork {
    $saved = @{}
    foreach ($k in 'UPDATE_GITHUB_OWNER', 'UPDATE_GITHUB_REPO', 'UPDATE_GITHUB_BRANCH', 'RELEASE_BASE_URL') {
        $saved[$k] = [Environment]::GetEnvironmentVariable($k)
        [Environment]::SetEnvironmentVariable($k, $null)
    }
    $defaults = $ast.FindAll({
        $args[0] -is [System.Management.Automation.Language.AssignmentStatementAst] -and
        $args[0].Left.Extent.Text -like '$script:UPDATE_GITHUB_*'
    }, $false)
    if ($defaults.Count -lt 2) { Fail "the installer sets no repository for releases ($($defaults.Count) lines)" }
    foreach ($a in $defaults) { . ([scriptblock]::Create($a.Extent.Text)) }
    $base = Get-ReleaseBaseUrl
    if ($base -ne 'https://github.com/trybetterdesk/betterdesk/releases/latest/download') { Fail "the stable channel downloads from $base" }
    $script:UPDATE_GITHUB_BRANCH = 'dev'
    $base = Get-ReleaseBaseUrl
    if ($base -ne 'https://github.com/trybetterdesk/betterdesk/releases/download/dev') { Fail "the development channel downloads from $base" }
    $script:UPDATE_GITHUB_BRANCH = 'main'
    foreach ($k in $saved.Keys) { [Environment]::SetEnvironmentVariable($k, $saved[$k]) }
}

# A stand-in for betterdesk-server.exe: env merge writes a small .env (the
# admin password only on a fresh one), upgrade-env changes nothing.
function Use-FakeServerBinary {
    function script:Get-ServerBinaryPath { 'fake-server' }
    function script:Test-BinaryHasSubcommands { param($Path) $true }
    function script:fake-server {
        if ($args[0] -eq 'env' -and $args[1] -eq 'merge') {
            $target = $args[3]
            if ($args -contains '--fresh') {
                Set-Content -Path $target -Value @("DEFAULT_ADMIN_PASSWORD=$env:BD_SUBST_DEFAULT_ADMIN_PASSWORD", 'DB_TYPE=sqlite')
            }
            Write-Output '{"success":true}'
        }
        $global:LASTEXITCODE = 0
    }
}

# L65 and L36: a fresh install starts in Managed through .env, the first
# default a mode saved in the console replaces. An update adds no mode,
# and the service gets neither the mode nor ENROLLMENT_MODE_ENV_OVERRIDE,
# which would put the installer's value over the console's.
function Test-FreshInstallStartsManaged {
    $dir = New-TestInstall
    Use-FakeServerBinary
    $envFile = Join-Path $script:CONSOLE_PATH '.env'
    $env:ADMIN_PASSWORD = 'Fresh-Admin-Pass-1'
    Merge-ConsoleEnv -FreshInstall $true *> $null
    if ((Get-ConsoleEnvValue -Key 'ENROLLMENT_MODE') -ne 'managed') { Fail "a fresh .env does not start in Managed: $(Get-Content $envFile)" }
    Set-Content -Path $envFile -Value @('SESSION_SECRET=kept', 'DB_TYPE=sqlite')
    Merge-ConsoleEnv -FreshInstall $false *> $null
    if (Get-ConsoleEnvValue -Key 'ENROLLMENT_MODE') { Fail 'an update added an enrollment mode to .env' }
    $env:ADMIN_PASSWORD = $null

    $script:fakeNssm = 'fake-nssm'
    $script:nssmCalls = @()
    Setup-Services | Out-Null
    $script:fakeNssm = $null
    $envCall = $script:nssmCalls | Where-Object { $_[0] -eq 'set' -and $_[2] -eq 'AppEnvironmentExtra' } | Select-Object -Last 1
    if (-not $envCall) { Fail 'the NSSM service gets no environment' }
    elseif (@($envCall | Where-Object { $_ -like 'ENROLLMENT_MODE*' }).Count -gt 0) { Fail "the service sets the enrollment mode: $($envCall -join ' ')" }
    Remove-Item -Recurse -Force $dir
}

# L124: Windows does not create the PostgreSQL database, so an unattended
# install takes the address it is given as it is, and without one it needs
# the password of the existing database instead of making one up.
function Test-PostgreSQLAddressIsKept {
    $dir = New-TestInstall
    $script:POSTGRESQL_USER, $script:POSTGRESQL_HOST, $script:POSTGRESQL_PORT, $script:POSTGRESQL_DB = 'betterdesk', 'localhost', '5432', 'betterdesk'
    $script:USE_POSTGRESQL = $true
    $script:POSTGRESQL_URI = 'postgres://bd:Given-Pass@db.example:5433/desk?sslmode=require'
    $script:POSTGRESQL_PASS = ''
    $ok = Setup-PostgreSQLDatabase 6> $null
    if (-not $ok) { Fail 'the PostgreSQL set-up refused a given address' }
    if ($script:POSTGRESQL_URI -ne 'postgres://bd:Given-Pass@db.example:5433/desk?sslmode=require') { Fail "the given address was replaced: $($script:POSTGRESQL_URI)" }

    $script:POSTGRESQL_URI = ''
    $ok = Setup-PostgreSQLDatabase 6> $null
    if ($ok) { Fail "an unattended install without an address or password went on with $($script:POSTGRESQL_URI)" }

    $script:POSTGRESQL_PASS = 'p@ss/word'
    $ok = Setup-PostgreSQLDatabase 6> $null
    if (-not $ok -or $script:POSTGRESQL_URI -ne 'postgres://betterdesk:p%40ss%2Fword@localhost:5432/betterdesk?sslmode=disable') { Fail "the address from the parts is $($script:POSTGRESQL_URI)" }
    $script:USE_POSTGRESQL = $false
    $script:POSTGRESQL_URI = ''
    $script:POSTGRESQL_PASS = ''
    Remove-Item -Recurse -Force $dir
}

Test-TaskLauncherLoops
Test-UpdateRewritesOldLauncher
Test-NssmServiceIsSupervised
Test-ConsoleHomeCopiesNoConsoleFiles
Test-MigrationMenusPassOverwrite
Test-PostgreSQLSwitchMovesTheWholeInstall
Test-ReleasesComeFromTheFork
Test-PostgreSQLAddressIsKept
Test-FreshInstallStartsManaged
if ($script:failures -gt 0) { exit 1 }
Write-Host 'Windows installer checks passed'
# The stand-ins set LASTEXITCODE, and a caller such as GitHub's pwsh step
# exits with it when the script ends without an exit of its own.
exit 0
