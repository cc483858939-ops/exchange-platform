$ErrorActionPreference = "Stop"

$deployScript = Join-Path $PSScriptRoot "deploy.ps1"
. $deployScript -LibraryOnly

function Assert-Condition {
    param([bool]$Condition, [string]$Message)
    if (-not $Condition) {
        throw $Message
    }
}

Assert-Condition (Test-ReleaseRevision "09eecd4b7f86") "valid release revision was rejected"
Assert-Condition (-not (Test-ReleaseRevision "09EECD4B7F86")) "uppercase release revision was accepted"
Assert-Condition (-not (Test-ReleaseRevision "release")) "tag-like release revision was accepted"
Assert-Condition (-not (Test-ReleaseRevision "")) "empty release revision was accepted"

$digest = ("a" * 64) -join ""
Assert-Condition (Test-DigestImage "registry.example.com/go-exchange@sha256:$digest") "valid digest image was rejected"
Assert-Condition (Test-DigestImage "registry.example.com:5000/go-exchange@sha256:$digest") "valid port-qualified digest image was rejected"
Assert-Condition (-not (Test-DigestImage "registry.example.com/go-exchange:latest")) "latest image was accepted"
Assert-Condition (-not (Test-DigestImage "registry.example.com/go-exchange:v1")) "tag image was accepted"
Assert-Condition (-not (Test-DigestImage "registry.example.com/go-exchange:stable@sha256:$digest")) "tag plus digest image was accepted"
Assert-Condition (-not (Test-DigestImage "registry.example.com/go-exchange@sha256:$($digest.ToUpperInvariant())")) "uppercase digest was accepted"
Assert-Condition (-not (Test-DigestImage "")) "empty image was accepted"

$pending = [pscustomobject]@{
    status = [pscustomobject]@{ failed = 3; conditions = @() }
}
$complete = [pscustomobject]@{
    status = [pscustomobject]@{ conditions = @([pscustomobject]@{ type = "Complete"; status = "True" }) }
}
$failed = [pscustomobject]@{
    status = [pscustomobject]@{ conditions = @([pscustomobject]@{ type = "Failed"; status = "True" }) }
}
Assert-Condition ((Get-JobTerminalState $pending) -eq "pending") "status.failed without Failed condition was terminal"
Assert-Condition ((Get-JobTerminalState $complete) -eq "complete") "Complete=True was not terminal success"
Assert-Condition ((Get-JobTerminalState $failed) -eq "failed") "Failed=True was not terminal failure"
Assert-Condition ((Get-JobTerminalState $null) -eq "pending") "missing Job status was not pending"

Write-Output "deploy.ps1 library tests: PASS"
