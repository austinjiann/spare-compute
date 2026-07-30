param(
    [string]$DeviceName = $env:COMPUTEHOP_DEVICE_NAME,
    [Parameter(ValueFromRemainingArguments = $true)]
    [string[]]$DaemonArgs = @()
)

$ErrorActionPreference = "Stop"

if ([string]::IsNullOrWhiteSpace($DeviceName)) {
    $DeviceName = $env:COMPUTERNAME
}
if ([string]::IsNullOrWhiteSpace($DeviceName)) {
    $DeviceName = "ComputeHop Worker"
}

$Daemon = Join-Path $PSScriptRoot "bin\computehopd.exe"
& $Daemon --role worker --device-name $DeviceName @DaemonArgs
exit $LASTEXITCODE
