param(
    [string]$DeviceName = $env:COMPUTEHOP_DEVICE_NAME
)

$ErrorActionPreference = "Stop"

if ([string]::IsNullOrWhiteSpace($DeviceName)) {
    $DeviceName = $env:COMPUTERNAME
}
if ([string]::IsNullOrWhiteSpace($DeviceName)) {
    $DeviceName = "ComputeHop Worker"
}

$InstallDir = Join-Path $env:LOCALAPPDATA "ComputeHop\Worker"
$BinDir = Join-Path $InstallDir "bin"
New-Item -ItemType Directory -Force -Path $BinDir | Out-Null

Copy-Item -Force -Path (Join-Path $PSScriptRoot "bin\computehop.exe") -Destination (Join-Path $BinDir "computehop.exe")
Copy-Item -Force -Path (Join-Path $PSScriptRoot "bin\computehopd.exe") -Destination (Join-Path $BinDir "computehopd.exe")
Copy-Item -Force -Path (Join-Path $PSScriptRoot "run-worker.ps1") -Destination (Join-Path $InstallDir "run-worker.ps1")

$RunScript = Join-Path $InstallDir "run-worker.ps1"
$TaskName = "ComputeHop Worker"
$Arguments = "-NoProfile -ExecutionPolicy Bypass -File `"$RunScript`" -DeviceName `"$DeviceName`" --lan-only"
$Action = New-ScheduledTaskAction -Execute "powershell.exe" -Argument $Arguments
$Trigger = New-ScheduledTaskTrigger -AtLogOn
$CurrentUser = [System.Security.Principal.WindowsIdentity]::GetCurrent().Name
$Principal = New-ScheduledTaskPrincipal -UserId $CurrentUser -LogonType Interactive -RunLevel Limited
$Settings = New-ScheduledTaskSettingsSet -RestartCount 3 -RestartInterval (New-TimeSpan -Minutes 1)

Register-ScheduledTask -TaskName $TaskName -Action $Action -Trigger $Trigger -Principal $Principal -Settings $Settings -Force | Out-Null
Start-ScheduledTask -TaskName $TaskName

Write-Host "Installed ComputeHop worker scheduled task."
Write-Host "Confirm pairing requests with: $BinDir\computehop.exe connect confirm"
