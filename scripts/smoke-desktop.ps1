$ErrorActionPreference = 'Stop'
$root = Split-Path -Parent $PSScriptRoot
$daemonProcess = $null
$desktopProcess = $null
$previousArguments = $env:WEBVIEW2_ADDITIONAL_BROWSER_ARGUMENTS
Push-Location $root
try {
    New-Item -ItemType Directory -Path (Join-Path $root 'bin') -Force | Out-Null
    go build -o bin/nimbus-daemon.exe ./cmd/nimbus-daemon
    if ($LASTEXITCODE -ne 0) { throw 'Daemon build failed' }
    $daemonProcess = Start-Process -FilePath (Join-Path $root 'bin\nimbus-daemon.exe') -ArgumentList '--development' -WindowStyle Hidden -PassThru -RedirectStandardError (Join-Path $root 'bin\smoke-daemon-error.log')
    Start-Sleep -Milliseconds 400
    if ($daemonProcess.HasExited) { throw 'Cannot start smoke daemon; check bin/smoke-daemon-error.log' }
    # Debugging is enabled only for this test process, never configured in the app.
    $env:WEBVIEW2_ADDITIONAL_BROWSER_ARGUMENTS = '--remote-debugging-port=19222 --remote-debugging-address=127.0.0.1'
    $desktopProcess = Start-Process -FilePath (Join-Path $root 'apps\desktop\src-tauri\target\debug\nimbus-desktop.exe') -WindowStyle Hidden -PassThru -RedirectStandardError (Join-Path $root 'bin\smoke-desktop-error.log')
    $env:WEBVIEW2_ADDITIONAL_BROWSER_ARGUMENTS = $previousArguments
    Set-Location (Join-Path $root 'apps\desktop')
    node e2e/native-smoke.mjs
    if ($LASTEXITCODE -ne 0) { throw 'Native desktop smoke failed' }
} finally {
    $env:WEBVIEW2_ADDITIONAL_BROWSER_ARGUMENTS = $previousArguments
    if ($desktopProcess -and -not $desktopProcess.HasExited) { Stop-Process -Id $desktopProcess.Id }
    if ($daemonProcess -and -not $daemonProcess.HasExited) { Stop-Process -Id $daemonProcess.Id }
    Pop-Location
}
