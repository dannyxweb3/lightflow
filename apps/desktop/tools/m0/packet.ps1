param([Parameter(Mandatory=$true)][string]$EncodedArguments)
$ErrorActionPreference = 'Stop'
[Console]::OutputEncoding = New-Object Text.UTF8Encoding($false)
$arguments = @([Text.Encoding]::UTF8.GetString([Convert]::FromBase64String($EncodedArguments)) | ConvertFrom-Json)
& pktmon.exe @arguments
exit $LASTEXITCODE
