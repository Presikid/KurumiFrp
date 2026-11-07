# Current-user installation; no additional runtime or administrator elevation.
[CmdletBinding()]
param([switch]$NoOpen, [switch]$NoAutostart, [string]$ArchivePath="")
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
$source = $PSScriptRoot
$arch = [System.Runtime.InteropServices.RuntimeInformation]::OSArchitecture.ToString().ToLowerInvariant()
if ($arch -eq 'x64') { $arch = 'amd64' }
if ($arch -notin @('amd64','arm64')) { throw 'Unsupported Windows architecture.' }
if ((Get-Content -LiteralPath (Join-Path $source 'PLATFORM') -Raw).Trim() -ne "windows_$arch") { throw 'Wrong Windows package.' }
$exe = Join-Path $source 'kurumi.exe'
$expected = (Get-Content -LiteralPath (Join-Path $source 'SHA256SUMS') -Raw).Trim()
if ($expected -notmatch '^([a-f0-9]{64})  kurumi\.exe$') { throw 'Invalid checksum manifest.' }
$expectedHash = $Matches[1]
if ((Get-FileHash -Algorithm SHA256 -LiteralPath $exe).Hash.ToLowerInvariant() -ne $expectedHash) { throw 'Package checksum mismatch.' }
$root = [IO.Path]::GetFullPath((Join-Path $env:LOCALAPPDATA 'KurumiFRP'))
$registry = 'HKCU:\Software\Microsoft\Windows\CurrentVersion\Uninstall\KurumiFRP'
$powerShell = Join-Path $env:WINDIR 'System32\WindowsPowerShell\v1.0\powershell.exe'
Assert-Plain $root
foreach ($name in @('data','cores','install-receipt.json','uninstall.ps1','Uninstall.cmd','Launch.ps1','kurumi.exe','kurumi-new.exe','.uninstalling')) { Assert-Plain (Join-Path $root $name) }
if ((Test-Path -LiteralPath $root) -and !(Test-Path -LiteralPath (Join-Path $root 'install-receipt.json'))) {
 $entries = @(Get-ChildItem -LiteralPath $root -Force)
 if ($entries.Count -gt 0) {
  $legacy = Join-Path $root 'kurumi.exe'
  if (!(Test-Path -LiteralPath $legacy) -or !(Test-Path -LiteralPath (Join-Path $root 'data\access.token'))) { throw 'Unrecognized pre-existing install directory.' }
  if ((& $legacy --version) -ne '0.2.0') { throw 'Unrecognized legacy version; directory retained.' }
 }
}
if (Test-Path -LiteralPath (Join-Path $root 'install-receipt.json')) {
 $previous = Get-Content -LiteralPath (Join-Path $root 'install-receipt.json') -Raw | ConvertFrom-Json
 if ($previous.product -ne 'io.github.Presikid.KurumiFrp' -or $previous.schema -ne 1 -or $previous.root -ne $root) { throw 'Invalid installation receipt.' }
}
if (Test-Path -LiteralPath $registry) {
 $old = Get-ItemProperty -LiteralPath $registry
 if ($old.InstallLocation -ne $root) { throw 'Unrelated uninstall registration exists.' }
}
foreach ($p in @($root,(Join-Path $root 'data'),(Join-Path $root 'cores'))) { New-Item -ItemType Directory -Path $p -Force | Out-Null }
$guard = Join-Path $root '.maintenance'
if (Test-Path -LiteralPath $guard) { throw 'Another install/uninstall is active.' }
New-Item -ItemType Directory -Path $guard | Out-Null
try {
 if (!(Test-Path -LiteralPath (Join-Path $root 'install-receipt.json'))) {
  @{product='io.github.Presikid.KurumiFrp';schema=1;root=$root;version='0.3.0';links=@()} | ConvertTo-Json -Depth 5 | Set-Content -LiteralPath (Join-Path $root 'install-receipt.json') -Encoding UTF8
 }
 $acl = New-Object Security.AccessControl.DirectorySecurity($root, [Security.AccessControl.AccessControlSections]::Access)
 $acl.SetAccessRuleProtection($true,$false)
 foreach ($rule in @($acl.GetAccessRules($true,$false,[Security.Principal.SecurityIdentifier]))) { [void]$acl.RemoveAccessRuleSpecific($rule) }
 foreach ($sid in @([Security.Principal.WindowsIdentity]::GetCurrent().User,(New-Object Security.Principal.SecurityIdentifier('S-1-5-18')))) {
  $acl.AddAccessRule((New-Object Security.AccessControl.FileSystemAccessRule($sid,'FullControl','ContainerInherit,ObjectInherit','None','Allow')))
 }
 Set-Acl -LiteralPath $root -AclObject $acl
 $dest = Join-Path $root 'kurumi.exe'
 $same = (Test-Path -LiteralPath $dest) -and ((Get-FileHash -LiteralPath $dest).Hash -eq (Get-FileHash -LiteralPath $exe).Hash)
 if ((Test-Path -LiteralPath (Join-Path $root 'data\manager.lock')) -and !$same) { throw 'Exit the running workbench before upgrading. Installer files retained.' }
 if (!$same) { Copy-Item -LiteralPath $exe -Destination (Join-Path $root 'kurumi-new.exe') -Force; Move-Item -LiteralPath (Join-Path $root 'kurumi-new.exe') -Destination $dest -Force }
 foreach ($name in @('uninstall.ps1','Uninstall.cmd')) { Copy-Item -LiteralPath (Join-Path $source $name) -Destination (Join-Path $root $name) -Force }
 $launch = @'
param([switch]$Background)
$ErrorActionPreference = 'Stop'
$env:PSModulePath = [IO.Path]::Combine($PSHOME, 'Modules') + [IO.Path]::PathSeparator + $env:PSModulePath
$root = $PSScriptRoot
$arguments = '--data "' + (Join-Path $root 'data') + '" --frp-dir "' + (Join-Path $root 'cores') + '" --listen auto'
if (!$Background) { $arguments += ' --open' }
Start-Process -FilePath (Join-Path $root 'kurumi.exe') -ArgumentList $arguments -WorkingDirectory $root -WindowStyle Hidden
'@
 Set-Content -LiteralPath (Join-Path $root 'Launch.ps1') -Value $launch -Encoding UTF8
 $wsh = New-Object -ComObject WScript.Shell
 $links = @()
 foreach ($kind in @('Desktop','Programs','Startup')) {
  $folder = [Environment]::GetFolderPath($kind)
  if (!$folder) { continue }
  $path = Join-Path $folder 'KurumiFRP.lnk'; Assert-Plain $path
  $args = '-NoProfile -WindowStyle Hidden -ExecutionPolicy Bypass -File "' + (Join-Path $root 'Launch.ps1') + '"'
  if ($kind -eq 'Startup') { $args += ' -Background' }
  if (Test-Path -LiteralPath $path) {
   $oldLink = $wsh.CreateShortcut($path)
   if ($oldLink.TargetPath -ne $powerShell -or $oldLink.Arguments -ne $args) { throw "Unrelated shortcut retained: $path" }
  }
  if ($kind -ne 'Startup' -or !$NoAutostart) {
   New-Item -ItemType Directory -Path $folder -Force | Out-Null
   $link = $wsh.CreateShortcut($path); $link.TargetPath = $powerShell; $link.Arguments = $args; $link.WorkingDirectory = $root; $link.Save()
  }
  if (Test-Path -LiteralPath $path) { $links += @{path=$path;target=$powerShell;arguments=$args} }
 }
 $receipt = @{product='io.github.Presikid.KurumiFrp';schema=1;root=$root;version=(Get-Content -LiteralPath (Join-Path $source 'VERSION') -Raw).Trim();links=@($links)}
 $receipt | ConvertTo-Json -Depth 5 | Set-Content -LiteralPath (Join-Path $root 'install-receipt.json') -Encoding UTF8
 New-Item -Path $registry -Force | Out-Null
 $uninstall = '"' + $powerShell + '" -NoProfile -ExecutionPolicy Bypass -File "' + (Join-Path $root 'uninstall.ps1') + '"'
 foreach ($entry in @{DisplayName='KurumiFRP';DisplayVersion=$receipt.version;Publisher='KurumiFRP';InstallLocation=$root;DisplayIcon=$dest;UninstallString=$uninstall;QuietUninstallString=($uninstall+' -Yes')}.GetEnumerator()) {
  New-ItemProperty -LiteralPath $registry -Name $entry.Key -Value $entry.Value -PropertyType String -Force | Out-Null
 }
 foreach ($name in @('NoModify','NoRepair')) { New-ItemProperty -LiteralPath $registry -Name $name -Value 1 -PropertyType DWord -Force | Out-Null }
 if (!(Test-Path -LiteralPath (Join-Path $root 'data\manager.lock'))) { & (Join-Path $root 'Launch.ps1') -Background }
 & $dest --data (Join-Path $root 'data') --check-running
 if ($LASTEXITCODE -ne 0) { throw 'Startup not verified. Installer files retained.' }
 if (!$NoOpen) { & (Join-Path $root 'Launch.ps1') }
 Write-Host "Installed in $root. Uninstall from Windows Settings > Apps or Uninstall.cmd."
 Write-Host 'Full uninstall deletes all application configurations, keys, cores and logs.'
 if ($ArchivePath) { & $dest --cleanup-package $source --cleanup-archive $ArchivePath } else { & $dest --cleanup-package $source }
 if ($LASTEXITCODE -ne 0) { throw 'Application installed, but package cleanup was not completed; inspect the error above.' }
 Write-Host 'Verified extracted installer payload removed. No Downloads folder was scanned.'
} finally {
 if (Test-Path -LiteralPath $guard) { Remove-Item -LiteralPath $guard -Force }
}
