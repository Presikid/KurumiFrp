# Permanently remove this user's KurumiFRP installation, not OS/browser records.
[CmdletBinding()]
param([switch]$Yes)
$ErrorActionPreference = 'Stop'
$env:PSModulePath = [IO.Path]::Combine($PSHOME, 'Modules') + [IO.Path]::PathSeparator + $env:PSModulePath
Set-StrictMode -Version Latest
function Assert-Plain([string]$Path) {
 $p = [IO.Path]::GetFullPath($Path)
 while ($p) {
  $item = Get-Item -LiteralPath $p -Force -ErrorAction SilentlyContinue
  if ($item -and ($item.Attributes -band [IO.FileAttributes]::ReparsePoint)) { throw "Linked path refused: $p" }
  $parent = [IO.Path]::GetDirectoryName($p)
  if ($parent -eq $p) { break }; $p = $parent
 }
}
# Delete a reparse point itself. Never traverse junctions/symlinks into other data.
function Remove-Tree([string]$Path) {
 $item = Get-Item -LiteralPath $Path -Force -ErrorAction SilentlyContinue
 if (!$item) { return }
 if ($item.Attributes -band [IO.FileAttributes]::ReparsePoint) {
  if ($item.Attributes -band [IO.FileAttributes]::Directory) { [IO.Directory]::Delete($Path) } else { [IO.File]::Delete($Path) }
 } elseif ($item.PSIsContainer) {
  foreach ($child in @($item.GetFileSystemInfos())) { Remove-Tree $child.FullName }
  [IO.Directory]::Delete($Path)
 } else {
  if ($item.IsReadOnly) { $item.IsReadOnly = $false }
  [IO.File]::Delete($Path)
 }
}
function Remove-Retry([string]$Path) {
 for ($i=0; $i -lt 40; $i++) {
  try { Remove-Tree $Path; return } catch { if ($i -eq 39) { throw }; Start-Sleep -Milliseconds 100 }
 }
}
$root = [IO.Path]::GetFullPath((Join-Path $env:LOCALAPPDATA 'KurumiFRP'))
$registry = 'HKCU:\Software\Microsoft\Windows\CurrentVersion\Uninstall\KurumiFRP'
Assert-Plain $root
if (!(Test-Path -LiteralPath $root)) { Write-Host 'KurumiFRP is not installed for this user.'; exit 0 }
foreach ($name in @('install-receipt.json','data','data\manager.lock','.uninstalling','kurumi.exe')) { Assert-Plain (Join-Path $root $name) }
$receipt = Get-Content -LiteralPath (Join-Path $root 'install-receipt.json') -Raw | ConvertFrom-Json
if ($receipt.product -ne 'io.github.Presikid.KurumiFrp' -or $receipt.schema -ne 1 -or $receipt.root -ne $root) { throw 'Installation ownership not verified. Nothing removed.' }
$allowed = @()
foreach ($kind in @('Desktop','Programs','Startup')) {
 $folder = [Environment]::GetFolderPath($kind)
 if ($folder) { $allowed += (Join-Path $folder 'KurumiFRP.lnk') }
}
$wsh = New-Object -ComObject WScript.Shell
foreach ($record in @($receipt.links)) {
 if ($allowed -notcontains $record.path) { throw 'Unexpected shortcut path in receipt.' }
 Assert-Plain $record.path
 if (Test-Path -LiteralPath $record.path) {
  $link = $wsh.CreateShortcut($record.path)
  if ($link.TargetPath -ne $record.target -or $link.Arguments -ne $record.arguments -or !$link.Arguments.Contains((Join-Path $root 'Launch.ps1'))) { throw "Modified shortcut retained: $($record.path)" }
 }
}
if ((Test-Path -LiteralPath $registry) -and (Get-ItemProperty -LiteralPath $registry).InstallLocation -ne $root) { throw 'Unrelated uninstall registry entry retained.' }
if (!$Yes) {
 Write-Host "This permanently deletes $root, ALL keys, configurations, cores, logs and startup entries."
 if ((Read-Host 'Type UNINSTALL to continue') -cne 'UNINSTALL') { Write-Host 'Cancelled. Nothing removed.'; exit 0 }
}
$guard = Join-Path $root '.maintenance'
if (Test-Path -LiteralPath $guard) { throw 'Another install/uninstall is active.' }
New-Item -ItemType Directory -Path $guard | Out-Null
try {
 Set-Content -LiteralPath (Join-Path $root '.uninstalling') -Value 'uninstalling' -Encoding ASCII
 $exe = Join-Path $root 'kurumi.exe'
 if (Test-Path -LiteralPath $exe) {
  & $exe --data (Join-Path $root 'data') --stop
  if ($LASTEXITCODE -ne 0) { throw 'Shutdown not confirmed. Installation retained; no process was killed by name.' }
 } elseif (Test-Path -LiteralPath (Join-Path $root 'data\manager.lock')) { throw 'Binary missing but process state unresolved. Files retained.' }
 Set-Location -LiteralPath $env:TEMP
 [Environment]::CurrentDirectory = $env:TEMP
 # Keep the uninstaller and ownership receipt until other files are gone, so a
 # sharing violation cannot silently destroy the retry path.
 foreach ($entry in @(Get-ChildItem -LiteralPath $root -Force)) {
  if ($entry.Name -notin @('uninstall.ps1','Uninstall.cmd','install-receipt.json','.uninstalling','.maintenance')) { Remove-Retry $entry.FullName }
 }
 foreach ($record in @($receipt.links)) {
  if (Test-Path -LiteralPath $record.path) { Remove-Item -LiteralPath $record.path -Force }
 }
 if (Test-Path -LiteralPath $registry) { Remove-Item -LiteralPath $registry -Recurse -Force }
 foreach ($name in @('Uninstall.cmd','install-receipt.json','uninstall.ps1','.uninstalling','.maintenance')) { Remove-Retry (Join-Path $root $name) }
 Remove-Retry $root
 if (Test-Path -LiteralPath $root) { throw 'Uninstall incomplete: application directory remains.' }
 Write-Host 'Removed KurumiFRP files, keys, cores, logs, shortcuts, startup and uninstall registration.'
 Write-Host 'System logs, browser history, restore points, backups and user exports were not altered.'
} finally {
 if (Test-Path -LiteralPath $root) {
  Remove-Item -LiteralPath (Join-Path $root '.uninstalling') -Force -ErrorAction SilentlyContinue
  if (Test-Path -LiteralPath $guard) { Remove-Item -LiteralPath $guard -Force -ErrorAction SilentlyContinue }
 }
}
