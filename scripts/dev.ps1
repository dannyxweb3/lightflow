param([switch]$Browser)
$ErrorActionPreference = 'Stop'
$root = Split-Path -Parent $PSScriptRoot
$desktop = Join-Path $root 'apps\desktop'
$cargoBin = Join-Path $env:USERPROFILE '.cargo\bin'
if (Test-Path -LiteralPath $cargoBin) { $env:PATH = "$cargoBin;$env:PATH" }
$daemonProcess = $null
Push-Location $root
try {
    if ($Browser) {
        Write-Host 'Browser preview: open http://127.0.0.1:1420/?preview=1 (simulated, no traffic protection)'
    } else {
        New-Item -ItemType Directory -Path (Join-Path $root 'bin') -Force | Out-Null
        go build -o bin/nimbus-daemon.exe ./cmd/nimbus-daemon
        if ($LASTEXITCODE -ne 0) { throw 'Go daemon build failed' }
        $daemonProcess = Start-Process -FilePath (Join-Path $root 'bin\nimbus-daemon.exe') -ArgumentList '--development' -WorkingDirectory $root -WindowStyle Hidden -PassThru -RedirectStandardOutput (Join-Path $root 'bin\daemon.log') -RedirectStandardError (Join-Path $root 'bin\daemon-error.log')
        Start-Sleep -Milliseconds 400
        if ($daemonProcess.HasExited) { throw 'Daemon did not start. Check bin/daemon-error.log; another instance may own the pipe.' }
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
    Pop-Location
}
