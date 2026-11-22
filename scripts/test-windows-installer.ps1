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
# GitHub's API gives no answer unless a test stands in for it, so no check
# reaches the network
function Invoke-RestMethod { throw [System.Net.WebException]::new('The remote name could not be resolved: api.github.com') }

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

    # L72: a move of the install's own database points .env at the new one
    # and restarts the server
    $envFile = Join-Path $script:CONSOLE_PATH '.env'
    Set-Content -Path $envFile -Value @('SESSION_SECRET=kept', 'DB_TYPE=sqlite', 'DATABASE_URL=', "DB_URL=$src")
    $script:restarted = $false
    function script:Stop-ServerService { }
    function script:Start-ServerService { $script:restarted = $true }
    Invoke-TestMenu '2' @('', 'postgres://u@h/db', 'n') | Out-Null
    if ((Get-ConsoleEnvValue -Key 'DB_TYPE') -ne 'postgres' -or (Get-ConsoleEnvValue -Key 'DATABASE_URL') -ne 'postgres://u@h/db' -or (Get-ConsoleEnvValue -Key 'DB_URL') -ne 'postgres://u@h/db') {
        Fail "after the menu's move .env does not point at PostgreSQL: $(Get-Content $envFile)"
    }
    if (-not $script:restarted) { Fail 'the server was not started again after the move' }
    Invoke-TestMenu '3' @('postgres://u@h/db', '', 'y') | Out-Null
    if ((Get-ConsoleEnvValue -Key 'DB_TYPE') -ne 'sqlite' -or (Get-ConsoleEnvValue -Key 'DB_URL') -ne $src) { Fail "after the menu's move back .env does not point at SQLite: $(Get-Content $envFile)" }
    Remove-Item -Force $envFile

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

# L225: -PgUri means PostgreSQL as its help says, also without
# -PostgreSQL, and an interactive install asks no database question for
# it, where the menu's default made it SQLite again.
function Test-PgUriMeansPostgreSQL {
    $dir = New-TestInstall
    $saved = $env:USE_POSTGRESQL
    $env:USE_POSTGRESQL = $null
    $uri = 'postgres://bd:Given-Pass@db.example:5433/desk?sslmode=require'
    if (-not (Test-PostgreSQLWanted -Switch $false -Uri $uri)) { Fail '-PgUri alone does not mean PostgreSQL' }
    if (Test-PostgreSQLWanted -Switch $false -Uri '') { Fail 'neither -PostgreSQL nor -PgUri means PostgreSQL' }
    if (-not (Test-PostgreSQLWanted -Switch $true -Uri '')) { Fail '-PostgreSQL does not mean PostgreSQL' }
    $script:menuShown = $false
    function Invoke-MenuChoose { param($Title, $Subtitle, $Items, $Returns) $script:menuShown = $true; $script:MENU_CHOICE = '1' }
    $script:AUTO_MODE = $false
    $script:USE_POSTGRESQL = $true
    $script:POSTGRESQL_URI = $uri
    Choose-DatabaseType 6> $null
    if ($script:menuShown) { Fail 'the database question was asked with an address given' }
    if (-not $script:USE_POSTGRESQL -or $script:POSTGRESQL_URI -ne $uri) { Fail "the given address did not stay: $($script:USE_POSTGRESQL) $($script:POSTGRESQL_URI)" }
    $script:AUTO_MODE = $true
    $script:USE_POSTGRESQL = $false
    $script:POSTGRESQL_URI = ''
    $env:USE_POSTGRESQL = $saved
    Remove-Item -Recurse -Force $dir
}

# L74: the server's log, which it writes to standard error, goes to
# logs\server.log, as a service and as a scheduled task; an update of the
# task keeps one redirect.
function Test-ServerLogFile {
    $dir = New-TestInstall
    $log = Join-Path $script:RUSTDESK_PATH 'logs\server.log'
    $script:fakeNssm = 'fake-nssm'
    $script:nssmCalls = @()
    Setup-Services | Out-Null
    $script:fakeNssm = $null
    foreach ($stream in 'AppStdout', 'AppStderr') {
        $call = $script:nssmCalls | Where-Object { $_[0] -eq 'set' -and $_[2] -eq $stream } | Select-Object -Last 1
        if (-not $call -or $call[3] -ne $log) { Fail "the service's $stream goes to $($call[3]), not $log" }
    }
    $script:registeredTasks = @{}
    Setup-Services | Out-Null
    $launcher = Join-Path $script:RUSTDESK_PATH 'start-betterdesk-server.cmd'
    $redirect = '>> "' + $log + '" 2>&1'
    $line = @(Get-Content $launcher | Where-Object { $_ -like '"*betterdesk-server.exe"*' })
    if ($line.Count -ne 1 -or -not $line[0].EndsWith($redirect)) { Fail "the task's server line does not write $log`: $line" }
    Update-ServerServiceDefinition | Out-Null
    $line = @(Get-Content $launcher | Where-Object { $_ -like '"*betterdesk-server.exe"*' })
    if ($line.Count -ne 1 -or -not $line[0].EndsWith($redirect) -or ($line[0] -split '>>').Count -ne 2) { Fail "an update of the task changed the redirect: $line" }
    Remove-Item -Recurse -Force $dir
}

# L68: the signal and relay ports live in .env, where the admin changes
# them. The service does not set them, and an update moves the ports an
# older launcher set into .env instead of setting 21116 and 21117 again.
function Test-UpdateKeepsTheAdminsPorts {
    $dir = New-TestInstall
    $envFile = Join-Path $script:CONSOLE_PATH '.env'
    Set-Content -Path $envFile -Value @('SESSION_SECRET=kept', 'SIGNAL_PORT=21116', 'RELAY_PORT=21117')
    Setup-Services | Out-Null
    $launcher = Join-Path $script:RUSTDESK_PATH 'start-betterdesk-server.cmd'
    if ((Get-Content $launcher -Raw) -match '(SIGNAL|RELAY)_PORT') { Fail "the launcher sets the ports: $(Get-Content $launcher)" }
    $exe = Join-Path $script:RUSTDESK_PATH 'betterdesk-server.exe'
    Set-Content -Path $launcher -Encoding ASCII -Value @(
        '@echo off',
        'set "BETTERDESK_SUPERVISOR=task"',
        'set "SIGNAL_PORT=31116"',
        'set "RELAY_PORT=31117"',
        ':run',
        "`"$exe`" -mode all -api-port 21114",
        'goto run'
    )
    Update-ServerServiceDefinition | Out-Null
    if ((Get-Content $launcher -Raw) -match '(SIGNAL|RELAY)_PORT') { Fail "the updated launcher sets the ports: $(Get-Content $launcher)" }
    if ((Get-ConsoleEnvValue -Key 'SIGNAL_PORT') -ne '31116' -or (Get-ConsoleEnvValue -Key 'RELAY_PORT') -ne '31117') { Fail "the update lost the launcher's ports: $(Get-Content $envFile)" }
    if ((Get-ConsoleEnvValue -Key 'SESSION_SECRET') -ne 'kept') { Fail 'the update lost a line of .env' }
    Remove-Item -Recurse -Force $dir
}

# L140: -Channel picks the channel, wins over .env and goes into it, and a
# failed download says what to do: no stable release yet, or no way out.
function Test-ChannelAndDownloadErrors {
    $dir = New-TestInstall
    $saved = $env:UPDATE_GITHUB_BRANCH
    $env:UPDATE_GITHUB_BRANCH = $null
    $Channel = 'dev'
    $defaults = $ast.FindAll({
        $args[0] -is [System.Management.Automation.Language.AssignmentStatementAst] -and
        $args[0].Left.Extent.Text -like '$script:UPDATE_GITHUB_*'
    }, $false)
    foreach ($a in $defaults) { . ([scriptblock]::Create($a.Extent.Text)) }
    $envFile = Join-Path $script:CONSOLE_PATH '.env'
    Set-Content -Path $envFile -Value @('UPDATE_GITHUB_BRANCH=main')
    Read-UpdateGitHubBranchFromEnv
    if ($script:UPDATE_GITHUB_BRANCH -ne 'dev') { Fail "-Channel dev lost to the main of .env: $($script:UPDATE_GITHUB_BRANCH)" }
    Use-FakeServerBinary
    Merge-ConsoleEnv -FreshInstall $false *> $null
    if ((Get-ConsoleEnvValue -Key 'UPDATE_GITHUB_BRANCH') -ne 'dev') { Fail "the given channel is not in .env: $(Get-Content $envFile)" }

    $script:UPDATE_GITHUB_BRANCH_GIVEN = ''
    $script:UPDATE_GITHUB_BRANCH = 'main'
    Remove-Item -Force $envFile
    function script:Invoke-WebRequest { throw [System.Net.WebException]::new('The remote server returned an error: (404) Not Found.') }
    $out = (Get-ReleaseBinary -Dir $dir 6>&1 | Out-String -Width 4096)
    if ($out -notmatch 'no stable release' -or $out -notmatch '-Channel dev' -or $out -notmatch '-Binary') { Fail "a missing stable release says: $out" }
    # Without a channel given or saved, no stable release yet: the
    # development build, its channel kept; a given channel gets the error
    if ($out -notmatch 'installing the development build' -or $out -notmatch '-Update -Channel stable') { Fail "no stable release does not fall back to the development build: $out" }
    if ($script:UPDATE_GITHUB_BRANCH -ne 'dev' -or $script:UPDATE_GITHUB_BRANCH_GIVEN -ne 'dev') { Fail "the fallback does not keep the development channel: $($script:UPDATE_GITHUB_BRANCH)" }
    $script:UPDATE_GITHUB_BRANCH_GIVEN = 'main'
    $script:UPDATE_GITHUB_BRANCH = 'main'
    $out = (Get-ReleaseBinary -Dir $dir 6>&1 | Out-String -Width 4096)
    if ($out -match 'installing the development build' -or $out -notmatch 'no stable release yet') { Fail "-Channel stable without a release says: $out" }
    $script:UPDATE_GITHUB_BRANCH_GIVEN = ''
    $script:UPDATE_GITHUB_BRANCH = 'main'
    function script:Invoke-WebRequest { throw [System.Net.WebException]::new('The remote name could not be resolved: github.com') }
    $out = (Get-ReleaseBinary -Dir $dir 6>&1 | Out-String -Width 4096)
    if ($out -notmatch 'Could not reach' -or $out -notmatch '-Binary') { Fail "a download without network says: $out" }
    Remove-Item function:Invoke-WebRequest -ErrorAction SilentlyContinue
    $script:UPDATE_GITHUB_BRANCH = 'main'
    $env:UPDATE_GITHUB_BRANCH = $saved
    Remove-Item -Recurse -Force $dir
}

# L69: the host clients get becomes PANEL_PUBLIC_HOST, so the HTTP port
# redirects to HTTPS there; a value the admin set stays.
function Test-PanelPublicHost {
    $dir = New-TestInstall
    $envFile = Join-Path $script:CONSOLE_PATH '.env'
    Set-Content -Path $envFile -Value @('SESSION_SECRET=kept', 'PANEL_PUBLIC_HOST=')
    Setup-Services | Out-Null
    if ((Get-ConsoleEnvValue -Key 'PANEL_PUBLIC_HOST') -ne '203.0.113.7') { Fail "the install set PANEL_PUBLIC_HOST to '$(Get-ConsoleEnvValue -Key 'PANEL_PUBLIC_HOST')'" }
    Set-PanelPublicHost -Address '198.51.100.1:21117'
    if ((Get-ConsoleEnvValue -Key 'PANEL_PUBLIC_HOST') -ne '203.0.113.7') { Fail 'a second address replaced PANEL_PUBLIC_HOST' }
    Set-Content -Path $envFile -Value @('SESSION_SECRET=kept')
    Set-PanelPublicHost -Address 'desk.example.org:21117'
    if ((Get-ConsoleEnvValue -Key 'PANEL_PUBLIC_HOST') -ne 'desk.example.org') { Fail "PANEL_PUBLIC_HOST from host:port is '$(Get-ConsoleEnvValue -Key 'PANEL_PUBLIC_HOST')'" }
    Remove-Item -Recurse -Force $dir
}

# L151: -Help documents every parameter; a second -Auto run updates; the
# password shows only when the install made it and leaves .env after the
# first start; the install keeps a copy of the script for updates; the
# summary names the log and the commands for later.
function Test-WindowsFirstHour {
    $documented = @([regex]::Matches((Get-Content $installer -Raw), '(?m)^\.PARAMETER\s+(\w+)') | ForEach-Object { $_.Groups[1].Value })
    foreach ($p in $ast.ParamBlock.Parameters) {
        $name = $p.Name.VariablePath.UserPath
        if ($name -in 'NodeJs', 'Flask') { continue }
        if ($documented -notcontains $name) { Fail "the help does not document -$name" }
    }
    if (-not ($ast.ParamBlock.Parameters | Where-Object { $_.Name.VariablePath.UserPath -eq 'Help' })) { Fail 'there is no -Help' }

    # The real binary path again (Use-FakeServerBinary replaced it)
    foreach ($fn in $ast.FindAll({ $args[0] -is [System.Management.Automation.Language.FunctionDefinitionAst] -and $args[0].Name -eq 'Get-ServerBinaryPath' }, $false)) {
        . ([scriptblock]::Create(($fn.Extent.Text -replace '^function ', 'function script:')))
    }
    $dir = New-TestInstall
    $script:UNINSTALL_MODE = $false
    $script:MINIMAL_MODE = $false
    if ((Get-AutoAction) -ne 'install') { Fail "a machine without BetterDesk gets $(Get-AutoAction)" }
    Setup-Services | Out-Null
    Set-Content -Path (Join-Path $script:RUSTDESK_PATH 'betterdesk-server.exe') -Value 'exe'
    $envFile = Join-Path $script:CONSOLE_PATH '.env'
    Set-Content -Path $envFile -Value @('DEFAULT_ADMIN_PASSWORD=First-Pass-1', 'INIT_ADMIN_PASS=First-Pass-1', 'SESSION_SECRET=kept', 'DB_TYPE=sqlite')
    Copy-Item $envFile "$envFile.pre-go"
    if ((Get-AutoAction) -ne 'update') { Fail "a second run on an install gets $(Get-AutoAction)" }
    $script:USE_POSTGRESQL = $true
    if ((Get-AutoAction) -ne 'install') { Fail "-PostgreSQL on a SQLite install gets $(Get-AutoAction), not the move" }
    $script:USE_POSTGRESQL = $false

    $env:ADMIN_PASSWORD = $null
    $script:FRESH_CONSOLE_ENV = $false
    $out = (Create-AdminUser 6>&1 | Out-String)
    if ($out -match 'First-Pass-1') { Fail 'a run that kept .env showed the admin password again' }
    $script:FRESH_CONSOLE_ENV = $true
    $out = (Create-AdminUser 6>&1 | Out-String)
    if ($out -notmatch 'First-Pass-1') { Fail 'a fresh install did not show the admin password' }
    Remove-FirstAdminPassword
    foreach ($f in $envFile, "$envFile.pre-go") {
        if ((Get-Content $f -Raw) -match 'First-Pass-1') { Fail "$(Split-Path -Leaf $f) keeps the admin password after the first start" }
    }
    if ((Get-ConsoleEnvValue -Key 'SESSION_SECRET') -ne 'kept') { Fail 'forgetting the password lost a line of .env' }

    $script:SELF_PATH = $installer
    Install-ManagerScript 6> $null
    $copy = Join-Path $script:RUSTDESK_PATH 'betterdesk.ps1'
    if (-not (Test-Path $copy) -or (Get-FileHash $copy).Hash -ne (Get-FileHash $installer).Hash) { Fail "the install keeps no copy of the script at $copy" }

    Set-Content -Path (Join-Path $script:RUSTDESK_PATH 'id_ed25519.pub') -Value 'AAAABBBBCCCCDDDDEEEEFFFFGGGGHHHHIIIIJJJJ='
    Set-ConsoleEnvValue -Key 'PANEL_PUBLIC_HOST' -Value 'desk.example.org'
    $out = (Show-InstallSummary 6>&1 | Out-String)
    Set-ConsoleEnvValue -Key 'API_PORT' -Value '21121'
    $out = (Show-InstallSummary 6>&1 | Out-String)
    if (-not $out.Contains('http://desk.example.org:21121')) { Fail "the summary gives clients another API address (L76): $out" }
    foreach ($want in 'desk.example.org', 'AAAABBBBCCCCDDDDEEEEFFFFGGGGHHHHIIIIJJJJ=', (Get-ServerLogPath), $envFile, '-Update', '-Uninstall', 'admin') {
        if (-not $out.Contains($want)) { Fail "the summary does not name $want" }
    }
    $script:FRESH_CONSOLE_ENV = $false
    Remove-Item -Recurse -Force $dir
}

# L155: the server's updater is told the repository of the releases, so an
# installed binary with an older default does not ask upstream; one named
# in .env stays.
function Test-UpdateAsksTheFork {
    $dir = New-TestInstall
    $script:UPDATE_GITHUB_OWNER, $script:UPDATE_GITHUB_REPO = 'trybetterdesk', 'betterdesk'
    $script:UPDATE_GITHUB_OWNER_GIVEN, $script:UPDATE_GITHUB_REPO_GIVEN = '', ''
    function script:Invoke-ServerCommand { param([string[]]$Arguments, $InputText, $Binary) $script:seenRepo = "$env:UPDATE_GITHUB_OWNER/$env:UPDATE_GITHUB_REPO"; return 1 }
    function script:Do-BackupSilent { }
    # The installed binary checks release signatures, so its updater runs
    function Test-BinaryVerifiesReleases { param($Path) $true }
    $script:BINARY_FILE = ''
    $script:FROM_SOURCE = $false
    $envFile = Join-Path $script:CONSOLE_PATH '.env'
    Set-Content -Path $envFile -Value @('SESSION_SECRET=kept')
    Update-GoInstall *> $null
    if ($script:seenRepo -ne 'trybetterdesk/betterdesk') { Fail "betterdesk-server update ran for $($script:seenRepo)" }
    Set-Content -Path $envFile -Value @('UPDATE_GITHUB_OWNER=acme', 'UPDATE_GITHUB_REPO=desk')
    Update-GoInstall *> $null
    if ($script:seenRepo -ne 'acme/desk') { Fail "the repository of .env lost to the default: $($script:seenRepo)" }
    if ($env:UPDATE_GITHUB_OWNER) { Fail 'the update left UPDATE_GITHUB_OWNER in the environment' }
    Remove-Item -Recurse -Force $dir
}

# L67: both installers open the same ports: RustDesk's (21115 to 21117 TCP,
# 21116 UDP), the client API port, 21122 while CDAP is on, and the
# console's ports unless TLS ends at a reverse proxy.
function Test-FirewallPorts {
    $dir = New-TestInstall
    $envFile = Join-Path $script:CONSOLE_PATH '.env'
    Set-Content -Path $envFile -Value @('API_PORT=21121', 'CONSOLE_PORT=5000', 'HTTPS_ENABLED=true', 'HTTPS_PORT=5443', 'CDAP_ENABLED=Y', 'TRUST_PROXY=false')
    $got = (Get-FirewallPorts | ForEach-Object { "$($_.Port)/$($_.Proto)" }) -join ' '
    if ($got -ne '21115/TCP 21116/TCP 21116/UDP 21117/TCP 21121/TCP 21122/TCP 5000/TCP 5443/TCP') { Fail "the firewall ports: $got" }
    Set-Content -Path $envFile -Value @('API_PORT=21121', 'CONSOLE_PORT=5000', 'HTTPS_ENABLED=false', 'CDAP_ENABLED=N', 'TRUST_PROXY=Y')
    $got = (Get-FirewallPorts | ForEach-Object { "$($_.Port)/$($_.Proto)" }) -join ' '
    if ($got -ne '21115/TCP 21116/TCP 21116/UDP 21117/TCP 21121/TCP') { Fail "the firewall ports behind a reverse proxy without CDAP: $got" }
    Remove-Item -Recurse -Force $dir
}

# L66: the server binary makes the certificate, so Windows needs no
# OpenSSL; a certificate that could not be written as PEM files leaves the
# console on HTTP instead of turning HTTPS on without files.
function Test-CertificateFromTheBinary {
    $dir = New-TestInstall
    $envFile = Join-Path $script:CONSOLE_PATH '.env'
    Set-Content -Path $envFile -Value @('HTTPS_ENABLED=false', 'SSL_CERT_PATH=', 'SSL_KEY_PATH=', 'RUSTDESK_API_TLS=auto')
    function script:Get-PublicIP { '203.0.113.7' }
    function script:Get-LocalIP { '192.168.1.5' }
    function script:Test-BinaryHasCertCommand { $true }
    function script:Invoke-ServerCommand {
        param([string[]]$Arguments, $InputText, $Binary)
        $script:certArgs = $Arguments -join ' '
        $out = $Arguments[2]
        Set-Content -Path (Join-Path $out 'betterdesk.crt') -Value 'crt'
        Set-Content -Path (Join-Path $out 'betterdesk.key') -Value 'key'
        return 0
    }
    if (-not (Generate-SSLCertificates 6> $null)) { Fail 'the cert command made no certificate' }
    if ($script:certArgs -notmatch '--host 203\.0\.113\.7' -or $script:certArgs -notmatch '--host 192\.168\.1\.5') { Fail "the cert command was given: $($script:certArgs)" }
    if ((Get-ConsoleEnvValue -Key 'HTTPS_ENABLED') -ne 'true' -or (Get-ConsoleEnvValue -Key 'RUSTDESK_API_TLS') -ne 'false') { Fail "after the certificate .env has: $(Get-Content $envFile)" }

    Remove-Item -Recurse -Force (Join-Path $script:RUSTDESK_PATH 'ssl')
    Set-Content -Path $envFile -Value @('HTTPS_ENABLED=false', 'SSL_CERT_PATH=', 'SSL_KEY_PATH=')
    function script:Test-BinaryHasCertCommand { $false }
    function script:New-SelfSignedCertificate { [pscustomobject]@{ Thumbprint = 'AB' } }
    function script:Export-Certificate { }
    function script:Export-PfxCertificate { }
    function script:Get-Command { param($Name, $ErrorAction) if ($Name -eq 'openssl') { return $null }; Microsoft.PowerShell.Core\Get-Command $Name -ErrorAction SilentlyContinue }
    $ok = Generate-SSLCertificates 6> $null
    Remove-Item function:Get-Command, function:New-SelfSignedCertificate, function:Export-Certificate, function:Export-PfxCertificate -ErrorAction SilentlyContinue
    if ($ok -or (Get-ConsoleEnvValue -Key 'HTTPS_ENABLED') -eq 'true') { Fail "HTTPS was turned on without PEM files: $(Get-Content $envFile)" }
    Remove-Item -Recurse -Force $dir
}

# L161: passwords and secrets come from the system's cryptographic
# generator, with repeats allowed, never from Get-Random.
function Test-SecretsAreRandom {
    function script:Get-Random { throw 'Get-Random was used for a secret' }
    $a = Generate-RandomPassword -Length 64
    $b = Generate-RandomPassword -Length 64
    Remove-Item function:Get-Random -ErrorAction SilentlyContinue
    if ($a.Length -ne 64 -or $a -notmatch '^[A-Za-z0-9]+$') { Fail "a secret of 64 is '$a'" }
    if ($a -eq $b) { Fail 'two secrets are the same' }
    $repeats = @($a.ToCharArray() | Group-Object -CaseSensitive | Where-Object { $_.Count -gt 1 }).Count
    if ($repeats -eq 0) { Fail 'a secret of 64 has no character twice, which a shuffle of the alphabet gives' }
    if ((Get-Content $installer -Raw) -match 'Get-Random -Count') { Fail 'the installer still draws secrets with Get-Random -Count' }
}

# L70: for a binary that reads DATABASE_URL the installer names the
# database there alone; an older binary gets DB_TYPE and DB_URL as it reads
# them, and older .env files are read as the server reads them.
function Test-DatabaseUrlOnly {
    $dir = New-TestInstall
    $envFile = Join-Path $script:CONSOLE_PATH '.env'
    function Get-DatabaseLines { (@(Get-Content $envFile | Where-Object { $_ -match '^(DATABASE_URL|DB_URL|DB_TYPE|DB_PATH)=' }) -join ' ') }

    # The binary says by its warning text whether it reads DATABASE_URL
    $exe = Join-Path $dir 'probe.exe'
    Set-Content -Path $exe -Value 'upgrade-env --from FILE ... Remove DB_TYPE to use DATABASE_URL ...'
    if (-not (Test-BinaryReadsDatabaseUrl -Path $exe)) { Fail 'a binary with the DATABASE_URL warning is taken for an older one' }
    Set-Content -Path $exe -Value 'upgrade-env --from FILE ...'
    if (Test-BinaryReadsDatabaseUrl -Path $exe) { Fail 'an older binary is taken for one that reads DATABASE_URL' }

    function Get-ServerBinaryPath { 'fake-server' }
    function Test-BinaryHasSubcommands { param($Path) $true }
    function Test-BinaryReadsDatabaseUrl { param($Path) $true }
    function fake-server {
        if ($args[0] -eq 'env' -and $args[1] -eq 'merge') {
            $target = $args[3]
            if ($args -contains '--fresh') { Set-Content -Path $target -Value @() }
            foreach ($pair in @(@('DEFAULT_ADMIN_PASSWORD', $env:BD_SUBST_DEFAULT_ADMIN_PASSWORD), @('DATABASE_URL', $env:BD_SUBST_DATABASE_URL))) {
                if (-not (Get-Content $target | Where-Object { $_ -like "$($pair[0])=*" })) { Add-Content -Path $target -Value "$($pair[0])=$($pair[1])" }
            }
            Write-Output '{"success":true}'
        }
        $global:LASTEXITCODE = 0
    }
    $sqlite = Join-Path $script:RUSTDESK_PATH 'db_v2.sqlite3'
    $uri = 'postgres://bd:p&w|d@127.0.0.1:5432/bd?sslmode=disable'
    $env:ADMIN_PASSWORD = 'Database-Url-Pass-1'
    $script:USE_POSTGRESQL = $false
    $script:POSTGRESQL_URI = ''
    Merge-ConsoleEnv -FreshInstall $true *> $null
    if ((Get-DatabaseLines) -ne "DATABASE_URL=$sqlite") { Fail "a fresh SQLite install names its database as: $(Get-DatabaseLines)" }
    Remove-Item -Force $envFile
    $script:USE_POSTGRESQL = $true
    $script:POSTGRESQL_URI = $uri
    Merge-ConsoleEnv -FreshInstall $true *> $null
    if ((Get-DatabaseLines) -ne "DATABASE_URL=$uri") { Fail "a fresh PostgreSQL install names its database as: $(Get-DatabaseLines)" }
    $env:ADMIN_PASSWORD = $null

    # A move between databases leaves DATABASE_URL alone, also in an older file
    Set-Content -Path $envFile -Value @('SESSION_SECRET=kept', 'DB_TYPE=sqlite', 'DB_PATH=C:\old\db.sqlite3', 'DATABASE_URL=', 'DB_URL=C:\old\db.sqlite3')
    Use-DatabaseInEnv -Type postgres -Address $uri
    if ((Get-DatabaseLines) -ne "DATABASE_URL=$uri") { Fail "the move to PostgreSQL names the database as: $(Get-DatabaseLines)" }
    if ((Get-ConsoleEnvValue -Key 'SESSION_SECRET') -ne 'kept') { Fail 'the move lost another line of .env' }
    Use-SQLiteInstall 'C:\data\db_v2.sqlite3'
    if ((Get-DatabaseLines) -ne 'DATABASE_URL=C:\data\db_v2.sqlite3') { Fail "the move back to SQLite names the database as: $(Get-DatabaseLines)" }

    # An update keeps an older file as it is and keeps it on PostgreSQL; a
    # file of this installer names PostgreSQL by its address alone
    Set-Content -Path $envFile -Value @('DB_TYPE=postgres', "DATABASE_URL=$uri", "DB_URL=$uri")
    $script:USE_POSTGRESQL = $false
    $script:POSTGRESQL_URI = ''
    Preserve-DatabaseConfig *> $null
    if (-not $script:USE_POSTGRESQL -or $script:POSTGRESQL_URI -ne $uri) { Fail 'an older PostgreSQL install is not seen as one' }
    Merge-ConsoleEnv -FreshInstall $false *> $null
    if ((Get-DatabaseLines) -ne "DB_TYPE=postgres DATABASE_URL=$uri DB_URL=$uri") { Fail "an update changed an older file's database: $(Get-DatabaseLines)" }
    Set-Content -Path $envFile -Value @("DATABASE_URL=$uri")
    $script:USE_POSTGRESQL = $false
    Preserve-DatabaseConfig *> $null
    if (-not $script:USE_POSTGRESQL -or $script:POSTGRESQL_URI -ne $uri) { Fail 'DATABASE_URL alone is not seen as PostgreSQL' }

    # Read as the server reads them (envvar.DatabaseFrom)
    foreach ($case in @(
            @('postgres postgres://a@h/d', @('DB_TYPE=postgres', 'DATABASE_URL=postgres://a@h/d', 'DB_URL=postgres://a@h/d')),
            @('sqlite C:\old\db', @('DB_TYPE=sqlite', 'DATABASE_URL=', 'DB_URL=C:\old\db')),
            @('sqlite C:\old\db', @('DB_TYPE=sqlite', 'DATABASE_URL=postgres://a@h/d', 'DB_URL=C:\old\db')),
            @('sqlite ', @('DB_TYPE=SQLite', 'DATABASE_URL=postgres://a@h/d', 'DB_URL=postgresql://a@h/d')),
            @('postgres postgresql://a@h/d', @('DATABASE_URL=postgresql://a@h/d')),
            @('postgres postgres://a@h/d', @('DATABASE_URL=', 'DB_URL=postgres://a@h/d')),
            @('sqlite C:\srv\db.sqlite3', @('DATABASE_URL=sqlite://C:\srv\db.sqlite3', 'DB_URL=C:\other')),
            @('sqlite ', @('SESSION_SECRET=x')))) {
        Set-Content -Path $envFile -Value $case[1]
        $got = "$(Get-EnvDatabaseType) $(Get-EnvDatabaseUrl)"
        if ($got -ne $case[0]) { Fail "$($case[1] -join ' ') is read as '$got', want '$($case[0])'" }
    }
    $script:USE_POSTGRESQL = $false
    $script:POSTGRESQL_URI = ''
    Remove-Item -Recurse -Force $dir
}

# A152: a download is checked against the release's signed manifest by the
# installed betterdesk-server (verify-release). A first install has no tool
# for the signature on Windows: it says plainly that the signature is
# checked from the second install on, and keeps the SHA-256 check. An
# update of a binary older than the release key goes through this download,
# not the old updater.
function Test-ReleaseSignatureOnWindows {
    $dir = New-TestInstall
    $asset = 'betterdesk-server-windows-amd64.exe'
    $rel = Join-Path $dir 'release'
    $stage = Join-Path $dir 'stage'
    New-Item -ItemType Directory -Path $rel, $stage -Force | Out-Null
    Set-Content -Path (Join-Path $rel $asset) -Value 'the new server'
    $sum = (Get-FileHash -Path (Join-Path $rel $asset) -Algorithm SHA256).Hash.ToLower()
    Set-Content -Path (Join-Path $rel 'SHA256SUMS') -Value "$sum  $asset"
    Set-Content -Path (Join-Path $rel 'manifest.json') -Value @('{', '  "owner": "trybetterdesk",', '  "branch": "main",', '  "files": []', '}')
    Set-Content -Path (Join-Path $rel 'manifest.sig') -Value 'c2lnbmF0dXJl'
    function Invoke-WebRequest {
        param($Uri, $OutFile, [switch]$UseBasicParsing, $ErrorAction)
        $name = ($Uri -split '/')[-1]
        $script:downloads += $name
        $from = Join-Path $rel $name
        if (-not (Test-Path $from)) { throw [System.Net.WebException]::new('The remote server returned an error: (404) Not Found.') }
        Copy-Item -Path $from -Destination $OutFile -Force
    }
    $script:UPDATE_GITHUB_BRANCH_GIVEN = ''
    $script:UPDATE_GITHUB_BRANCH = 'main'
    $script:SKIP_VERIFY = $false

    # The installed binary says by its help whether it checks signatures
    $probe = Join-Path $dir 'probe.exe'
    Set-Content -Path $probe -Value 'upgrade-env --from FILE ... verify-release --manifest FILE --signature FILE ...'
    if (-not (Test-BinaryVerifiesReleases -Path $probe)) { Fail 'a binary with verify-release is taken for an older one' }
    Set-Content -Path $probe -Value 'upgrade-env --from FILE ...'
    if (Test-BinaryVerifiesReleases -Path $probe) { Fail 'an older binary is taken for one that checks signatures' }

    # A first install: no installed binary
    $script:downloads = @()
    $out = (Get-ReleaseBinary -Dir $stage 6>&1 | Out-String -Width 4096)
    if (-not (Test-Path (Join-Path $stage 'betterdesk-server.exe'))) { Fail "a first install with a matching SHA-256 installed nothing: $out" }
    if ($out -notmatch 'checked from the second install on') { Fail "a first install does not say when the signature is checked: $out" }
    if ($script:downloads -contains 'manifest.sig') { Fail 'a first install fetched a signature it cannot check' }

    # An installed binary with verify-release checks the download
    function Get-ServerBinaryPath { 'fake-verify' }
    function Test-BinaryVerifiesReleases { param($Path) $Path -eq 'fake-verify' }
    function fake-verify {
        $script:verifyArgs = @($args | ForEach-Object { "$_" })
        if ($script:refuse) { Write-Output 'betterdesk-server verify-release: the signature of the manifest does not verify against the release key'; $global:LASTEXITCODE = 1; return }
        Write-Output "verified: betterdesk-server 3.6.0 (0123456), $($args[6])"
        $global:LASTEXITCODE = 0
    }
    $script:refuse = $false
    Remove-Item -Recurse -Force $stage; New-Item -ItemType Directory -Path $stage | Out-Null
    $out = (Get-ReleaseBinary -Dir $stage 6>&1 | Out-String -Width 4096)
    if (-not (Test-Path (Join-Path $stage 'betterdesk-server.exe'))) { Fail "a release the installed binary checked was not installed: $out" }
    $want = "verify-release --manifest $(Join-Path $stage 'manifest.json') --signature $(Join-Path $stage 'manifest.sig') --name $asset $(Join-Path $stage $asset)"
    if (($script:verifyArgs -join ' ') -ne $want) { Fail "verify-release ran as: $($script:verifyArgs -join ' ')" }
    if ($out -notmatch 'checked by the installed betterdesk-server') { Fail "the download does not say who checked the signature: $out" }
    $script:refuse = $true
    Remove-Item -Recurse -Force $stage; New-Item -ItemType Directory -Path $stage | Out-Null
    $out = (Get-ReleaseBinary -Dir $stage 6>&1 | Out-String -Width 4096)
    if (Test-Path (Join-Path $stage 'betterdesk-server.exe')) { Fail 'a release the installed binary refused was installed' }
    if ($out -notmatch 'does not verify' -or $out -notmatch '-SkipVerify') { Fail "the refusal does not say why and what to do: $out" }
    $script:refuse = $false
    $script:UPDATE_GITHUB_BRANCH = 'dev'
    Remove-Item -Recurse -Force $stage; New-Item -ItemType Directory -Path $stage | Out-Null
    $out = (Get-ReleaseBinary -Dir $stage 6>&1 | Out-String -Width 4096)
    if (Test-Path (Join-Path $stage 'betterdesk-server.exe')) { Fail "a stable release was installed for the development channel: $out" }
    $script:UPDATE_GITHUB_BRANCH = 'main'
    Remove-Item -Force (Join-Path $rel 'manifest.sig')
    Remove-Item -Recurse -Force $stage; New-Item -ItemType Directory -Path $stage | Out-Null
    $out = (Get-ReleaseBinary -Dir $stage 6>&1 | Out-String -Width 4096)
    if (Test-Path (Join-Path $stage 'betterdesk-server.exe')) { Fail 'a release without a signature was installed' }
    if ($out -notmatch 'no signed manifest') { Fail "a release without a signature says: $out" }

    # An update of an older binary does not run its updater
    function Test-BinaryVerifiesReleases { param($Path) $false }
    function Do-BackupSilent { }
    function Invoke-ServerCommand { param([string[]]$Arguments, $InputText, $Binary) $script:updaterRan = $true; return 1 }
    function Get-StagedServerBinary { $script:ownDownload = $true; $script:STAGED_SERVER_DIR = Join-Path $dir 'staged'; return $false }
    $script:updaterRan = $false
    $script:ownDownload = $false
    $script:BINARY_FILE = ''
    $script:FROM_SOURCE = $false
    Update-GoInstall *> $null
    if ($script:updaterRan) { Fail "the older binary's updater ran" }
    if (-not $script:ownDownload) { Fail "the update of an older binary did not download and check the release itself" }
    Remove-Item -Recurse -Force $dir
}

# L210 (A183): while the repository has no stable release, the stable
# channel installs the newest pre-release that is not the rolling dev one,
# also with -Channel stable, and says which release it took; the
# development build only when there is no pre-release either.
function Test-NoStableReleaseTakesTheNewestPrerelease {
    $dir = New-TestInstall
    $asset = 'betterdesk-server-windows-amd64.exe'
    $rel = Join-Path $dir 'rc2'
    New-Item -ItemType Directory -Path $rel -Force | Out-Null
    Set-Content -Path (Join-Path $rel $asset) -Value 'candidate'
    $sum = (Get-FileHash -Path (Join-Path $rel $asset) -Algorithm SHA256).Hash.ToLower()
    Set-Content -Path (Join-Path $rel 'SHA256SUMS') -Value "$sum  $asset"
    $script:releases = @(
        [pscustomobject]@{ tag_name = 'dev'; draft = $false; prerelease = $true },
        [pscustomobject]@{ tag_name = 'v3.7.0-rc.1'; draft = $true; prerelease = $true },
        [pscustomobject]@{ tag_name = 'v3.6.0-rc.2'; draft = $false; prerelease = $true },
        [pscustomobject]@{ tag_name = 'v3.6.0-rc.1'; draft = $false; prerelease = $true })
    # L254: GitHub's API says there is no stable release (404), and the
    # download from releases/latest fails the way Windows PowerShell 5.1
    # reports it on this route: a closed connection without a status, which
    # the install took for a network failure
    $script:latest = $null
    function Invoke-RestMethod {
        param($Uri, $Headers, [switch]$UseBasicParsing, $ErrorAction)
        if ($Uri -eq 'https://api.github.com/repos/trybetterdesk/betterdesk/releases/latest') {
            if ($script:latest) { return $script:latest }
            throw [System.Net.WebException]::new('The remote server returned an error: (404) Not Found.')
        }
        if ($Uri -ne 'https://api.github.com/repos/trybetterdesk/betterdesk/releases?per_page=50') { throw "unexpected $Uri" }
        return $script:releases
    }
    function Invoke-WebRequest {
        param($Uri, $OutFile, [switch]$UseBasicParsing, $ErrorAction)
        if ($Uri -like 'https://github.com/trybetterdesk/betterdesk/releases/latest/*') { throw [System.Net.WebException]::new('The underlying connection was closed: The connection was closed unexpectedly.') }
        if ($Uri -notlike 'https://github.com/trybetterdesk/betterdesk/releases/download/v3.6.0-rc.2/*' -and $Uri -notlike 'https://github.com/trybetterdesk/betterdesk/releases/download/v3.6.0/*') { throw [System.Net.WebException]::new('The remote server returned an error: (404) Not Found.') }
        Copy-Item -Path (Join-Path $rel (($Uri -split '/')[-1])) -Destination $OutFile -Force
    }
    $script:UPDATE_GITHUB_OWNER, $script:UPDATE_GITHUB_REPO = 'trybetterdesk', 'betterdesk'
    $script:SKIP_VERIFY = $true
    foreach ($given in @('', 'main')) {
        $stage = Join-Path $dir "stage-$given"
        New-Item -ItemType Directory -Path $stage -Force | Out-Null
        $script:UPDATE_GITHUB_BRANCH_GIVEN = $given
        $script:UPDATE_GITHUB_BRANCH = 'main'
        $out = (Get-ReleaseBinary -Dir $stage 6>&1 | Out-String -Width 4096)
        if (-not (Test-Path (Join-Path $stage 'betterdesk-server.exe'))) { Fail "the newest pre-release was not installed (given '$given'): $out" }
        if ($out -notmatch 'no stable release yet: installing the newest pre-release v3\.6\.0-rc\.2') { Fail "the install does not say which release it took: $out" }
        if ($script:UPDATE_GITHUB_BRANCH -ne 'main') { Fail "the install left the stable channel for $($script:UPDATE_GITHUB_BRANCH)" }
    }
    # Without a pre-release: the development build
    $script:releases = @([pscustomobject]@{ tag_name = 'dev'; draft = $false; prerelease = $true })
    $script:UPDATE_GITHUB_BRANCH_GIVEN = ''
    $script:UPDATE_GITHUB_BRANCH = 'main'
    $out = (Get-ReleaseBinary -Dir (Join-Path $dir 'stage-') 6>&1 | Out-String -Width 4096)
    if ($out -notmatch 'no stable release or pre-release yet: installing the development build') { Fail "without a pre-release the install does not take the development build: $out" }
    # With the stable channel given there is no development build to fall
    # back to: the install says the repository has no release
    $script:UPDATE_GITHUB_BRANCH_GIVEN = 'main'
    $script:UPDATE_GITHUB_BRANCH = 'main'
    $out = (Get-ReleaseBinary -Dir (Join-Path $dir 'stage-none') 6>&1 | Out-String -Width 4096)
    if ($out -notmatch 'has no stable release or pre-release yet' -or $out -match 'Could not reach') { Fail "a repository without releases is reported as: $out" }
    # A stable release: its files by its tag
    $script:latest = [pscustomobject]@{ tag_name = 'v3.6.0'; draft = $false; prerelease = $false }
    $stage = Join-Path $dir 'stage-stable'
    New-Item -ItemType Directory -Path $stage -Force | Out-Null
    $out = (Get-ReleaseBinary -Dir $stage 6>&1 | Out-String -Width 4096)
    if (-not (Test-Path (Join-Path $stage 'betterdesk-server.exe')) -or $out -notmatch 'releases/download/v3\.6\.0') { Fail "the stable release was not installed by its tag: $out" }
    $script:latest = $null
    $script:UPDATE_GITHUB_BRANCH_GIVEN = ''
    $script:UPDATE_GITHUB_BRANCH = 'main'
    $env:UPDATE_GITHUB_BRANCH = $null
    $script:SKIP_VERIFY = $false
    Remove-Item -Recurse -Force $dir
}

# L71: the installed binary's migrate command moves the database; the
# separate tool of an older release is the fallback.
function Test-MigrateCommand {
    $dir = New-TestInstall
    $probe = Join-Path $dir 'probe.exe'
    Set-Content -Path $probe -Value 'upgrade-env --from FILE ... migrate -mode sqlite2pg|pg2sqlite|rust2go ...'
    if (-not (Test-BinaryMigrates -Path $probe)) { Fail 'a binary with the migrate command is taken for an older one' }
    Set-Content -Path $probe -Value 'upgrade-env --from FILE ...'
    if (Test-BinaryMigrates -Path $probe) { Fail 'an older binary is taken for one with the migrate command' }

    function Get-ServerBinaryPath { 'fake-server' }
    function Test-BinaryMigrates { param($Path) $true }
    function fake-server { $script:serverCalls += ($args -join ' '); $global:LASTEXITCODE = 0 }
    $script:serverCalls = @()
    $src = Join-Path $script:RUSTDESK_PATH 'db_v2.sqlite3'
    Set-Content -Path $src -Value 'db'
    Set-Content -Path (Join-Path $script:CONSOLE_PATH '.env') -Value @('SESSION_SECRET=kept')
    $uri = 'postgres://u:p@h/db?sslmode=disable'
    $script:USE_POSTGRESQL = $true
    $script:POSTGRESQL_URI = $uri
    $script:AUTO_MODE = $true
    if ((Get-MigrateBin) -ne 'Invoke-ServerMigrate') { Fail "the migrate tool is $(Get-MigrateBin), not the server's command" }
    Migrate-SQLiteToPostgreSQL *> $null
    if ($script:serverCalls -notcontains "migrate -mode sqlite2pg -src $src -dst $uri") { Fail "the switch ran the server as: $($script:serverCalls -join '; ')" }
    if ($script:serverCalls -notcontains "migrate -mode backup -src $src") { Fail "the switch made no backup with the migrate command: $($script:serverCalls -join '; ')" }
    if ((Get-ConsoleEnvValue -Key 'DATABASE_URL') -ne $uri) { Fail "after the move .env does not point at PostgreSQL: $(Get-Content (Join-Path $script:CONSOLE_PATH '.env'))" }
    $script:USE_POSTGRESQL = $false
    $script:POSTGRESQL_URI = ''
    Remove-Item -Recurse -Force $dir
}

# L73: the installer's backup is the server's own (database, PostgreSQL
# too, settings, the console's data) plus the keys; without the backup
# command, or when it fails, the SQLite file is copied as before.
function Test-BackupUsesTheServersBackup {
    $dir = New-TestInstall
    $probe = Join-Path $dir 'probe.exe'
    Set-Content -Path $probe -Value 'upgrade-env --from FILE ... backup [--out DIR] [--db DSN] ...'
    if (-not (Test-BinaryBacksUp -Path $probe)) { Fail 'a binary with the backup command is taken for an older one' }
    Set-Content -Path $probe -Value 'upgrade-env --from FILE ...'
    if (Test-BinaryBacksUp -Path $probe) { Fail 'an older binary is taken for one with the backup command' }

    # The installer's own Do-BackupSilent (an earlier check stands one in)
    $real = $ast.FindAll({ $args[0] -is [System.Management.Automation.Language.FunctionDefinitionAst] -and $args[0].Name -eq 'Do-BackupSilent' }, $false) | Select-Object -First 1
    . ([scriptblock]::Create($real.Extent.Text))
    $script:BACKUP_DIR = Join-Path $dir 'backups'
    $script:DB_PATH = Join-Path $script:RUSTDESK_PATH 'db_v2.sqlite3'
    Set-Content -Path $script:DB_PATH -Value 'sqlite file'
    Set-Content -Path (Join-Path $script:RUSTDESK_PATH 'id_ed25519') -Value 'private key'
    function Get-ZipNames([string]$Path) {
        Add-Type -AssemblyName System.IO.Compression.FileSystem
        $z = [System.IO.Compression.ZipFile]::OpenRead($Path)
        try { return @($z.Entries | ForEach-Object { $_.Name }) } finally { $z.Dispose() }
    }
    function Test-BinaryBacksUp { param($Path) $true }
    function Invoke-ServerCommand {
        param([string[]]$Arguments, $InputText, $Binary)
        $script:backupArgs = $Arguments
        if ($script:backupFails) { return 1 }
        Set-Content -Path (Join-Path $Arguments[2] 'betterdesk-full-backup.zip') -Value 'archive'
        return 0
    }
    $script:backupFails = $false
    Do-BackupSilent *> $null
    $zip = @(Get-ChildItem -Path $script:BACKUP_DIR -Filter '*.zip')[0]
    if (-not $zip) { Fail 'no backup was made' }
    else {
        if ($script:backupArgs[0] -ne 'backup' -or $script:backupArgs[1] -ne '--out' -or $script:backupArgs[2] -notlike (Join-Path $script:BACKUP_DIR 'betterdesk_backup_*')) { Fail "the server's backup ran as: $($script:backupArgs -join ' ')" }
        $names = Get-ZipNames $zip.FullName
        if ($names -notcontains 'betterdesk-full-backup.zip' -or $names -notcontains 'id_ed25519') { Fail "the backup holds: $($names -join ', ')" }
        if ($names -contains 'db_v2.sqlite3') { Fail 'the backup copied the live SQLite file beside the server backup' }
        Remove-Item -Force $zip.FullName
    }
    $script:backupFails = $true
    $out = (Do-BackupSilent 6>&1 | Out-String -Width 4096)
    if ($out -notmatch 'betterdesk-server backup failed') { Fail "a failed server backup is not said: $out" }
    $zip = @(Get-ChildItem -Path $script:BACKUP_DIR -Filter '*.zip')[0]
    $names = Get-ZipNames $zip.FullName
    if ($names -notcontains 'db_v2.sqlite3') { Fail "without the server backup the database file is not in the backup: $($names -join ', ')" }
    Remove-Item -Recurse -Force $dir
}

# Get-HostLines BLOCK: what BLOCK wrote with Write-Host, as lines, and the
# console colours it set that are not the host's own
function Get-HostLines([scriptblock]$Block) {
    $text = New-Object System.Text.StringBuilder
    $colors = @()
    foreach ($record in @(& $Block 6>&1)) {
        $m = $record.MessageData
        if ($m -is [System.Management.Automation.HostInformationMessage]) {
            [void]$text.Append($m.Message)
            if (-not $m.NoNewLine) { [void]$text.Append("`n") }
            if ($m.ForegroundColor -ne $Host.UI.RawUI.ForegroundColor) { $colors += [string]$m.ForegroundColor }
            if ($m.BackgroundColor -ne $Host.UI.RawUI.BackgroundColor) { $colors += [string]$m.BackgroundColor }
        } else {
            [void]$text.Append("$m`n")
        }
    }
    [pscustomobject]@{ Lines = @($text.ToString().TrimEnd("`r", "`n") -split "`r?`n"); Colors = $colors }
}

# Get-Contrast R G B: the lowest contrast of the colour on black, on
# PowerShell's blue (#012456) and on white
function Get-Contrast([int]$R, [int]$G, [int]$B) {
    $lum = {
        param([int[]]$rgb)
        $c = foreach ($v in $rgb) { $v = $v / 255; if ($v -le 0.04045) { $v / 12.92 } else { [math]::Pow(($v + 0.055) / 1.055, 2.4) } }
        0.2126 * $c[0] + 0.7152 * $c[1] + 0.0722 * $c[2]
    }
    $l = & $lum @($R, $G, $B)
    $min = 99
    foreach ($bg in @(@(12, 12, 12), @(1, 36, 86), @(255, 255, 255))) {
        $lb = & $lum $bg
        $ratio = ([math]::Max($l, $lb) + 0.05) / ([math]::Min($l, $lb) + 0.05)
        if ($ratio -lt $min) { $min = $ratio }
    }
    return $min
}

# The header: the BetterDesk mark beside the name and the version in a
# console, one plain line when the output goes to a file or a pipe or
# NO_COLOR is set, and no escape codes then. No box characters, no line
# wider than 60 columns. The status helpers use no blue or magenta, and
# their 24-bit colours keep 3.5:1 on black, PowerShell's blue and white.
function Test-HeaderAndColours {
    function Clear-Host { }
    $dir = New-TestInstall
    $script:VERSION = [regex]::Match((Get-Content $installer -Raw), '(?m)^\$script:VERSION = "([^"]+)"').Groups[1].Value
    $plain = "BetterDesk Server installer $($script:VERSION)"
    $esc = [string][char]27
    $banned = 'Blue', 'DarkBlue', 'Magenta', 'DarkMagenta'
    # Box drawing (U+2500 to U+257F) and the half and full blocks of the mark
    $boxChars = '[' + [char]0x2500 + '-' + [char]0x257F + ']'
    $halfBlocks = '[' + [char]0x2580 + [char]0x2584 + [char]0x2588 + ']'
    $helpers = { Print-Success 's'; Print-Error 'e'; Print-Warning 'w'; Print-Info 'i'; Print-Step 'p' }
    $savedNoColor = $env:NO_COLOR

    # A file or a pipe: a run of its own with its output redirected
    $child = Join-Path $dir 'header.ps1'
    Set-Content -Path $child -Encoding ASCII -Value @(
        '$tokens = $null; $errors = $null'
        "`$ast = [System.Management.Automation.Language.Parser]::ParseFile('$installer', [ref]`$tokens, [ref]`$errors)"
        'foreach ($fn in $ast.FindAll({ $args[0] -is [System.Management.Automation.Language.FunctionDefinitionAst] }, $false)) { . ([scriptblock]::Create($fn.Extent.Text)) }'
        "`$script:VERSION = '$($script:VERSION)'; `$script:LOG_FILE = '$(Join-Path $dir 'child.log')'"
        'Print-Header; Print-Info "a note"'
    )
    $env:NO_COLOR = $null
    $out = @(& (Get-Process -Id $PID).Path -NoProfile -NonInteractive -ExecutionPolicy Bypass -File $child)
    if (($out -join "`n").Contains($esc)) { Fail "the header has escape codes in a pipe: $($out -join ' | ')" }
    if ($out[0] -ne $plain) { Fail "in a pipe the header is not one plain line: $($out -join ' | ')" }

    # A console, with and without NO_COLOR
    function Test-OutputRedirected { $false }
    $script:COLOR_MODE = $null
    if ((Get-ColorMode) -eq 'none') { Fail 'a console without NO_COLOR gets no colour' }
    $env:NO_COLOR = '1'
    $script:COLOR_MODE = $null
    $got = Get-HostLines { Print-Header; & $helpers }
    if (($got.Lines -join "`n").Contains($esc)) { Fail "with NO_COLOR there are escape codes: $($got.Lines -join ' | ')" }
    if ($got.Lines[0] -ne $plain) { Fail "with NO_COLOR the header is not one plain line: $($got.Lines -join ' | ')" }
    if ($got.Colors) { Fail "with NO_COLOR there are console colours: $($got.Colors -join ', ')" }
    $env:NO_COLOR = $savedNoColor

    foreach ($mode in 'vt', 'console') {
        $script:COLOR_MODE = $mode
        $got = Get-HostLines { Print-Header }
        $text = @($got.Lines | ForEach-Object { $_ -replace "$esc\[[0-9;]*m", '' })
        if (($text -join "`n") -notmatch "BetterDesk[\s\S]*Server installer $([regex]::Escape($script:VERSION))") { Fail "the $mode header does not say BetterDesk and the version: $($text -join ' | ')" }
        if (($text -join "`n") -match $boxChars) { Fail "the $mode header draws a box: $($text -join ' | ')" }
        $markLines = @($text | Where-Object { $_ -match $halfBlocks })
        if ($markLines.Count -ne 6) { Fail "the $mode header has $($markLines.Count) lines of the mark, not 6" }
        foreach ($line in $text) {
            if ($line.Length -gt 60) { Fail "the $mode header has a line of $($line.Length) columns: $line" }
            if ($line -match '\s$') { Fail "the $mode header has a line that ends in spaces: '$line'" }
        }
        if ($text -notcontains '[!] Windows support is experimental.') { Fail "the $mode header does not say that Windows support is experimental" }
        if ($mode -eq 'vt' -and ($got.Lines -join '') -notmatch '38;2;88;166;255') { Fail 'the vt header does not draw the mark in its 24-bit colours' }
        foreach ($c in $got.Colors) { if ($banned -contains $c) { Fail "the $mode header uses $c" } }

        $got = Get-HostLines $helpers
        foreach ($c in $got.Colors) { if ($banned -contains $c) { Fail "a status helper uses $c" } }
        if ($mode -eq 'vt') {
            $rgb = @($got.Lines | ForEach-Object { [regex]::Matches($_, '38;2;(\d+);(\d+);(\d+)') })
            if ($rgb.Count -lt 4) { Fail "the status helpers have no 24-bit colours: $($got.Lines -join ' | ')" }
            foreach ($m in $rgb) {
                $min = Get-Contrast ([int]$m.Groups[1].Value) ([int]$m.Groups[2].Value) ([int]$m.Groups[3].Value)
                if ($min -lt 3.5) { Fail ("the status colour {0} has {1:N2}:1 on black, blue or white" -f $m.Value, $min) }
            }
        }
    }
    $script:COLOR_MODE = $null
    Remove-Item -Recurse -Force $dir
}

# The menus' title box and descriptions use no colour that is hard to read
# on PowerShell's blue: no blue, magenta or dark gray.
function Test-MenuColours {
    function Clear-Host { }
    function Test-TuiAvailable { $false }
    function Read-Host { param($Prompt) '1' }
    $got = Get-HostLines { Invoke-MenuChoose -Title 'Title' -Subtitle 'Subtitle' -Items @("One`tthe first", "Back`tthe menu before") -Returns @('1', '0') }
    if (-not $got.Colors) { Fail 'the menu has no colours to check' }
    foreach ($c in $got.Colors) {
        if ('Blue', 'DarkBlue', 'Magenta', 'DarkMagenta', 'DarkGray' -contains $c) { Fail "the menu uses $c" }
    }
}

Test-HeaderAndColours
Test-MenuColours
Test-TaskLauncherLoops
Test-UpdateRewritesOldLauncher
Test-NssmServiceIsSupervised
Test-ConsoleHomeCopiesNoConsoleFiles
Test-MigrationMenusPassOverwrite
Test-PostgreSQLSwitchMovesTheWholeInstall
Test-ReleasesComeFromTheFork
Test-PostgreSQLAddressIsKept
Test-PgUriMeansPostgreSQL
Test-ServerLogFile
Test-UpdateKeepsTheAdminsPorts
Test-ChannelAndDownloadErrors
Test-PanelPublicHost
Test-WindowsFirstHour
Test-UpdateAsksTheFork
Test-FirewallPorts
Test-CertificateFromTheBinary
Test-SecretsAreRandom
Test-DatabaseUrlOnly
Test-ReleaseSignatureOnWindows
Test-NoStableReleaseTakesTheNewestPrerelease
Test-MigrateCommand
Test-BackupUsesTheServersBackup
Test-FreshInstallStartsManaged
if ($script:failures -gt 0) { exit 1 }
Write-Host 'Windows installer checks passed'
# The stand-ins set LASTEXITCODE, and a caller such as GitHub's pwsh step
# exits with it when the script ends without an exit of its own.
exit 0
