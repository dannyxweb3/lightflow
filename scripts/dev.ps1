param(
    [switch]$Browser,
    [ValidateSet('Production', 'Test')]
    [string]$Profile = 'Production'
)
$ErrorActionPreference = 'Stop'
$root = Split-Path -Parent $PSScriptRoot
$desktop = Join-Path $root 'apps\desktop'
. (Join-Path $PSScriptRoot 'dev-daemon.ps1')
$cargoBin = Join-Path $env:USERPROFILE '.cargo\bin'
if (Test-Path -LiteralPath $cargoBin) { $env:PATH = "$cargoBin;$env:PATH" }
$daemonProcess = $null
$previousLaunchProfile = $env:VITE_LIGHTFLOW_PROFILE
$env:VITE_LIGHTFLOW_PROFILE = $Profile.ToLowerInvariant()
Push-Location $root
try {
    Write-Host "Launch profile: $Profile (UI simulation only; real desktop VPN is not implemented)"
    if ($Browser) {
        Write-Host 'Browser preview: open http://127.0.0.1:1420/?preview=1 (simulated, no traffic protection)'
    } else {
        New-Item -ItemType Directory -Path (Join-Path $root 'bin') -Force | Out-Null
        $daemonPath = Join-Path $root 'bin\nimbus-daemon.exe'
        $existingDaemon = Find-ProjectDevelopmentDaemon -ExecutablePath $daemonPath
        if ($existingDaemon) {
            if (-not (Test-DevelopmentDaemonReady)) { throw 'Existing project daemon is not responding. Close the previous client and try again.' }
            Write-Host 'Reusing the existing project development daemon.'
        } else {
            go build -o bin/nimbus-daemon.exe ./cmd/nimbus-daemon
            if ($LASTEXITCODE -ne 0) { throw 'Go daemon build failed' }
            $daemonProcess = Start-Process -FilePath $daemonPath -ArgumentList '--development' -WorkingDirectory $root -WindowStyle Hidden -PassThru -RedirectStandardOutput (Join-Path $root 'bin\daemon.log') -RedirectStandardError (Join-Path $root 'bin\daemon-error.log')
            $readyDeadline = [DateTime]::UtcNow.AddSeconds(5)
            $daemonReady = $false
            do {
                if ($daemonProcess.HasExited) { throw 'Daemon did not start. Check bin/daemon-error.log; the development pipe may belong to another client.' }
                $daemonReady = Test-DevelopmentDaemonReady
                if (-not $daemonReady) { Start-Sleep -Milliseconds 100 }
            } while (-not $daemonReady -and [DateTime]::UtcNow -lt $readyDeadline)
            if (-not $daemonReady) { throw 'Daemon did not become ready within 5 seconds. Check bin/daemon-error.log.' }
        }
    }
    Set-Location $desktop
    if (-not (Test-Path -LiteralPath 'node_modules')) {
        npm ci
        if ($LASTEXITCODE -ne 0) { throw 'Dependency install failed' }
    }
    if ($Browser) { npm run dev } else { npm run tauri -- dev }
    if ($LASTEXITCODE -ne 0) { throw 'Desktop development process failed' }
} finally {
    if ($daemonProcess -and -not $daemonProcess.HasExited) { Stop-Process -Id $daemonProcess.Id }
    $env:VITE_LIGHTFLOW_PROFILE = $previousLaunchProfile
    Pop-Location
}
