@echo off
(
 cd /d "%TEMP%"
 if errorlevel 1 exit /b 1
 powershell.exe -NoProfile -ExecutionPolicy Bypass -File "%~dp0install.ps1" %*
 if errorlevel 1 (
  pause
  exit /b 1
 )
 exit /b 0
)
