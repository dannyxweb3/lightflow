# Isolated capture calibration. No TUN, credentials or HTTP requests are used.
$ErrorActionPreference = 'Stop'
[Console]::OutputEncoding = New-Object Text.UTF8Encoding($false)
$OutputEncoding = [Console]::OutputEncoding
$repo = (Resolve-Path (Join-Path $PSScriptRoot '../../../..')).Path
$directory = Join-Path $repo ('.local/m0/evidence/capture-calibration-' + [Guid]::NewGuid().ToString('N').Substring(0,12))
$name = 'LightflowM0Cal-' + [Guid]::NewGuid().ToString('N').Substring(0,12)
$started = $false
$added = $false
try {
    $identity = [Security.Principal.WindowsIdentity]::GetCurrent()
    $principal = [Security.Principal.WindowsPrincipal]$identity
    if (-not $principal.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)) { throw 'ADMIN_REQUIRED' }
    New-Item -ItemType Directory -Path $directory | Out-Null
    & icacls.exe $directory /inheritance:r /grant:r ('*{0}:(OI)(CI)F' -f $identity.User.Value) '*S-1-5-18:(OI)(CI)F' | Out-Null
    if ($LASTEXITCODE -ne 0) { throw 'ACL_FAILED' }
    $status = (& pktmon.exe status 2>&1 | Out-String)
    $filters = (& pktmon.exe filter list 2>&1 | Out-String)
    [IO.File]::WriteAllText((Join-Path $directory 'initial-status.txt'),$status)
    [IO.File]::WriteAllText((Join-Path $directory 'initial-filters.txt'),$filters)
    $stoppedChinese = -join ([char[]]@(0x6ca1,0x6709,0x8fd0,0x884c))
    $emptyChinese = (-join ([char[]]@(0x6570,0x636e,0x5305,0x7b5b,0x9009,0x5668))) + ': ' + [char]0x65e0
    if ($status -notmatch 'Stopped' -and -not $status.Contains($stoppedChinese)) { throw 'CAPTURE_BUSY_OR_UNKNOWN' }
    if ($filters -notmatch 'No filters' -and ($filters -replace '\s+',' ').Trim() -ne $emptyChinese) { throw 'FILTERS_BUSY_OR_UNKNOWN' }
    & pktmon.exe filter add $name -i 1.1.1.1 -t TCP -p 443 | Out-File (Join-Path $directory 'filter.txt')
    if ($LASTEXITCODE -ne 0) { throw 'FILTER_FAILED' }
    $added = $true
    & pktmon.exe start --capture --comp all --flags '0x032' --pkt-size 128 --file-size 16 --file-name (Join-Path $directory 'packets.etl') | Out-File (Join-Path $directory 'start.txt')
    if ($LASTEXITCODE -ne 0) { throw 'START_FAILED' }
    $started = $true
    & pktmon.exe status | Out-File (Join-Path $directory 'status.txt')
    $tcp = New-Object Net.Sockets.TcpClient
    try { $pending = $tcp.ConnectAsync('1.1.1.1',443); if (-not $pending.Wait(5000)) { throw 'TCP_TIMEOUT' }; if (-not $tcp.Connected) { throw 'TCP_FAILED' } } finally { $tcp.Dispose() }
    Start-Sleep -Milliseconds 500
    & pktmon.exe stop | Out-File (Join-Path $directory 'stop.txt')
    if ($LASTEXITCODE -ne 0) { throw 'STOP_FAILED' }
    $started = $false
    & pktmon.exe etl2pcap (Join-Path $directory 'packets.etl') --out (Join-Path $directory 'packets.pcapng') | Out-File (Join-Path $directory 'convert.txt')
    if ($LASTEXITCODE -ne 0) { throw 'CONVERT_FAILED' }
    $length = (Get-Item -LiteralPath (Join-Path $directory 'packets.pcapng')).Length
    $result = 'CALIBRATION_PCAP_BYTES=' + $length
    $result | Set-Content -LiteralPath (Join-Path $repo '.local/m0/calibration.safe.log')
    Write-Output $result
} catch {
    $code = $_.Exception.Message
    if ($code -notin @('ADMIN_REQUIRED','ACL_FAILED','CAPTURE_BUSY_OR_UNKNOWN','FILTERS_BUSY_OR_UNKNOWN','FILTER_FAILED','START_FAILED','TCP_TIMEOUT','TCP_FAILED','STOP_FAILED','CONVERT_FAILED')) { $code='SHELL_OPERATION_FAILED' }
    ('CAPTURE_CALIBRATION_FAILED_' + $code) | Set-Content -LiteralPath (Join-Path $repo '.local/m0/calibration.safe.log')
    exit 1
} finally {
    if ($started) { & pktmon.exe stop | Out-Null }
    if ($added) { & pktmon.exe filter remove $name | Out-Null }
}
