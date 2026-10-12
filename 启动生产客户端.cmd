@echo off
setlocal
cd /d "%~dp0"
echo Starting Lightflow desktop - PRODUCTION profile.
echo API: https://lightflow.aibusinesses.cc
echo Gateway: lightflow-gw.aibusinesses.cc (port supplied by API)
echo REAL DESKTOP DEVELOPMENT MODE: connection uses the Windows network service.
echo Please keep this window open. The first build may take several minutes.
powershell.exe -NoLogo -NoProfile -ExecutionPolicy Bypass -File "%~dp0apps\desktop\tools\desktop-real.ps1"
if errorlevel 1 (
    echo.
    echo Startup failed. Please share the error message above.
    pause
)
endlocal
