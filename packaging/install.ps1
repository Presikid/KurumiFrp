# Install the extracted release for the current Windows user. No administrator rights required.
[CmdletBinding()]
param([switch]$NoOpen, [switch]$NoAutostart)
$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest
$source = $PSScriptRoot
$arch = [System.Runtime.InteropServices.RuntimeInformation]::OSArchitecture.ToString().ToLowerInvariant()
if ($arch -eq 'x64') { $arch = 'amd64' }
if ($arch -notin @('amd64','arm64')) { throw 'Unsupported Windows architecture.' }
if ((Get-Content -LiteralPath (Join-Path $source 'PLATFORM') -Raw).Trim() -ne "windows_$arch") { throw 'Download the package matching this Windows machine.' }
$exe = Join-Path $source 'kurumi.exe'
$expected = (Get-Content -LiteralPath (Join-Path $source 'SHA256SUMS') -Raw).Trim()
if ($expected -notmatch '^([a-f0-9]{64})  kurumi\.exe$') { throw 'Invalid package checksum manifest.' }
if ((Get-FileHash -Algorithm SHA256 -LiteralPath $exe).Hash.ToLowerInvariant() -ne $Matches[1]) { throw 'Package checksum mismatch.' }
$root = Join-Path $env:LOCALAPPDATA 'KurumiFRP'
foreach ($p in @($root, (Join-Path $root 'data'), (Join-Path $root 'cores'))) {
 if (Test-Path -LiteralPath $p) {
  if ((Get-Item -LiteralPath $p -Force).Attributes -band [IO.FileAttributes]::ReparsePoint) { throw "Unsafe install directory: $p" }
 } else { New-Item -ItemType Directory -Path $p -Force | Out-Null }
}
# Current user and SYSTEM only. Do not share the local token or encrypted data directory.
$acl = New-Object Security.AccessControl.DirectorySecurity
$acl.SetAccessRuleProtection($true,$false)
foreach ($sid in @([Security.Principal.WindowsIdentity]::GetCurrent().User, (New-Object Security.Principal.SecurityIdentifier('S-1-5-18')))) {
 $acl.AddAccessRule((New-Object Security.AccessControl.FileSystemAccessRule($sid,'FullControl','ContainerInherit,ObjectInherit','None','Allow')))
}
Set-Acl -LiteralPath $root -AclObject $acl
$dest = Join-Path $root 'kurumi.exe'
if ((Test-Path -LiteralPath $dest) -and ((Get-Item -LiteralPath $dest -Force).Attributes -band [IO.FileAttributes]::ReparsePoint)) { throw 'Unsafe existing executable.' }
$running = Test-Path -LiteralPath (Join-Path $root 'data\manager.lock')
$same = (Test-Path -LiteralPath $dest) -and ((Get-FileHash $dest).Hash -eq (Get-FileHash $exe).Hash)
if ($running -and !$same) { throw 'Exit the running workbench using its WebUI Quit button, then run Install.cmd again. Never delete its lock while frp is running.' }
if (!$same) { Copy-Item -LiteralPath $exe -Destination (Join-Path $root 'kurumi-new.exe') -Force; Move-Item -LiteralPath (Join-Path $root 'kurumi-new.exe') -Destination $dest -Force }
$launch = @'
param([switch]$Background)
$ErrorActionPreference = 'Stop'
$root = $PSScriptRoot
$arguments = '--data "' + (Join-Path $root 'data') + '" --frp-dir "' + (Join-Path $root 'cores') + '" --listen auto'
if (!$Background) { $arguments += ' --open' }
Start-Process -FilePath (Join-Path $root 'kurumi.exe') -ArgumentList $arguments -WorkingDirectory $root -WindowStyle Hidden
'@
Set-Content -LiteralPath (Join-Path $root 'Launch.ps1') -Value $launch -Encoding UTF8
$wsh = New-Object -ComObject WScript.Shell
$locations = @([Environment]::GetFolderPath('Desktop'), [Environment]::GetFolderPath('Programs'))
if (!$NoAutostart) { $locations += [Environment]::GetFolderPath('Startup') }
foreach ($folder in $locations) {
 if (!$folder) { continue }
 New-Item -ItemType Directory -Path $folder -Force | Out-Null
 $shortcut = $wsh.CreateShortcut((Join-Path $folder 'KurumiFRP.lnk'))
 $shortcut.TargetPath = Join-Path $env:WINDIR 'System32\WindowsPowerShell\v1.0\powershell.exe'
 $shortcut.Arguments = '-NoProfile -WindowStyle Hidden -ExecutionPolicy Bypass -File "' + (Join-Path $root 'Launch.ps1') + '"'
 if ($folder -eq [Environment]::GetFolderPath('Startup')) { $shortcut.Arguments += ' -Background' }
 $shortcut.WorkingDirectory = $root
 $shortcut.Save()
}
if (!$NoOpen) { & (Join-Path $root 'Launch.ps1') }
Write-Host "Installed in $root. Desktop shortcut: KurumiFRP."
Write-Host 'The launcher selects an available local port in 9000..9010 and opens WebUI. Core management -> Download -> Activate.'
Write-Host 'Configuration and secrets were preserved. No firewall or administrator service was changed.'
