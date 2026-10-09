param(
    [ValidateSet('Before','Gateway','Route','After')][string]$Operation,
    [Parameter(Mandatory=$true)][string]$Directory,
    [Parameter(Mandatory=$true)][ValidatePattern('^LightflowM0-[a-f0-9]{12}$')][string]$Device,
    [string]$TargetIP
)
$ErrorActionPreference = 'Stop'
function Snapshot {
    # Exclude lifetimes/counters. Include all pre-existing interface, DNS and route state.
    $adapters = @(Get-NetAdapter | Where-Object Name -ne $Device | Sort-Object ifIndex | Select-Object ifIndex,Name,InterfaceGuid,Status)
    $indices = @($adapters | ForEach-Object ifIndex)
    $addresses = @(Get-NetIPAddress | Where-Object { $_.InterfaceIndex -in $indices } | Sort-Object InterfaceIndex,IPAddress | Select-Object InterfaceIndex,IPAddress,PrefixLength,PrefixOrigin)
    $dns = @(Get-DnsClientServerAddress | Where-Object { $_.InterfaceIndex -in $indices } | Sort-Object InterfaceIndex,AddressFamily | Select-Object InterfaceIndex,AddressFamily,ServerAddresses)
    $routes = @(Get-NetRoute | Where-Object { $_.InterfaceIndex -in $indices } | Sort-Object InterfaceIndex,DestinationPrefix,NextHop | Select-Object InterfaceIndex,DestinationPrefix,NextHop,RouteMetric,Protocol)
    [ordered]@{adapters=$adapters;addresses=$addresses;dns=$dns;routes=$routes} | ConvertTo-Json -Depth 6 -Compress
}
try {
    $directoryPath = (Resolve-Path -LiteralPath $Directory).Path
    $expectedRoot = [IO.Path]::GetFullPath($env:ProgramData).TrimEnd('\') + '\'
    if (-not $directoryPath.StartsWith($expectedRoot,[StringComparison]::OrdinalIgnoreCase) -or
        (Split-Path $directoryPath -Leaf) -notlike 'LightflowM0-*') { throw 'PRIVATE_PATH_INVALID' }
    $baseline = Join-Path $directoryPath 'network-before.json'
    if ($Operation -eq 'Before') {
        if (Get-NetAdapter -Name $Device -ErrorAction SilentlyContinue) { throw 'OWNED_NAME_ALREADY_EXISTS' }
        $activeVPN = @(Get-NetAdapter | Where-Object { $_.Status -eq 'Up' -and $_.InterfaceDescription -match 'Wintun|WireGuard|TAP-Windows|OpenVPN|Tailscale' })
        if ($activeVPN.Count -gt 0) { throw 'OTHER_VPN_ACTIVE' }
        $collision = @(Get-NetIPAddress | Where-Object { $_.IPAddress -like '198.18.*' -or $_.IPAddress -like '198.19.*' -or $_.IPAddress -like 'fdfe:dcba:9876:*' })
        $collision += @(Get-NetRoute | Where-Object { $_.DestinationPrefix -like '198.18.*' -or $_.DestinationPrefix -like '198.19.*' -or $_.DestinationPrefix -like 'fdfe:dcba:9876:*' })
        if ($collision.Count -gt 0) { throw 'TUN_ADDRESS_CONFLICT' }
        [IO.File]::WriteAllText($baseline,(Snapshot))
        Write-Output 'NETWORK_BASELINE_RECORDED'
    } elseif ($Operation -eq 'Gateway') {
        $gatewayRoute = @(Find-NetRoute -RemoteIPAddress '192.168.194.128')
        $gatewayAdapter = @(Get-NetAdapter | Where-Object { $_.ifIndex -eq $gatewayRoute[0].InterfaceIndex -and $_.Status -eq 'Up' -and $_.Name -ne $Device })
        if ($gatewayAdapter.Count -ne 1) { throw 'GATEWAY_INTERFACE_UNAVAILABLE' }
        [IO.File]::WriteAllText((Join-Path $directoryPath 'gateway-interface.txt'),$gatewayAdapter[0].Name)
        Write-Output 'GATEWAY_INTERFACE_SELECTED'
    } elseif ($Operation -eq 'Route') {
        if (-not [Net.IPAddress]::TryParse($TargetIP,[ref]([Net.IPAddress]$null))) { throw 'INVALID_TEST_IP' }
        $adapter = @(Get-NetAdapter | Where-Object Name -eq $Device)
        if (-not $adapter) { throw 'TUN_ADAPTER_NOT_CREATED' }
        $route = @(Find-NetRoute -RemoteIPAddress $TargetIP)
        if ($adapter.Status -ne 'Up' -or -not ($route | Where-Object InterfaceIndex -eq $adapter.ifIndex)) { throw 'TUN_ROUTE_NOT_SELECTED' }
        try { $v6route = @(Find-NetRoute -RemoteIPAddress '2606:4700:4700::1111') } catch { throw 'IPV6_ROUTE_NOT_INTERCEPTED' }
        if (-not ($v6route | Where-Object InterfaceIndex -eq $adapter.ifIndex)) { throw 'IPV6_ROUTE_NOT_INTERCEPTED' }
        Write-Output 'TUN_IPV4_AND_IPV6_ROUTES_SELECTED'
    } else {
        $timer = [Diagnostics.Stopwatch]::StartNew()
        do {
            $leftover = @(Get-NetAdapter -Name $Device -ErrorAction SilentlyContinue)
            $same = (Snapshot) -ceq [IO.File]::ReadAllText($baseline)
            if ($leftover.Count -eq 0 -and $same) {
                [IO.File]::WriteAllText((Join-Path $directoryPath 'network-after.json'),(Snapshot))
                Write-Output ('NETWORK_CHECK_MS=' + $timer.ElapsedMilliseconds)
                exit 0
            }
            Start-Sleep -Milliseconds 200
        } while ($timer.Elapsed.TotalSeconds -lt 8)
        [IO.File]::WriteAllText((Join-Path $directoryPath 'network-after.json'),(Snapshot))
        throw 'NETWORK_RECOVERY_MISMATCH'
    }
} catch {
    # Never print arbitrary exception text or network configuration.
    $codes = @('PRIVATE_PATH_INVALID','OWNED_NAME_ALREADY_EXISTS','OTHER_VPN_ACTIVE','TUN_ADDRESS_CONFLICT','GATEWAY_INTERFACE_UNAVAILABLE','INVALID_TEST_IP','TUN_ADAPTER_NOT_CREATED','TUN_ROUTE_NOT_SELECTED','IPV6_ROUTE_NOT_INTERCEPTED','NETWORK_RECOVERY_MISMATCH')
    $code = $_.Exception.Message
    if ($code -notin $codes) {
        $errorId = $_.FullyQualifiedErrorId
        if ($errorId -notmatch '^[A-Za-z0-9.,_-]{1,256}$') { $errorId = 'UNCLASSIFIED_SCRIPT_ERROR' }
        [IO.File]::WriteAllText((Join-Path $directoryPath 'network-error-id.txt'),$errorId)
        [IO.File]::WriteAllText((Join-Path $PSScriptRoot '../../../../.local/m0/network-error-id.safe.txt'),($errorId + ';LINE=' + $_.InvocationInfo.ScriptLineNumber))
    }
    if ($code -notin $codes) { $code = 'NETWORK_INSPECTION_FAILED' }
    Write-Output $code
    exit 1
}
