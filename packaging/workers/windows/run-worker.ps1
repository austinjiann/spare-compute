param(
    [string]$DeviceName = $env:COMPUTEHOP_DEVICE_NAME,
    [switch]$LanOnly,
    [string]$ConnectivityUrl = "",
    [string[]]$StunServer = @(),
    [string[]]$TurnServer = @(),
    [string]$TurnUsername = "",
    [string]$TurnPassword = "",
    [Parameter(ValueFromRemainingArguments = $true)]
    [string[]]$ExtraDaemonArgs = @()
)

$ErrorActionPreference = "Stop"

if ([string]::IsNullOrWhiteSpace($DeviceName)) {
    $DeviceName = $env:COMPUTERNAME
}
if ([string]::IsNullOrWhiteSpace($DeviceName)) {
    $DeviceName = "ComputeHop Worker"
}

$Daemon = Join-Path $PSScriptRoot "bin\computehopd.exe"
$DaemonArgs = @("--role", "worker", "--device-name", $DeviceName)
if ($LanOnly) {
    $DaemonArgs += "--lan-only"
}
if (-not [string]::IsNullOrWhiteSpace($ConnectivityUrl)) {
    $DaemonArgs += @("--connectivity-url", $ConnectivityUrl)
}
foreach ($Server in $StunServer) {
    if (-not [string]::IsNullOrWhiteSpace($Server)) {
        $DaemonArgs += @("--stun-server", $Server)
    }
}
foreach ($Server in $TurnServer) {
    if (-not [string]::IsNullOrWhiteSpace($Server)) {
        $DaemonArgs += @("--turn-server", $Server)
    }
}
if (-not [string]::IsNullOrWhiteSpace($TurnUsername)) {
    $DaemonArgs += @("--turn-username", $TurnUsername)
}
if (-not [string]::IsNullOrWhiteSpace($TurnPassword)) {
    $DaemonArgs += @("--turn-password", $TurnPassword)
}
$DaemonArgs += $ExtraDaemonArgs
& $Daemon @DaemonArgs
exit $LASTEXITCODE
