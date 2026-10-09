param(
    [ValidateSet('global','smart')][string]$Mode = 'global',
    [string]$Account = '.local/windows-s0-account.json',
    [ValidateSet('https://lightflow.u26d.local:8443','https://192.168.194.128:8443')][string]$Control = 'https://lightflow.u26d.local:8443',
    [switch]$ResolveProbe,
    [switch]$Check
)
$ErrorActionPreference = 'Stop'
$repo = (Resolve-Path (Join-Path $PSScriptRoot '../../../..')).Path
Push-Location $repo
try {
    if (-not $Check -and $Account -eq '.local/windows-s0-account.json' -and
        -not (Test-Path -LiteralPath $Account) -and (Test-Path -LiteralPath '.local/windows-s0~account.json')) {
        $Account = '.local/windows-s0~account.json'
        Write-Host 'Using local S0 account filename alias: .local/windows-s0~account.json'
    }
    & (Join-Path $PSScriptRoot 'prepare.ps1')
    $arguments = @('run','./apps/desktop/tools/m0','-mode',$Mode,'-control',$Control)
    if ($ResolveProbe) { $arguments += '-resolve-probe' }
    if ($Check) { $arguments += '-check' } else { $arguments += @('-account',$Account) }
    & go @arguments
    if ($LASTEXITCODE -ne 0) { throw 'M0 probe failed. See the safe error code above.' }
} finally { Pop-Location }
