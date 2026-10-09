@echo off
setlocal
echo Lightflow M0 test probe. This does not enable VPN or TUN.
powershell.exe -NoProfile -ExecutionPolicy Bypass -File "%~dp0run.ps1"
pause
