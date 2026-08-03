param(
    [string]$DeviceName = "",
    [switch]$LanOnly,
    [switch]$RemoteEnabled
)

$ErrorActionPreference = "Stop"

if ($LanOnly -and $RemoteEnabled) {
    throw "-LanOnly and -RemoteEnabled cannot be combined."
}

$InstallDir = Join-Path $env:LOCALAPPDATA "ComputeHop\Worker"
$BinDir = Join-Path $InstallDir "bin"
$Cli = Join-Path $BinDir "computehop.exe"
$Daemon = Join-Path $BinDir "computehopd.exe"
$Runner = Join-Path $InstallDir "run-installed-worker.ps1"
$TaskName = "ComputeHop Worker"

foreach ($Path in @($Cli, $Daemon, (Join-Path $InstallDir "run-worker.ps1"), $Runner)) {
    if (-not (Test-Path -LiteralPath $Path -PathType Leaf)) {
        throw "Installed worker file is missing: $Path"
    }
}

$Task = Get-ScheduledTask -TaskName $TaskName -ErrorAction Stop
$Action = $Task.Actions | Select-Object -First 1
if ($null -eq $Action -or $Action.Execute -notmatch "powershell(\.exe)?$" -or $Action.Arguments -notlike "*run-installed-worker.ps1*") {
    throw "Scheduled task does not launch the installed ComputeHop worker runner."
}
if ($Task.State -eq "Disabled") {
    throw "Scheduled task is disabled: $TaskName"
}

$RunnerText = Get-Content -LiteralPath $Runner -Raw
if (-not [string]::IsNullOrWhiteSpace($DeviceName) -and $RunnerText -notlike "*$DeviceName*") {
    throw "Installed worker runner does not include expected device name: $DeviceName"
}
if ($LanOnly -and $RunnerText -notlike "*-LanOnly*") {
    throw "Expected installed worker runner to include -LanOnly."
}
if ($RemoteEnabled -and $RunnerText -like "*-LanOnly*") {
    throw "Expected installed worker runner to allow remote connectivity, but -LanOnly is present."
}

& $Cli version | Out-Null
$StatusOutput = & $Cli status
if (($StatusOutput -join "`n") -notmatch "\(worker,") {
    throw "Worker daemon status did not report worker role.`n$($StatusOutput -join "`n")"
}
if (-not [string]::IsNullOrWhiteSpace($DeviceName) -and ($StatusOutput -join "`n") -notmatch [regex]::Escape("Device: $DeviceName ")) {
    throw "Worker daemon status did not report expected device name: $DeviceName`n$($StatusOutput -join "`n")"
}
& $Cli doctor | Out-Null

Write-Host "Installed ComputeHop worker validation passed."
Write-Host "Install dir: $InstallDir"
Write-Host "Scheduled task: $TaskName"
