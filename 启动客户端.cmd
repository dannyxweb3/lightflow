@echo off
setlocal
cd /d "%~dp0"
echo Starting Nimbus desktop. Please keep this window open.
echo The first build may take several minutes.
powershell.exe -NoLogo -NoProfile -ExecutionPolicy Bypass -File "%~dp0scripts\dev.ps1"
if errorlevel 1 (
    echo.
    echo Startup failed. Please share the error message above.
    pause
)
endlocal
