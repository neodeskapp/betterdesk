package generator

import (
	"os"
	"path/filepath"
	"strings"
)

// The installer scripts that go next to the client in a template build.
// Their text is what the old services wrote (windowsSupportInstaller.js,
// platformSupportInstaller.js).

const windowsInstallerScript = `[CmdletBinding()]
param(
    [switch]$UseScheduledTask
)

$ErrorActionPreference = 'Stop'
$profileInstallsService = __INSTALL_SERVICE__
$profileAutostarts = __AUTOSTART__
$app = @(
    (Join-Path $PSScriptRoot 'betterdesk.exe'),
    (Join-Path $PSScriptRoot 'rustdesk.exe')
) | Where-Object { Test-Path -LiteralPath $_ } | Select-Object -First 1

if (-not $app) {
    throw 'BetterDesk executable was not found next to this installer.'
}

$appName = [IO.Path]::GetFileNameWithoutExtension($app)
$serviceName = $appName
$taskName = "$appName Tray"
$startupShortcut = Join-Path $env:ProgramData "Microsoft\Windows\Start Menu\Programs\Startup\$appName Tray.lnk"
$isUninstall = [IO.Path]::GetFileNameWithoutExtension($MyInvocation.MyCommand.Name) -match 'Uninstall'
$identity = [Security.Principal.WindowsIdentity]::GetCurrent()
$principal = [Security.Principal.WindowsPrincipal]::new($identity)
$isAdministrator = $principal.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)

if (-not $isAdministrator) {
    $arguments = @(
        '-NoProfile', '-ExecutionPolicy', 'Bypass', '-File',
        $MyInvocation.MyCommand.Path
    )
    if ($UseScheduledTask) {
        $arguments += '-UseScheduledTask'
    }
    $elevated = Start-Process -FilePath 'powershell.exe' -Verb RunAs -ArgumentList $arguments -Wait -PassThru
    exit $elevated.ExitCode
}

if ($isUninstall) {
    Unregister-ScheduledTask -TaskName $taskName -Confirm:$false -ErrorAction SilentlyContinue
    if ($profileInstallsService -and (Test-Path -LiteralPath $app)) {
        $process = Start-Process -FilePath $app -ArgumentList '--uninstall-service' -Wait -PassThru -WindowStyle Hidden
        if ($process.ExitCode -ne 0) {
            throw "BetterDesk service uninstall failed with exit code $($process.ExitCode)."
        }
    }
    exit 0
}

if ($profileInstallsService) {
    $process = Start-Process -FilePath $app -ArgumentList '--install-service' -Wait -PassThru -WindowStyle Hidden
    if ($process.ExitCode -ne 0) {
        throw "BetterDesk service installation failed with exit code $($process.ExitCode)."
    }
    $service = Get-Service -Name $serviceName -ErrorAction Stop
    if ($service.Status -notin @('Running', 'StartPending')) {
        throw "BetterDesk service is not running (status: $($service.Status))."
    }
}

if ($profileAutostarts -and (-not $profileInstallsService -or $UseScheduledTask)) {
    Remove-Item -LiteralPath $startupShortcut -Force -ErrorAction SilentlyContinue
    $taskAction = New-ScheduledTaskAction -Execute $app -Argument '--tray' -WorkingDirectory (Split-Path -Parent $app)
    $taskTrigger = New-ScheduledTaskTrigger -AtLogOn -User $identity.Name
    $taskPrincipal = New-ScheduledTaskPrincipal -UserId $identity.Name -LogonType InteractiveToken -RunLevel Highest
    $taskSettings = New-ScheduledTaskSettingsSet -RestartCount 3 -RestartInterval (New-TimeSpan -Minutes 1) -AllowStartIfOnBatteries -DontStopIfGoingOnBatteries
    Register-ScheduledTask -TaskName $taskName -Action $taskAction -Trigger $taskTrigger -Principal $taskPrincipal -Settings $taskSettings -Force | Out-Null
}

if (-not $profileInstallsService -and -not $profileAutostarts) {
    Write-Host "BetterDesk installed without service or autostart."
} else {
    Write-Host "BetterDesk Support startup configuration installed successfully."
}
`

// InstallerOptions say what an installer sets up on the machine.
type InstallerOptions struct {
	InstallService bool
	Autostart      bool
}

func psBool(v bool) string {
	if v {
		return "$true"
	}
	return "$false"
}

// WindowsInstallerScript is the PowerShell installer for a Windows build.
// The same script uninstalls when its file name has "Uninstall" in it.
func WindowsInstallerScript(o InstallerOptions) string {
	script := strings.Replace(windowsInstallerScript, "__INSTALL_SERVICE__", psBool(o.InstallService), 1)
	return strings.Replace(script, "__AUTOSTART__", psBool(o.Autostart), 1)
}

// writeWindowsInstallers writes Install-BetterDesk.ps1 and
// Uninstall-BetterDesk.ps1 into dir.
func writeWindowsInstallers(dir string, o InstallerOptions) error {
	script := []byte(WindowsInstallerScript(o))
	for _, name := range []string{"Install-BetterDesk.ps1", "Uninstall-BetterDesk.ps1"} {
		if err := os.WriteFile(filepath.Join(dir, name), script, 0o666); err != nil {
			return err
		}
	}
	return nil
}

func shellBool(v bool) string {
	if v {
		return "1"
	}
	return "0"
}

func linuxInstaller(o InstallerOptions) string {
	return `#!/bin/sh
set -eu
APP_DIR="/opt/betterdesk-support-agent"
UNIT="betterdesk-support-agent.service"
SOURCE_DIR="$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)"
INSTALL_SERVICE="` + shellBool(o.InstallService) + `"
AUTOSTART="` + shellBool(o.Autostart) + `"

if [ "$(id -u)" -ne 0 ]; then
    exec sudo "$0" "$@"
fi

mkdir -p "$APP_DIR"
cp -a "$SOURCE_DIR"/. "$APP_DIR"/
chmod 755 "$APP_DIR"/betterdesk 2>/dev/null || true
if [ "$INSTALL_SERVICE" = 1 ]; then
    cat >"/etc/systemd/system/$UNIT" <<EOF
[Unit]
Description=BetterDesk Support Agent
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
ExecStart=$APP_DIR/betterdesk --service
Restart=on-failure
RestartSec=5
User=root
NoNewPrivileges=false

[Install]
WantedBy=multi-user.target
EOF
    systemctl daemon-reload
    if [ "$AUTOSTART" = 1 ]; then
        systemctl enable --now "$UNIT"
    else
        systemctl disable "$UNIT" 2>/dev/null || true
    fi
fi
echo "BetterDesk Support Agent installed."
`
}

func linuxUninstaller(o InstallerOptions) string {
	return `#!/bin/sh
set -eu
APP_DIR="/opt/betterdesk-support-agent"
UNIT="betterdesk-support-agent.service"
if [ "$(id -u)" -ne 0 ]; then
    exec sudo "$0" "$@"
fi
if [ "` + shellBool(o.InstallService) + `" = 1 ]; then
    systemctl disable --now "$UNIT" 2>/dev/null || true
    rm -f "/etc/systemd/system/$UNIT"
    systemctl daemon-reload
fi
rm -rf "$APP_DIR"
echo "BetterDesk Support Agent removed."
`
}

func macInstaller(o InstallerOptions) string {
	runAtLoad := "false"
	if o.Autostart {
		runAtLoad = "true"
	}
	return `#!/bin/sh
set -eu
SOURCE_DIR="$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)"
APP="$(find "$SOURCE_DIR" -maxdepth 2 -name '*.app' -type d | head -n 1)"
TARGET="/Applications/BetterDesk Support Agent.app"
PLIST="/Library/LaunchDaemons/com.unitronix.betterdesk-support-agent.plist"
if [ -z "$APP" ]; then echo "BetterDesk app bundle not found" >&2; exit 1; fi
if [ "$(id -u)" -ne 0 ]; then exec sudo "$0" "$@"; fi
rm -rf "$TARGET"
ditto "$APP" "$TARGET"
if [ "` + shellBool(o.InstallService) + `" = 1 ]; then
    SERVICE="$TARGET/Contents/MacOS/service"
    [ -x "$SERVICE" ] || SERVICE="$TARGET/Contents/MacOS/betterdesk"
    cat >"$PLIST" <<EOF
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
<key>Label</key><string>com.unitronix.betterdesk-support-agent</string>
<key>ProgramArguments</key><array><string>$SERVICE</string><string>--service</string></array>
<key>RunAtLoad</key><` + runAtLoad + `/>
<key>KeepAlive</key><true/>
</dict></plist>
EOF
    launchctl bootout system "$PLIST" 2>/dev/null || true
    launchctl bootstrap system "$PLIST"
    if [ "` + shellBool(o.Autostart) + `" = 1 ]; then launchctl kickstart -k system/com.unitronix.betterdesk-support-agent; fi
fi
echo "BetterDesk Support Agent installed."
`
}

func macUninstaller(o InstallerOptions) string {
	return `#!/bin/sh
set -eu
PLIST="/Library/LaunchDaemons/com.unitronix.betterdesk-support-agent.plist"
if [ "$(id -u)" -ne 0 ]; then exec sudo "$0" "$@"; fi
if [ "` + shellBool(o.InstallService) + `" = 1 ]; then
    launchctl bootout system "$PLIST" 2>/dev/null || true
    rm -f "$PLIST"
fi
rm -rf "/Applications/BetterDesk Support Agent.app"
echo "BetterDesk Support Agent removed."
`
}

// UnixInstallerScripts returns the install and uninstall scripts for a
// Linux build, and for every other platform the macOS ones.
func UnixInstallerScripts(platform string, o InstallerOptions) (install, uninstall string) {
	if platform == "linux" {
		return linuxInstaller(o), linuxUninstaller(o)
	}
	return macInstaller(o), macUninstaller(o)
}

// writeUnixInstallers writes install-betterdesk-support.sh and
// uninstall-betterdesk-support.sh, both executable, into dir.
func writeUnixInstallers(dir, platform string, o InstallerOptions) error {
	install, uninstall := UnixInstallerScripts(platform, o)
	for name, text := range map[string]string{"install-betterdesk-support.sh": install, "uninstall-betterdesk-support.sh": uninstall} {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte(text), 0o755); err != nil {
			return err
		}
		if err := os.Chmod(path, 0o755); err != nil {
			return err
		}
	}
	return nil
}
