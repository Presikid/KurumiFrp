@echo off
cd /d "%TEMP%"
if errorlevel 1 exit /b 1
rem End batch input before PowerShell removes this file; keep the caller shell.
(goto) 2>nul & powershell.exe -NoProfile -ExecutionPolicy Bypass -File "%~dp0uninstall.ps1" %*
