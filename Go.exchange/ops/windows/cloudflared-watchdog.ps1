[CmdletBinding()]
param(
    [string]$ServiceName = 'cloudflared',
    [string]$ReadyUrl = 'http://127.0.0.1:20241/ready',
    [int]$ProbeTimeoutSeconds = 5,
    [int]$ConfirmDelaySeconds = 10,
    [int]$RestartCooldownMinutes = 15,
    [int]$RecoveryTimeoutSeconds = 60
)

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'

$stateDirectory = 'C:\ProgramData\GoExchange\cloudflared-watchdog'
$stateFile = Join-Path $stateDirectory 'last-restart.txt'
$logFile = Join-Path $stateDirectory 'watchdog.log'
$mutex = New-Object System.Threading.Mutex($false, 'Global\GoExchangeCloudflaredWatchdog')
$lockAcquired = $false

function Write-WatchdogLog {
    param([Parameter(Mandatory = $true)][string]$Message)

    $timestamp = [DateTimeOffset]::Now.ToString('o')
    Add-Content -LiteralPath $logFile -Value "$timestamp $Message" -Encoding UTF8
}

function Invoke-TunnelReadinessProbe {
    try {
        $response = Invoke-WebRequest `
            -UseBasicParsing `
            -Uri $ReadyUrl `
            -TimeoutSec $ProbeTimeoutSeconds
        $payload = $response.Content | ConvertFrom-Json
        $readyConnections = [int]$payload.readyConnections
        return [pscustomobject]@{
            Ready = $response.StatusCode -eq 200 -and $readyConnections -gt 0
            Detail = "status=$($response.StatusCode) readyConnections=$readyConnections"
        }
    }
    catch {
        return [pscustomobject]@{
            Ready = $false
            Detail = "probe_error=$($_.Exception.Message)"
        }
    }
}

try {
    try {
        $lockAcquired = $mutex.WaitOne(0)
    }
    catch [System.Threading.AbandonedMutexException] {
        $lockAcquired = $true
    }

    if (-not $lockAcquired) {
        exit 0
    }

    New-Item -ItemType Directory -Path $stateDirectory -Force | Out-Null

    $firstProbe = Invoke-TunnelReadinessProbe
    if ($firstProbe.Ready) {
        exit 0
    }

    Start-Sleep -Seconds $ConfirmDelaySeconds
    $secondProbe = Invoke-TunnelReadinessProbe
    if ($secondProbe.Ready) {
        Write-WatchdogLog "Transient readiness failure recovered without restart; first=$($firstProbe.Detail)."
        exit 0
    }

    $now = [DateTimeOffset]::UtcNow
    $lastRestartAt = [DateTimeOffset]::MinValue
    if (Test-Path -LiteralPath $stateFile) {
        try {
            $lastRestartAt = [DateTimeOffset]::Parse(
                (Get-Content -LiteralPath $stateFile -Raw).Trim()
            )
        }
        catch {
            Write-WatchdogLog "Ignored invalid restart state: $($_.Exception.Message)"
        }
    }

    if (($now - $lastRestartAt).TotalMinutes -lt $RestartCooldownMinutes) {
        exit 0
    }

    Set-Content -LiteralPath $stateFile -Value $now.ToString('o') -Encoding ASCII
    Write-WatchdogLog "Restarting service '$ServiceName' after two failed probes; first=$($firstProbe.Detail); second=$($secondProbe.Detail)."

    $service = Get-Service -Name $ServiceName -ErrorAction Stop
    if ($service.Status -eq [System.ServiceProcess.ServiceControllerStatus]::Running) {
        Restart-Service -Name $ServiceName -Force -ErrorAction Stop
    }
    else {
        Start-Service -Name $ServiceName -ErrorAction Stop
    }

    $deadline = [DateTimeOffset]::UtcNow.AddSeconds($RecoveryTimeoutSeconds)
    do {
        Start-Sleep -Seconds 5
        $recoveryProbe = Invoke-TunnelReadinessProbe
        if ($recoveryProbe.Ready) {
            Write-WatchdogLog "Tunnel recovered after service restart; $($recoveryProbe.Detail)."
            exit 0
        }
    } while ([DateTimeOffset]::UtcNow -lt $deadline)

    Write-WatchdogLog "Tunnel remained unhealthy after service restart; last=$($recoveryProbe.Detail)."
    exit 1
}
catch {
    try {
        New-Item -ItemType Directory -Path $stateDirectory -Force | Out-Null
        Write-WatchdogLog "Watchdog failed: $($_.Exception.Message)"
    }
    catch {
        # The scheduled task exit code remains the fallback signal if logging fails.
    }
    exit 1
}
finally {
    if ($lockAcquired) {
        try {
            $mutex.ReleaseMutex()
        }
        catch {
            # The process is exiting; there is no further recovery action here.
        }
    }
    $mutex.Dispose()
}
