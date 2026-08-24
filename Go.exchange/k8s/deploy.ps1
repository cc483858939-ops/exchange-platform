[CmdletBinding()]
param(
    [string]$Namespace = "default",
    [string]$ReleaseRevision = "",
    [string]$Image = "",
    [int]$MigrationTimeoutSeconds = 180,
    [int]$RolloutTimeoutSeconds = 180,
    [switch]$LibraryOnly
)

$ErrorActionPreference = "Stop"
$ManifestDir = $PSScriptRoot

function Test-ReleaseRevision {
    param([AllowNull()][string]$Value)
    if ([string]::IsNullOrWhiteSpace($Value) -or $Value -ne $Value.Trim()) {
        return $false
    }
    return $Value -cmatch '^[0-9a-f]{7,40}$'
}

function Test-DigestImage {
    param([AllowNull()][string]$Value)
    if ([string]::IsNullOrWhiteSpace($Value) -or $Value -ne $Value.Trim()) {
        return $false
    }
    if ($Value -cnotmatch '^[^@\s]+@sha256:[0-9a-f]{64}$') {
        return $false
    }
    $repository = $Value.Substring(0, $Value.IndexOf('@'))
    if ([string]::IsNullOrWhiteSpace($repository)) {
        return $false
    }
    $lastSlash = $repository.LastIndexOf('/')
    $lastSegment = $repository.Substring($lastSlash + 1)
    return -not $lastSegment.Contains(':')
}

function Assert-DeploymentInputs {
    if (-not (Test-ReleaseRevision $ReleaseRevision)) {
        throw "ReleaseRevision must match ^[0-9a-f]{7,40}$"
    }
    if (-not (Test-DigestImage $Image)) {
        throw "Image must be an immutable image digest ending in @sha256:<64 lowercase hex>"
    }
    if ($MigrationTimeoutSeconds -le 0) {
        throw "MigrationTimeoutSeconds must be positive"
    }
    if ($RolloutTimeoutSeconds -le 0) {
        throw "RolloutTimeoutSeconds must be positive"
    }
}

function Invoke-Kubectl {
    param([Parameter(Mandatory = $true)][string[]]$Arguments)
    $output = (& kubectl --namespace $Namespace @Arguments 2>&1 | Out-String)
    $exitCode = $LASTEXITCODE
    if ($exitCode -ne 0) {
        throw "kubectl failed with exit code $($exitCode): kubectl $($Arguments -join ' ')"
    }
    return $output
}

function Render-Manifest {
    param(
        [Parameter(Mandatory = $true)][string]$ManifestPath,
        [Parameter(Mandatory = $true)][string]$ContainerName,
        [Parameter(Mandatory = $true)][string]$OutputPath
    )
    $intermediatePath = [System.IO.Path]::GetTempFileName()
    try {
        $imageOutput = Invoke-Kubectl -Arguments @("set", "image", "--local", "-f", $ManifestPath, "$ContainerName=$Image", "-o", "json")
        $imageObject = $imageOutput | ConvertFrom-Json
        $imageObject | ConvertTo-Json -Depth 100 | Set-Content -LiteralPath $intermediatePath -Encoding UTF8

        $releaseOutput = Invoke-Kubectl -Arguments @("set", "env", "--local", "-f", $intermediatePath, "RELEASE_REVISION=$ReleaseRevision", "-o", "json")
        $releaseObject = $releaseOutput | ConvertFrom-Json
        $releaseObject | ConvertTo-Json -Depth 100 | Set-Content -LiteralPath $OutputPath -Encoding UTF8
        return $releaseObject
    }
    finally {
        Remove-Item -LiteralPath $intermediatePath -Force -ErrorAction SilentlyContinue
    }
}

function Assert-MigrationRender {
    param([Parameter(Mandatory = $true)][object]$Job)
    if ($Job.kind -ne "Job") {
        throw "migration render did not produce a Job"
    }
    $containers = @($Job.spec.template.spec.containers | Where-Object { $_.name -eq "migrate" })
    if ($containers.Count -ne 1) {
        throw "migration render must contain exactly one migrate container"
    }
    if ($containers[0].image -ne $Image) {
        throw "migration render image does not match the requested digest"
    }
    $releaseEnv = @($containers[0].env | Where-Object { $_.name -eq "RELEASE_REVISION" })
    if ($releaseEnv.Count -ne 1 -or $releaseEnv[0].value -ne $ReleaseRevision) {
        throw "migration render is missing the requested RELEASE_REVISION"
    }
}

function Get-JobTerminalState {
    param([AllowNull()][object]$Job)
    if ($null -eq $Job -or $null -eq $Job.status) {
        return "pending"
    }
    foreach ($condition in @($Job.status.conditions)) {
        if ($condition.type -eq "Failed" -and $condition.status -eq "True") {
            return "failed"
        }
        if ($condition.type -eq "Complete" -and $condition.status -eq "True") {
            return "complete"
        }
    }
    # status.failed alone is not a terminal failure; the Failed condition is
    # the authoritative terminal signal for this rollout gate.
    return "pending"
}

function Invoke-Deployment {
    Assert-DeploymentInputs
    $temporaryPaths = @()
    try {
        $migrationPath = [System.IO.Path]::GetTempFileName()
        $temporaryPaths += $migrationPath
        $migration = Render-Manifest -ManifestPath (Join-Path $ManifestDir "migration-job.yaml") -ContainerName "migrate" -OutputPath $migrationPath
        Assert-MigrationRender $migration

        $createdOutput = Invoke-Kubectl -Arguments @("create", "-f", $migrationPath, "-o", "json")
        $createdJob = $createdOutput | ConvertFrom-Json
        $jobName = [string]$createdJob.metadata.name
        $jobUID = [string]$createdJob.metadata.uid
        if ([string]::IsNullOrWhiteSpace($jobName) -or [string]::IsNullOrWhiteSpace($jobUID)) {
            throw "migration Job create response did not include name and UID"
        }

        $deadline = [DateTime]::UtcNow.AddSeconds($MigrationTimeoutSeconds)
        while ($true) {
            $jobOutput = Invoke-Kubectl -Arguments @("get", "job", $jobName, "-o", "json")
            $job = $jobOutput | ConvertFrom-Json
            if ([string]$job.metadata.uid -ne $jobUID) {
                throw "migration Job UID changed while polling $jobName"
            }
            $terminalState = Get-JobTerminalState $job
            if ($terminalState -eq "complete") {
                break
            }
            if ($terminalState -eq "failed") {
                throw "migration Job $jobName failed; API and Worker rollout stopped"
            }
            if ([DateTime]::UtcNow -ge $deadline) {
                throw "migration Job $jobName did not complete within $MigrationTimeoutSeconds seconds"
            }
            Start-Sleep -Seconds 2
        }

        Invoke-Kubectl -Arguments @("apply", "-f", (Join-Path $ManifestDir "api-service.yaml")) | Out-Null
        $apiPath = [System.IO.Path]::GetTempFileName()
        $temporaryPaths += $apiPath
        $null = Render-Manifest -ManifestPath (Join-Path $ManifestDir "api-deployment.yaml") -ContainerName "app" -OutputPath $apiPath
        Invoke-Kubectl -Arguments @("apply", "-f", $apiPath) | Out-Null
        Invoke-Kubectl -Arguments @("rollout", "status", "deployment/go-exchange-api", "--timeout=${RolloutTimeoutSeconds}s") | Out-Null

        $workerPath = [System.IO.Path]::GetTempFileName()
        $temporaryPaths += $workerPath
        $null = Render-Manifest -ManifestPath (Join-Path $ManifestDir "worker-deployment.yaml") -ContainerName "app" -OutputPath $workerPath
        Invoke-Kubectl -Arguments @("apply", "-f", $workerPath) | Out-Null
        Invoke-Kubectl -Arguments @("rollout", "status", "deployment/go-exchange-worker", "--timeout=${RolloutTimeoutSeconds}s") | Out-Null
    }
    finally {
        foreach ($temporaryPath in $temporaryPaths) {
            Remove-Item -LiteralPath $temporaryPath -Force -ErrorAction SilentlyContinue
        }
    }
}

if (-not $LibraryOnly) {
    Invoke-Deployment
}
