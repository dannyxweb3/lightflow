param([switch]$InstallOnly)
$ErrorActionPreference='Stop'
$repo=(Resolve-Path (Join-Path $PSScriptRoot '../../..')).Path
$configPath=Join-Path $repo '.local/desktop-production.json'
if (-not (Test-Path -LiteralPath $configPath)) { throw 'Missing local desktop-production.json with control field.' }
$config=Get-Content -LiteralPath $configPath -Raw | ConvertFrom-Json
if ($config.control -notmatch '^https://[a-zA-Z0-9.-]+(:[0-9]+)?$') { throw 'Invalid HTTPS origin.' }
$build=Join-Path $repo '.local/desktop-build'
$runtime=Join-Path $env:ProgramData 'LightflowDesktopDev'
Push-Location $repo
try {
    & (Join-Path $PSScriptRoot 'm0/prepare.ps1')
    New-Item -ItemType Directory -Path $build -Force | Out-Null
    & go build -o (Join-Path $build 'lightflow-daemon.exe') ./cmd/lightflow-daemon
    if ($LASTEXITCODE -ne 0) { throw 'Service build failed.' }
    & go build -o (Join-Path $build 'm0.exe') ./apps/desktop/tools/m0
    if ($LASTEXITCODE -ne 0) { throw 'Worker build failed.' }
    $files=@(
        @{source=(Join-Path $build 'lightflow-daemon.exe');target='lightflow-daemon.exe'},
        @{source=(Join-Path $build 'm0.exe');target='m0.exe'}
    )
    foreach ($name in @('assets.lock.json','baseline.json','tun.json','network.ps1')) {
        $files+=@{source=(Join-Path $PSScriptRoot ('m0/'+$name));target=('apps/desktop/tools/m0/'+$name)}
    }
    foreach ($name in @('mihomo-v1.19.32.zip','mihomo-windows-amd64.exe','geoip.dat','geosite.dat')) {
        $files+=@{source=(Join-Path $repo ('.local/m0/assets/'+$name));target=('.local/m0/assets/'+$name)}
    }
    foreach ($file in $files) { $file.sha256=(Get-FileHash -LiteralPath $file.source -Algorithm SHA256).Hash }
    $manifest=Join-Path $build 'install.json'
    @{files=$files;control=$config.control;owner_sid=[Security.Principal.WindowsIdentity]::GetCurrent().User.Value} | ConvertTo-Json -Depth 5 | Set-Content -LiteralPath $manifest -Encoding UTF8
    $hash=(Get-FileHash -LiteralPath $manifest -Algorithm SHA256).Hash
    $installer=Join-Path $PSScriptRoot 'install-desktop-service.ps1'
    $log=Join-Path $build 'install.safe.log'
    $revisionText=(($files | Sort-Object target | ForEach-Object { $_.target+':'+$_.sha256 }) -join ';')+';'+$config.control+';'+[Security.Principal.WindowsIdentity]::GetCurrent().User.Value+';'+(Get-FileHash -LiteralPath $installer -Algorithm SHA256).Hash
    $sha=[Security.Cryptography.SHA256]::Create()
    try { $revision=([BitConverter]::ToString($sha.ComputeHash([Text.Encoding]::UTF8.GetBytes($revisionText)))).Replace('-','') } finally { $sha.Dispose() }
    $revisionFile=Join-Path $build 'installed.revision'
    $service=Get-Service -Name LightflowDesktopDev -ErrorAction SilentlyContinue
    $current=$service -and $service.Status -eq 'Running' -and (Test-Path -LiteralPath $revisionFile) -and (Get-Content -LiteralPath $revisionFile -Raw).Trim() -eq $revision
    if (-not $current) {
    '' | Set-Content -LiteralPath $log
    $arguments='-NoProfile -ExecutionPolicy Bypass -File "'+$installer+'" -Manifest "'+$manifest+'" -ExpectedHash '+$hash+' -SafeLog "'+$log+'"'
    Write-Host 'Installing the real desktop network service. Accept the Windows UAC prompt.'
    $process=Start-Process powershell.exe -Verb RunAs -WindowStyle Hidden -ArgumentList $arguments -PassThru
    $handle=$process.Handle
    while (-not $process.HasExited) { Start-Sleep -Milliseconds 500; $process.Refresh() }
    Get-Content -LiteralPath $log
    if ($process.ExitCode -ne 0) { throw 'Real desktop service installation failed.' }
    $revision | Set-Content -LiteralPath $revisionFile -Encoding ASCII
    } else { Write-Host 'Using the installed real desktop network service.' }
    if ($InstallOnly) { return }
    $env:LIGHTFLOW_REAL_DESKTOP='1'
    $env:VITE_LIGHTFLOW_PROFILE='production'
    $cargo=Join-Path $env:USERPROFILE '.cargo/bin'
    if (Test-Path -LiteralPath $cargo) { $env:PATH=$cargo+';'+$env:PATH }
    Set-Location (Join-Path $repo 'apps/desktop')
    & npm run tauri -- dev
    if ($LASTEXITCODE -ne 0) { throw 'Desktop startup failed.' }
} finally { Pop-Location }
