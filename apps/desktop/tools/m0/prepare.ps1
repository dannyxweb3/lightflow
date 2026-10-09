$ErrorActionPreference = 'Stop'
$repo = (Resolve-Path (Join-Path $PSScriptRoot '../../../..')).Path
$assets = Join-Path $repo '.local/m0/assets'
$lock = Get-Content (Join-Path $PSScriptRoot 'assets.lock.json') -Raw | ConvertFrom-Json
New-Item -ItemType Directory -Path $assets -Force | Out-Null
foreach ($asset in $lock.assets) {
    $path = Join-Path $assets $asset.name
    if (-not (Test-Path -LiteralPath $path)) { Invoke-WebRequest -Uri $asset.url -OutFile $path }
    if ((Get-FileHash -LiteralPath $path -Algorithm SHA256).Hash.ToLowerInvariant() -ne $asset.sha256) {
        throw "Asset hash mismatch: $($asset.name). Refusing to use a changed release."
    }
}
$core = Join-Path $assets 'mihomo-windows-amd64.exe'
if (-not (Test-Path -LiteralPath $core)) { Expand-Archive -LiteralPath (Join-Path $assets 'mihomo-v1.19.32.zip') -DestinationPath $assets }
if ((Get-FileHash -LiteralPath $core -Algorithm SHA256).Hash.ToLowerInvariant() -ne $lock.core_executable_sha256) { throw 'Core executable hash mismatch.' }
Write-Host "Verified Mihomo $($lock.core_version) and locked rule assets."
