function Find-ProjectDevelopmentDaemon {
    param([string]$ExecutablePath)
    $currentSid = [System.Security.Principal.WindowsIdentity]::GetCurrent().User.Value
    $processes = @(Get-CimInstance Win32_Process -Filter "Name='nimbus-daemon.exe'")
    foreach ($process in $processes) {
        if ($process.ExecutablePath -ne $ExecutablePath -or $process.CommandLine -notmatch '(?:^|\s)--development(?:\s|$)') { continue }
        $owner = Invoke-CimMethod -InputObject $process -MethodName GetOwnerSid
        if ($owner.ReturnValue -eq 0 -and $owner.Sid -eq $currentSid) { return $process }
    }
    return $null
}

function Test-DevelopmentDaemonReady {
    $pipe = $null
    $reader = $null
    $writer = $null
    try {
        $pipe = New-Object System.IO.Pipes.NamedPipeClientStream('.', 'nimbus-vpn-dev-v1', [System.IO.Pipes.PipeDirection]::InOut, [System.IO.Pipes.PipeOptions]::Asynchronous)
        $pipe.Connect(600)
        $utf8 = New-Object System.Text.UTF8Encoding($false)
        $writer = New-Object System.IO.StreamWriter($pipe, $utf8, 1024, $true)
        $reader = New-Object System.IO.StreamReader($pipe, $utf8, $false, 1024, $true)
        $requestId = [guid]::NewGuid().ToString()
        $request = @{ api_version = 1; request_id = $requestId; method = 'get_snapshot'; params = @{} } | ConvertTo-Json -Compress
        $writer.WriteLine($request)
        $writer.Flush()
        $readTask = $reader.ReadLineAsync()
        if (-not $readTask.Wait(1500)) { return $false }
        $response = $readTask.Result | ConvertFrom-Json
        return ($response.api_version -eq 1 -and $response.request_id -eq $requestId -and $response.result.simulation -eq $true -and -not $response.error)
    } catch {
        return $false
    } finally {
        if ($reader) { $reader.Dispose() }
        if ($writer) { $writer.Dispose() }
        if ($pipe) { $pipe.Dispose() }
    }
}
