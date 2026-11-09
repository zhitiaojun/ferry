# Build a signed sparse MSIX for the ft Explorer context menu and install it.
#
# The loose `Add-AppxPackage -Register` form (developer mode) does not work for
# this shell extension: Explorer hangs while resolving the packaged verb. A
# signed package installed normally does work — see README "一级右键菜单".
#
# Run elevated (it imports the self-signed certificate into
# LocalMachine\TrustedPeople). Everything else is per-user.
param(
  [string]$RepoRoot = (Split-Path -Parent (Split-Path -Parent $PSScriptRoot)),
  [string]$MenuDir  = (Join-Path $env:LOCALAPPDATA 'ft\menu'),
  [string]$SdkBin   = '',
  [switch]$Uninstall
)

$ErrorActionPreference = 'Stop'
$pkgName = 'FileTransfer.ShellMenu'
$clsid   = 'B7F1D2C4-8A31-4C77-9D2E-3A5B7C1E9F10'

function Find-SdkBin {
  param([string]$Hint)
  if ($Hint -and (Test-Path (Join-Path $Hint 'makeappx.exe'))) { return $Hint }
  $candidates = @(
    (Join-Path $env:USERPROFILE '.tools\sdk\x\bin'),
    (Join-Path $PSScriptRoot '..\..\.tools\sdk\x\bin')
  )
  foreach ($c in $candidates) {
    if (Test-Path $c) {
      $hit = Get-ChildItem -Path $c -Recurse -Filter makeappx.exe -ErrorAction SilentlyContinue |
             Where-Object { $_.FullName -match '\\x64\\' } | Select-Object -First 1
      if ($hit) { return $hit.DirectoryName }
    }
  }
  throw 'makeappx.exe not found; pass -SdkBin (see tools/sdk note in RUNBOOK)'
}

$sdk = Find-SdkBin $SdkBin
$makeappx = Join-Path $sdk 'makeappx.exe'
$signtool = Join-Path $sdk 'signtool.exe'

if ($Uninstall) {
  Get-AppxPackage $pkgName | Remove-AppxPackage -ErrorAction SilentlyContinue
  Remove-Item -Path "HKCU:\Software\Classes\CLSID\{$clsid}" -Recurse -Force -ErrorAction SilentlyContinue
  Get-Process dllhost -ErrorAction SilentlyContinue | Stop-Process -Force -ErrorAction SilentlyContinue
  Write-Host 'shellmenu uninstalled'
  exit 0
}

$manifest = Join-Path $RepoRoot 'deploy\msix\AppxManifest.xml'
$dll      = Join-Path $RepoRoot '.tools\dist\ftmenu.dll'
$stub     = Join-Path $RepoRoot '.tools\dist\ftstub.exe'
$assets   = Join-Path $RepoRoot 'deploy\msix\Assets'
foreach ($p in @($manifest, $dll, $stub)) {
  if (-not (Test-Path $p)) { throw "missing input: $p" }
}

# 1. stage
$stage = Join-Path $RepoRoot '.tools\msix-stage'
if (Test-Path $stage) { Remove-Item $stage -Recurse -Force }
New-Item -ItemType Directory -Force -Path $stage, (Join-Path $stage 'Assets') | Out-Null
Copy-Item $manifest (Join-Path $stage 'AppxManifest.xml')
Copy-Item $dll      (Join-Path $stage 'ftmenu.dll')
Copy-Item $stub     (Join-Path $stage 'ftstub.exe')
Copy-Item (Join-Path $assets '*') (Join-Path $stage 'Assets') -Recurse

# 2. sign needs a certificate whose subject matches Identity/@Publisher (CN=ft-dev)
$cert = Get-ChildItem Cert:\CurrentUser\My |
        Where-Object { $_.Subject -eq 'CN=ft-dev' -and $_.HasPrivateKey } |
        Sort-Object NotAfter -Descending | Select-Object -First 1
if (-not $cert) {
  $cert = New-SelfSignedCertificate -Type CodeSigningCert -Subject 'CN=ft-dev' `
          -KeyUsage DigitalSignature -FriendlyName 'ft shell menu (dev)' `
          -CertStoreLocation 'Cert:\CurrentUser\My' -NotAfter (Get-Date).AddYears(5)
  Write-Host "created certificate $($cert.Thumbprint)"
}
$cer = Join-Path $env:TEMP 'ft-dev.cer'
Export-Certificate -Cert $cert -FilePath $cer -Force | Out-Null

# 3. trust it for the machine (needs admin)
$trusted = Get-ChildItem Cert:\LocalMachine\TrustedPeople -ErrorAction SilentlyContinue |
           Where-Object { $_.Thumbprint -eq $cert.Thumbprint }
if (-not $trusted) {
  Import-Certificate -FilePath $cer -CertStoreLocation 'Cert:\LocalMachine\TrustedPeople' | Out-Null
  Write-Host 'certificate trusted (LocalMachine\TrustedPeople)'
}

# 4. pack + sign
$msix = Join-Path $RepoRoot '.tools\msix\ft-shellmenu.msix'
New-Item -ItemType Directory -Force -Path (Split-Path -Parent $msix) | Out-Null
& $makeappx pack /d $stage /p $msix /o | Out-Null
& $signtool sign /fd SHA256 /sha1 $cert.Thumbprint $msix | Out-Null
Write-Host "signed: $msix"

# 5. install (normal package, not loose registration)
Get-AppxPackage $pkgName | Remove-AppxPackage -ErrorAction SilentlyContinue
Start-Sleep -Milliseconds 500
Add-AppxPackage -Path $msix -ExternalLocation $MenuDir
Write-Host "installed $((Get-AppxPackage $pkgName).Version)"

# 6. classic CLSID so the shell can resolve the class even outside package context
$key = "HKCU:\Software\Classes\CLSID\{$clsid}"
New-Item -Path "$key\InprocServer32" -Force | Out-Null
Set-ItemProperty -Path $key -Name '(Default)' -Value 'ft Context Menu'
Set-ItemProperty -Path "$key\InprocServer32" -Name '(Default)' -Value (Join-Path $MenuDir 'ftmenu.dll')
Set-ItemProperty -Path "$key\InprocServer32" -Name 'ThreadingModel' -Value 'Both'

# 7. copy payload where the CLSID points
New-Item -ItemType Directory -Force -Path $MenuDir | Out-Null
Copy-Item $dll, $stub (Join-Path $MenuDir) -Force

Get-Process explorer -ErrorAction SilentlyContinue | Stop-Process -Force
Start-Sleep 2
Start-Process explorer.exe
Write-Host 'done'
