param(
    [ValidateSet('global','smart')][string]$Mode = 'global',
    [switch]$ForceKill,
    [switch]$DNSOff,
    [switch]$ResolveProbe,
    [switch]$ProxyOnly,
    [switch]$Relaxed,
    [switch]$OSRoute,
    [ValidateSet('https://lightflow.u26d.local:8443','https://192.168.194.128:8443')][string]$Control = 'https://lightflow.u26d.local:8443',
    [switch]$Elevated,
    [string]$ExpectedHash
)
$ErrorActionPreference = 'Stop'
$repo = (Resolve-Path (Join-Path $PSScriptRoot '../../../..')).Path
$binary = Join-Path $repo '.local/m0/m0.exe'
$safeLog = Join-Path $repo '.local/m0/tun.safe.log'
$safeError = Join-Path $repo '.local/m0/tun.safe-error.log'
$principal = [Security.Principal.WindowsPrincipal][Security.Principal.WindowsIdentity]::GetCurrent()
Push-Location $repo
try {
    if (-not $Elevated) {
        & (Join-Path $PSScriptRoot 'prepare.ps1')
        & go build -o $binary ./apps/desktop/tools/m0
        if ($LASTEXITCODE -ne 0) { throw 'Prototype build failed.' }
        $hash = (Get-FileHash -LiteralPath $binary -Algorithm SHA256).Hash
        '' | Set-Content -LiteralPath $safeLog -Encoding UTF8
        '' | Set-Content -LiteralPath $safeError -Encoding UTF8
        $arguments = '-NoProfile -ExecutionPolicy Bypass -File "' + $PSCommandPath + '" -Elevated -Mode ' + $Mode + ' -ExpectedHash ' + $hash + ' -Control ' + $Control
        if ($ForceKill) { $arguments += ' -ForceKill' }
        if ($DNSOff) { $arguments += ' -DNSOff' }
        if ($ResolveProbe) { $arguments += ' -ResolveProbe' }
        if ($ProxyOnly) { $arguments += ' -ProxyOnly' }
        if ($Relaxed) { $arguments += ' -Relaxed' }
        if ($OSRoute) { $arguments += ' -OSRoute' }
        if ($ProxyOnly) { Write-Host 'Starting elevated proxy-only control experiment. Accept the Windows UAC prompt.' }
        else { Write-Host 'Starting authorized isolated-machine TUN experiment. Accept the Windows UAC prompt.' }
        $process = Start-Process powershell.exe -Verb RunAs -WindowStyle Hidden -ArgumentList $arguments -PassThru
        $processHandle = $process.Handle
        while (-not $process.HasExited) { Start-Sleep -Milliseconds 500; $process.Refresh() }
        if (Test-Path -LiteralPath $safeLog) { Get-Content -LiteralPath $safeLog }
        if (Test-Path -LiteralPath $safeError) { Get-Content -LiteralPath $safeError }
        if ($null -eq $process.ExitCode -or $process.ExitCode -ne 0) { throw 'Elevated TUN experiment failed. See safe diagnostics above.' }
    } else {
        if (-not $principal.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)) { throw 'Administrator token required.' }
        if (-not $ExpectedHash -or (Get-FileHash -LiteralPath $binary -Algorithm SHA256).Hash -ne $ExpectedHash) { throw 'Prototype executable changed before elevation.' }
        $account = '.local/windows-s0-account.json'
        if (-not (Test-Path -LiteralPath $account) -and (Test-Path -LiteralPath '.local/windows-s0~account.json')) { $account = '.local/windows-s0~account.json' }
        $arguments = @('-mode',$Mode,'-account',$account,'-control',$Control)
        if (-not $ProxyOnly) { $arguments += '-tun' }
        if ($ForceKill) { $arguments += '-force-kill' }
        if ($DNSOff) { $arguments += '-tun-dns-off' }
        if ($ResolveProbe) { $arguments += '-resolve-probe' }
        if ($Relaxed) { $arguments += '-tun-relaxed' }
        if ($OSRoute) { $arguments += '-tun-os-route' }
        $worker = Start-Process -FilePath $binary -ArgumentList $arguments -WorkingDirectory $repo -WindowStyle Hidden -PassThru -RedirectStandardOutput $safeLog -RedirectStandardError $safeError
        $workerHandle = $worker.Handle
        $worker.WaitForExit()
        $worker.Refresh()
        if ($null -eq $worker.ExitCode) { throw 'Worker exit status unavailable.' }
        exit $worker.ExitCode
    }
} catch {
    if ($Elevated) { 'ELEVATED_RUNNER_FAILED' | Set-Content -LiteralPath $safeLog -Encoding UTF8; exit 1 }
    throw
} finally { Pop-Location }
