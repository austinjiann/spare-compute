param(
    [string]$DeviceName = $env:COMPUTEHOP_DEVICE_NAME,
    [switch]$Check,
    [switch]$LanOnly,
    [string]$ConnectivityUrl = "",
    [string[]]$StunServer = @(),
    [string[]]$TurnServer = @(),
    [string]$TurnUsername = "",
    [string]$TurnPassword = ""
)

$ErrorActionPreference = "Stop"

if ([string]::IsNullOrWhiteSpace($DeviceName)) {
    $DeviceName = $env:COMPUTERNAME
}
if ([string]::IsNullOrWhiteSpace($DeviceName)) {
    $DeviceName = "ComputeHop Worker"
}

if (
    -not $LanOnly `
    -and [string]::IsNullOrWhiteSpace($ConnectivityUrl) `
    -and $StunServer.Count -eq 0 `
    -and $TurnServer.Count -eq 0 `
    -and [string]::IsNullOrWhiteSpace($TurnUsername) `
    -and [string]::IsNullOrWhiteSpace($TurnPassword)
) {
    $LanOnly = $true
}

$SourceCli = Join-Path $PSScriptRoot "bin\computehop.exe"
$SourceDaemon = Join-Path $PSScriptRoot "bin\computehopd.exe"
$SourceRunner = Join-Path $PSScriptRoot "run-worker.ps1"
foreach ($Source in @($SourceCli, $SourceDaemon, $SourceRunner)) {
    if (-not (Test-Path -LiteralPath $Source -PathType Leaf)) {
        throw "Packaged worker file is missing: $Source"
    }
}

$InstallDir = Join-Path $env:LOCALAPPDATA "ComputeHop\Worker"
$BinDir = Join-Path $InstallDir "bin"

if (-not $Check) {
    New-Item -ItemType Directory -Force -Path $BinDir | Out-Null

    Copy-Item -Force -Path $SourceCli -Destination (Join-Path $BinDir "computehop.exe")
    Copy-Item -Force -Path $SourceDaemon -Destination (Join-Path $BinDir "computehopd.exe")
    Copy-Item -Force -Path $SourceRunner -Destination (Join-Path $InstallDir "run-worker.ps1")
}

$InstalledRunner = Join-Path $InstallDir "run-installed-worker.ps1"
function Quote-PowerShellLiteral([string]$Value) {
    return "'" + $Value.Replace("'", "''") + "'"
}
function Add-PowerShellOption([System.Collections.Generic.List[string]]$Parts, [string]$Name, [string]$Value) {
    if (-not [string]::IsNullOrWhiteSpace($Value)) {
        $Parts.Add($Name)
        $Parts.Add((Quote-PowerShellLiteral $Value))
    }
}
$QuotedDeviceName = Quote-PowerShellLiteral $DeviceName
$RunnerParts = [System.Collections.Generic.List[string]]::new()
$RunnerParts.Add("-DeviceName")
$RunnerParts.Add($QuotedDeviceName)
if ($LanOnly) {
    $RunnerParts.Add("-LanOnly")
}
Add-PowerShellOption $RunnerParts "-ConnectivityUrl" $ConnectivityUrl
foreach ($Server in $StunServer) {
    Add-PowerShellOption $RunnerParts "-StunServer" $Server
}
foreach ($Server in $TurnServer) {
    Add-PowerShellOption $RunnerParts "-TurnServer" $Server
}
Add-PowerShellOption $RunnerParts "-TurnUsername" $TurnUsername
Add-PowerShellOption $RunnerParts "-TurnPassword" $TurnPassword
$RunnerArgs = $RunnerParts -join " "

if ($Check) {
    Write-Host "Worker install check passed."
    Write-Host "Would install worker files to: $InstallDir"
    Write-Host "Would register scheduled task: ComputeHop Worker"
    Write-Host "Would run daemon as worker: $DeviceName"
    Write-Host "Would pass runner arguments: $RunnerArgs"
    exit 0
}

@"
`$ErrorActionPreference = "Stop"
& "`$PSScriptRoot\run-worker.ps1" $RunnerArgs
exit `$LASTEXITCODE
"@ | Set-Content -Path $InstalledRunner -Encoding UTF8

$TaskName = "ComputeHop Worker"
$Arguments = "-NoProfile -ExecutionPolicy Bypass -File `"$InstalledRunner`""
$Action = New-ScheduledTaskAction -Execute "powershell.exe" -Argument $Arguments
$Trigger = New-ScheduledTaskTrigger -AtLogOn
$CurrentUser = [System.Security.Principal.WindowsIdentity]::GetCurrent().Name
$Principal = New-ScheduledTaskPrincipal -UserId $CurrentUser -LogonType Interactive -RunLevel Limited
$Settings = New-ScheduledTaskSettingsSet -RestartCount 3 -RestartInterval (New-TimeSpan -Minutes 1)

Register-ScheduledTask -TaskName $TaskName -Action $Action -Trigger $Trigger -Principal $Principal -Settings $Settings -Force | Out-Null
Start-ScheduledTask -TaskName $TaskName

Write-Host "Installed ComputeHop worker scheduled task."
Write-Host "Confirm pairing requests with: $BinDir\computehop.exe connect confirm"
