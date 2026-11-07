# Public release bootstrap; no GitHub token, Python, Go or administrator needed.
[CmdletBinding()]
param([switch]$NoOpen,[switch]$NoAutostart)
$ErrorActionPreference='Stop'
$env:PSModulePath = [IO.Path]::Combine($PSHOME,'Modules') + [IO.Path]::PathSeparator + $env:PSModulePath
Set-StrictMode -Version Latest
[Net.ServicePointManager]::SecurityProtocol = [Net.SecurityProtocolType]::Tls12
$version='0.3.0'
$arch=[System.Runtime.InteropServices.RuntimeInformation]::OSArchitecture.ToString().ToLowerInvariant()
if ($arch -eq 'x64') { $arch='amd64' }
if ($arch -notin @('amd64','arm64')) { throw 'Unsupported CPU.' }
$asset="KurumiFRP_${version}_windows_${arch}.zip"
$base="https://github.com/Presikid/KurumiFrp/releases/download/v$version"
$work=Join-Path ([IO.Path]::GetTempPath()) ('kurumifrp-download-'+[Guid]::NewGuid().ToString('N'))
New-Item -ItemType Directory -Path $work | Out-Null
$success=$false
try {
 Invoke-WebRequest -UseBasicParsing -Uri "$base/$asset" -OutFile (Join-Path $work $asset) -TimeoutSec 600
 Invoke-WebRequest -UseBasicParsing -Uri "$base/checksums.txt" -OutFile (Join-Path $work 'checksums.txt') -TimeoutSec 120
 $rows=@(Get-Content -LiteralPath (Join-Path $work 'checksums.txt') | Where-Object { $_ -match ('^[a-f0-9]{64}  '+[Regex]::Escape($asset)+'$') })
 if ($rows.Count -ne 1) { throw 'Missing or duplicate checksum.' }
 if ((Get-FileHash -Algorithm SHA256 -LiteralPath (Join-Path $work $asset)).Hash.ToLowerInvariant() -ne $rows[0].Substring(0,64)) { throw 'Download checksum mismatch.' }
 Add-Type -AssemblyName System.IO.Compression.FileSystem
 [IO.Compression.ZipFile]::ExtractToDirectory((Join-Path $work $asset),$work)
 & (Join-Path $work 'KurumiFRP\install.ps1') -NoOpen:$NoOpen -NoAutostart:$NoAutostart -ArchivePath (Join-Path $work $asset)
 $success=$true
 Write-Host 'Installation verified. Temporary ZIP and extracted installer will be removed.'
} finally {
 if ($success) { Remove-Item -LiteralPath $work -Recurse -Force }
 else { Write-Host "Installation not confirmed. Download files retained in: $work" }
}
