@echo off
setlocal
cd /d "%~dp0"
echo Starting Lightflow desktop - PRODUCTION profile.
echo API: https://lightflow.aibusinesses.cc
echo Gateway: lightflow-gw.aibusinesses.cc (port supplied by API)
echo UI SIMULATION ONLY: real desktop login and VPN are not implemented.
echo Please keep this window open. The first build may take several minutes.
powershell.exe -NoLogo -NoProfile -ExecutionPolicy Bypass -File "%~dp0scripts\dev.ps1" -Profile Production
if errorlevel 1 (
    echo.
    echo Startup failed. Please share the error message above.
    pause
)
endlocal
