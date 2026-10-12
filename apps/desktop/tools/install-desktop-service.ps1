param([Parameter(Mandatory=$true)][string]$Manifest,[Parameter(Mandatory=$true)][string]$ExpectedHash,[Parameter(Mandatory=$true)][string]$SafeLog)
$ErrorActionPreference='Stop'
$stage='VERIFY'
try {
    $principal=[Security.Principal.WindowsPrincipal][Security.Principal.WindowsIdentity]::GetCurrent()
    if (-not $principal.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)) { throw 'ADMIN_REQUIRED' }
    if ((Get-FileHash -LiteralPath $Manifest -Algorithm SHA256).Hash -ne $ExpectedHash) { throw 'MANIFEST_CHANGED' }
    $data=Get-Content -LiteralPath $Manifest -Raw | ConvertFrom-Json
    $root=[IO.Path]::GetFullPath((Join-Path $env:ProgramData 'LightflowDesktopDev'))
    if ((Test-Path -LiteralPath $root) -and ((Get-Item -LiteralPath $root).Attributes -band [IO.FileAttributes]::ReparsePoint)) { throw 'UNSAFE_RUNTIME' }
    $service=Get-Service -Name LightflowDesktopDev -ErrorAction SilentlyContinue
    if (-not $service -and (Test-Path -LiteralPath $root)) { throw 'UNTRUSTED_EXISTING_RUNTIME' }
    if ($service) {
        $registered=Get-CimInstance Win32_Service -Filter "Name='LightflowDesktopDev'"
        if ($registered.PathName -ne ('"'+(Join-Path $root 'lightflow-daemon.exe')+'" -root "'+$root+'"')) { throw 'OTHER_SERVICE_OWNS_NAME' }
        Stop-Service -Name LightflowDesktopDev
        $service.WaitForStatus('Stopped',[TimeSpan]::FromSeconds(65))
    }
    New-Item -ItemType Directory -Path $root -Force | Out-Null
    & icacls.exe $root /inheritance:r /grant:r '*S-1-5-18:(OI)(CI)F' '*S-1-5-32-544:(OI)(CI)F' | Out-Null
    if ($LASTEXITCODE -ne 0) { throw 'ACL_FAILED' }
    & icacls.exe $root /setowner '*S-1-5-18' /Q | Out-Null
    if ($LASTEXITCODE -ne 0) { throw 'OWNER_FAILED' }
    $acl=New-Object Security.AccessControl.DirectorySecurity
    $acl.SetAccessRuleProtection($true,$false)
    foreach ($sid in @('S-1-5-18','S-1-5-32-544')) {
        $identity=New-Object Security.Principal.SecurityIdentifier($sid)
        $rule=New-Object Security.AccessControl.FileSystemAccessRule($identity,'FullControl','ContainerInherit,ObjectInherit','None','Allow')
        $acl.AddAccessRule($rule)
    }
    [IO.Directory]::SetAccessControl($root,$acl)
    if (@(Get-ChildItem -LiteralPath $root -Recurse -Force | Where-Object { $_.Attributes -band [IO.FileAttributes]::ReparsePoint }).Count -gt 0) { throw 'UNSAFE_RUNTIME' }
    $stage='COPY'
    foreach ($file in $data.files) {
        if ((Get-FileHash -LiteralPath $file.source -Algorithm SHA256).Hash -ne $file.sha256) { throw 'SOURCE_CHANGED' }
        $target=[IO.Path]::GetFullPath((Join-Path $root $file.target))
        if (-not $target.StartsWith($root+'\',[StringComparison]::OrdinalIgnoreCase)) { throw 'UNSAFE_TARGET' }
        New-Item -ItemType Directory -Path (Split-Path -Parent $target) -Force | Out-Null
        Copy-Item -LiteralPath $file.source -Destination $target -Force
        if ((Get-FileHash -LiteralPath $target -Algorithm SHA256).Hash -ne $file.sha256) { throw 'COPY_CHANGED' }
    }
    $stage='ENROLL'
    $worker=@($data.files | Where-Object target -eq 'm0.exe')[0]
    & (Join-Path $root 'lightflow-daemon.exe') -provision -root $root -control $data.control -owner-sid $data.owner_sid -worker-sha256 $worker.sha256 | Out-Null
    if ($LASTEXITCODE -ne 0) { throw 'ENROLLMENT_FAILED' }
    $stage='OWNER'
    & icacls.exe $root /setowner '*S-1-5-18' /T /Q | Out-Null
    if ($LASTEXITCODE -ne 0) { throw 'OWNER_FAILED' }
    foreach ($child in Get-ChildItem -LiteralPath $root -Force) {
        & icacls.exe $child.FullName /reset /T /Q | Out-Null
        if ($LASTEXITCODE -ne 0) { throw 'CHILD_ACL_FAILED' }
    }
    foreach ($file in $data.files) {
        $target=Join-Path $root $file.target
        if ((Get-FileHash -LiteralPath $target -Algorithm SHA256).Hash -ne $file.sha256) { throw 'INSTALLED_RESOURCE_CHANGED' }
    }
    $stage='START'
    if (-not $service) {
        New-Service -Name LightflowDesktopDev -DisplayName 'Lightflow desktop development network service' -BinaryPathName ('"'+(Join-Path $root 'lightflow-daemon.exe')+'" -root "'+$root+'"') -StartupType Manual | Out-Null
    }
    Start-Service -Name LightflowDesktopDev
    (Get-Service LightflowDesktopDev).WaitForStatus('Running',[TimeSpan]::FromSeconds(15))
    'REAL_DESKTOP_SERVICE_READY' | Set-Content -LiteralPath $SafeLog -Encoding UTF8
} catch {
    ('SERVICE_INSTALL_FAILED_'+$stage) | Set-Content -LiteralPath $SafeLog -Encoding UTF8
    exit 1
}
