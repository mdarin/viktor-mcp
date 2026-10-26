# Registers viktor syncd as a scheduled task that runs at user logon —
# the Windows equivalent of the macOS launchd agent (card #939).
#
# Usage (PowerShell, any working directory):
#   powershell -ExecutionPolicy Bypass -File deploy\windows\install-syncd.ps1 `
#     -BinaryPath "$env:USERPROFILE\.local\bin\viktor.exe" `
#     -WorkingDirectory "C:\path\to\viktor\repo"
#
# The .env with memory/history sections is loaded from -WorkingDirectory.
# Logs: %USERPROFILE%\viktor-syncd.log (stdout+stderr, appended).
param(
    [Parameter(Mandatory = $true)][string]$BinaryPath,
    [Parameter(Mandatory = $false)][string]$WorkingDirectory = (Get-Location).Path,
    [string]$Interval = "15m",
    [string]$FullInterval = "24h"
)

$ErrorActionPreference = "Stop"

if (-not (Test-Path $BinaryPath)) {
    throw "binary not found: $BinaryPath (build with: go build -o viktor.exe ./cmd/viktor)"
}
if (-not (Test-Path (Join-Path $WorkingDirectory ".env"))) {
    Write-Warning "no .env in $WorkingDirectory — syncd will run with both indexes disabled"
}

# cmd.exe wrapper captures stdout+stderr into a log file; a plain
# scheduled task discards console output.
$action = New-ScheduledTaskAction -Execute "cmd.exe" `
    -Argument "/c `"$BinaryPath`" syncd -interval $Interval -full-interval $FullInterval >> `"$env:USERPROFILE\viktor-syncd.log`" 2>&1" `
    -WorkingDirectory $WorkingDirectory
$trigger = New-ScheduledTaskTrigger -AtLogOn
$settings = New-ScheduledTaskSettingsSet `
    -AllowStartIfOnBatteries -DontStopIfGoingOnBatteries `
    -RestartCount 3 -RestartInterval (New-TimeSpan -Minutes 1) `
    -ExecutionTimeLimit ([TimeSpan]::Zero) `
    -StartWhenAvailable

Register-ScheduledTask -TaskName "viktor-syncd" `
    -Action $action -Trigger $trigger -Settings $settings `
    -Description "viktor memory+history sync daemon (launchd-equivalent, card #939)" `
    -Force | Out-Null

Start-ScheduledTask -TaskName "viktor-syncd"
Start-Sleep -Seconds 3
$info = Get-ScheduledTaskInfo -TaskName "viktor-syncd"
Write-Host "task 'viktor-syncd' registered; state: $($info.State)"
Write-Host "log: $env:USERPROFILE\viktor-syncd.log"
Write-Host "remove with: Unregister-ScheduledTask -TaskName 'viktor-syncd' -Confirm:`$false"
