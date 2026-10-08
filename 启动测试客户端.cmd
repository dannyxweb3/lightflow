@echo off
setlocal
cd /d "%~dp0"
echo Starting Lightflow desktop - TEST profile.
echo API: https://192.168.194.128:8443
echo Gateway: 192.168.194.128:4433/udp
echo UI SIMULATION ONLY: real desktop login and VPN are not implemented.
echo Please keep this window open. The first build may take several minutes.
powershell.exe -NoLogo -NoProfile -ExecutionPolicy Bypass -File "%~dp0scripts\dev.ps1" -Profile Test
if errorlevel 1 (
    echo.
    echo Startup failed. Please share the error message above.
    pause
)
endlocal
