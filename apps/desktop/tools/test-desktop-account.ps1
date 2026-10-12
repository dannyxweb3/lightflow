param([Parameter(Mandatory=$true)][string]$AccountFile,[switch]$VerifyConnection)
$ErrorActionPreference='Stop'
Add-Type -TypeDefinition @'
using System;
using System.Runtime.InteropServices;
public static class LightflowAccountPipeIdentity {
 [DllImport("kernel32.dll", SetLastError=true)]
 public static extern bool GetNamedPipeServerProcessId(IntPtr pipe, out uint pid);
}
'@
# Explicit local developer acceptance: credentials are read in-process, never arguments or output.
function Invoke-Desktop([string]$method,$params) {
    $pipe=[IO.Pipes.NamedPipeClientStream]::new('.', 'lightflow-desktop-real-v1', [IO.Pipes.PipeDirection]::InOut)
    try {
        $pipe.Connect(3000)
        [uint32]$serverPid=0
        $service=Get-CimInstance Win32_Service -Filter "Name='LightflowDesktopDev'"
        if (-not [LightflowAccountPipeIdentity]::GetNamedPipeServerProcessId($pipe.SafePipeHandle.DangerousGetHandle(),[ref]$serverPid) -or $service.State -ne 'Running' -or $service.ProcessId -ne $serverPid) { throw 'UNTRUSTED_DAEMON' }
        $writer=[IO.StreamWriter]::new($pipe,[Text.UTF8Encoding]::new($false),4096,$true)
        $reader=[IO.StreamReader]::new($pipe)
        $writer.AutoFlush=$true
        $writer.WriteLine((@{api_version=1;request_id=[guid]::NewGuid().ToString();method=$method;params=$params} | ConvertTo-Json -Depth 5 -Compress))
        $response=$reader.ReadLine() | ConvertFrom-Json
        if ($response.error) { throw 'DESKTOP_ACCOUNT_RPC_FAILED' }
        return $response.result
    } finally { $pipe.Dispose() }
}
function Wait-Account {
    $until=[DateTime]::UtcNow.AddSeconds(80)
    do {
        $state=Invoke-Desktop 'get_snapshot' @{}
        if (-not $state.account.busy) {
            if ($state.last_error) { throw 'DESKTOP_ACCOUNT_OPERATION_FAILED' }
            return $state
        }
        Start-Sleep -Milliseconds 250
    } while ([DateTime]::UtcNow -lt $until)
    throw 'DESKTOP_ACCOUNT_TIMEOUT'
}
function Wait-Connection([string]$expected) {
    $until=[DateTime]::UtcNow.AddSeconds(80)
    do {
        $state=Invoke-Desktop 'get_snapshot' @{}
        if ($state.last_error) { throw 'DESKTOP_CONNECTION_OPERATION_FAILED' }
        if ($state.state -eq $expected) { return }
        Start-Sleep -Milliseconds 500
    } while ([DateTime]::UtcNow -lt $until)
    throw 'DESKTOP_CONNECTION_TIMEOUT'
}
function Read-TestEgress {
    $value=& curl.exe --noproxy '*' --fail --silent --max-time 20 https://api.ipify.org
    if ($LASTEXITCODE -ne 0 -or $value -notmatch '^\d{1,3}(\.\d{1,3}){3}$') { throw 'EGRESS_PROBE_FAILED' }
    return $value
}
$credentials=Get-Content -LiteralPath $AccountFile -Raw | ConvertFrom-Json
try {
    $before=Wait-Account
    if ($before.simulation) { throw 'REAL_SERVICE_REQUIRED' }
    $count=$before.account.device_count
    $email=$before.account.email
    Invoke-Desktop 'logout' @{} | Out-Null
    $signedOut=Wait-Account
    if ($signedOut.account.authenticated -or $signedOut.state -ne 'disconnected') { throw 'LOGOUT_FAILED' }
    Invoke-Desktop 'login' @{email=$credentials.email;password=$credentials.password} | Out-Null
    $signedIn=Wait-Account
    if (-not $signedIn.account.authenticated -or $signedIn.account.email -ne $credentials.email -or -not $signedIn.account.checked_at) { throw 'LOGIN_FAILED' }
    if ($email -eq $credentials.email -and $signedIn.account.device_count -ne $count) { throw 'DEVICE_REUSE_FAILED' }
    Invoke-Desktop 'refresh_account' @{} | Out-Null
    $refreshed=Wait-Account
    if (-not $refreshed.account.authenticated) { throw 'ACCOUNT_REFRESH_FAILED' }
    'REAL_ACCOUNT_LOGOUT_LOGIN_REFRESH_AND_DEVICE_REUSE_PASS'
    if ($VerifyConnection) {
        $baseline=Read-TestEgress
        try {
            Invoke-Desktop 'connect' @{country_code=''} | Out-Null
            Wait-Connection 'connected'
            $proxied=Read-TestEgress
            if ($proxied -eq $baseline) { throw 'EGRESS_DID_NOT_CHANGE' }
        } finally {
            Invoke-Desktop 'logout' @{} | Out-Null
            $signedOut=Wait-Account
            if ($signedOut.account.authenticated) { throw 'CONNECTED_LOGOUT_FAILED' }
            Wait-Connection 'disconnected'
        }
        $restored=Read-TestEgress
        if ($restored -ne $baseline) { throw 'EGRESS_NOT_RESTORED' }
        Invoke-Desktop 'login' @{email=$credentials.email;password=$credentials.password} | Out-Null
        $signedIn=Wait-Account
        if (-not $signedIn.account.authenticated -or $signedIn.account.device_count -ne $refreshed.account.device_count) { throw 'RESTORE_LOGIN_FAILED' }
        'REAL_LOGIN_CONNECT_EGRESS_CONNECTED_LOGOUT_AND_RELOGIN_PASS'
    }
} finally { $credentials.password=''; $credentials=$null }
