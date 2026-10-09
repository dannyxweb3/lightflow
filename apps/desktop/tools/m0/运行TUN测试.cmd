@echo off
setlocal
echo Lightflow isolated-machine TUN experiment. Network changes last only for this test.
powershell.exe -NoProfile -ExecutionPolicy Bypass -File "%~dp0run-tun.ps1"
pause
