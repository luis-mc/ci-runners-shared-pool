package joinfile

// The templates use << >> as delimiters so the shell's ${...} stays as written.
// Each file holds the one-time join code and nothing else secret. Packages are
// checked against the SHA-256 written into the file (from the release's
// SHA256SUMS, taken in the owner's signed-in session), and the download itself
// pins the controller certificate by the same fingerprint the join uses
// (machines R5.65, CRSP133): a controller that does not present it is refused
// before anything is fetched.

// shellPin is the certificate pin of the .sh and .command files. The chain the
// controller presents must hold the certificate whose SHA-256 is FINGERPRINT
// (the CA, as tlsca.VerifyPin), and the server certificate must be signed by it
// and be inside its dates. curl then keeps to that server key with
// --pinnedpubkey, so the host name does not decide and nothing else is trusted.
// Needs: openssl, awk, curl.
const shellPin = `PIN=''
pin_controller() {
  [ -z "$PIN" ] || return 0
  command -v openssl >/dev/null 2>&1 || die 'openssl is needed to check the controller certificate.'
  case "$BASE" in https://*) ;; *) die "the controller address must start with https:// to be checked: $BASE" ;; esac
  pin_host=${BASE#https://}
  pin_want=$(printf '%s' "$FINGERPRINT" | tr -d ':' | tr 'A-F' 'a-f')
  rm -f "$TMP"/c*.pem
  openssl s_client -connect "$pin_host" -showcerts </dev/null 2>/dev/null | awk -v d="$TMP" '/-----BEGIN CERTIFICATE-----/{n++; on=1} on{print > (d "/c" n ".pem")} /-----END CERTIFICATE-----/{on=0}'
  [ -f "$TMP/c1.pem" ] || die "could not read the certificate of $BASE."
  pin_anchor=''
  for pin_f in "$TMP"/c*.pem; do
    pin_got=$(openssl x509 -in "$pin_f" -outform der | openssl dgst -sha256 | awk '{print $NF}')
    if [ "$pin_got" = "$pin_want" ]; then pin_anchor=$pin_f; break; fi
  done
  [ -n "$pin_anchor" ] || die "the certificate of $BASE is not the one pinned in this file. Nothing was downloaded."
  openssl verify -CAfile "$pin_anchor" "$TMP/c1.pem" 2>&1 | grep -q ': OK$' || die "the certificate of $BASE is not signed by the pinned one. Nothing was downloaded."
  PIN=$(openssl x509 -in "$TMP/c1.pem" -pubkey -noout | openssl pkey -pubin -outform der | openssl dgst -sha256 -binary | openssl base64)
  [ -n "$PIN" ] || die "could not read the key of the certificate of $BASE."
}

fetch() {
  command -v curl >/dev/null 2>&1 || die 'curl is needed to download the package.'
  pin_controller
  curl -fsSL -k --pinnedpubkey "sha256//$PIN" -o "$2" "$1"
}
`

// powerShellPin is the certificate pin of the .cmd file and of the SSH join to
// Windows (PowerShell 5.1 and later, .NET Framework 4.x, C# 5). Add-Type makes a
// static callback: a script block as callback fails on the threads the web
// client uses. The chain is built from what the controller presents; it must
// hold the pinned certificate (as tlsca.VerifyPin) and its signatures and dates
// must be right. The host name and the machine's own trust list do not decide.
const powerShellPin = `Add-Type -TypeDefinition @'
using System;
using System.Net;
using System.Net.Security;
using System.Security.Cryptography;
using System.Security.Cryptography.X509Certificates;
public class CrspPin {
  static string pin = "";
  public static void Enable(string fingerprint) {
    pin = fingerprint.Replace(":", "").ToLowerInvariant();
    ServicePointManager.ServerCertificateValidationCallback = Check;
  }
  static bool Check(object sender, X509Certificate cert, X509Chain presented, SslPolicyErrors errors) {
    if (cert == null || pin.Length != 64) return false;
    X509Chain built = new X509Chain();
    built.ChainPolicy.RevocationMode = X509RevocationMode.NoCheck;
    built.ChainPolicy.VerificationFlags = X509VerificationFlags.AllowUnknownCertificateAuthority;
    if (presented != null) {
      foreach (X509ChainElement el in presented.ChainElements) built.ChainPolicy.ExtraStore.Add(el.Certificate);
    }
    if (!built.Build(new X509Certificate2(cert))) return false;
    using (SHA256 sha = SHA256.Create()) {
      foreach (X509ChainElement el in built.ChainElements) {
        string got = BitConverter.ToString(sha.ComputeHash(el.Certificate.RawData)).Replace("-", "").ToLowerInvariant();
        if (got == pin) return true;
      }
    }
    return false;
  }
}
'@
try { [Net.ServicePointManager]::SecurityProtocol = [Net.SecurityProtocolType]'Tls12,Tls13' } catch { [Net.ServicePointManager]::SecurityProtocol = [Net.SecurityProtocolType]::Tls12 }
`

// PowerShellPin is powerShellPin for the SSH join; it ends where [CrspPin]::Enable
// is called with the fingerprint.
const PowerShellPin = powerShellPin

const linuxTemplate = `#!/bin/sh
# crsp: join this machine to the controller. Made <<.Version>>, valid until <<.Expires>>.
# The join code below works once and only until then. Delete this file after you use it.
# Run: sh <this file>
set -eu

CONTROLLER='<<.Controller>>'
BASE='<<.Base>>'
FINGERPRINT='<<.Fingerprint>>'
JOIN_CODE='<<.Code>>'

say() { printf '%s\n' "$*"; }
die() { printf 'crsp join: %s\n' "$*" >&2; exit 1; }

sum_for() {
  case "$1" in
<<- range .Sums >>
    '<<.Plain>>') say '<<.SHA256>>' ;;
<<- end >>
    *) say '' ;;
  esac
}

name_for() {
  case "$1" in
<<- range .Sums >>
    '<<.Plain>>') say '<<.Name>>' ;;
<<- end >>
    *) say '' ;;
  esac
}

if [ "$(id -u)" -eq 0 ]; then
  SUDO=''
else
  command -v sudo >/dev/null 2>&1 || die 'sudo is needed to install. Run this file as root.'
  SUDO='sudo'
fi

case "$(uname -m)" in
  x86_64|amd64) ARCH=amd64 ;;
  aarch64|arm64) ARCH=arm64 ;;
  *) die "this processor is not supported: $(uname -m)" ;;
esac

hash_of() {
  if command -v sha256sum >/dev/null 2>&1; then sha256sum "$1" | cut -d' ' -f1
  elif command -v shasum >/dev/null 2>&1; then shasum -a 256 "$1" | cut -d' ' -f1
  else die 'sha256sum is needed to check the download.'
  fi
}

TMP=$(mktemp -d)
trap 'rm -rf "$TMP"' EXIT INT TERM

` + shellPin + `
get_package() {
  want=$(sum_for "$1")
  [ -n "$want" ] || die "this controller has no package named $1."
  PKG=$(name_for "$1")
  say "Downloading $PKG from the controller..."
  fetch "$BASE/api/v1/packages/$PKG" "$TMP/$PKG" || die "could not download $PKG from $BASE."
  got=$(hash_of "$TMP/$PKG")
  [ "$got" = "$want" ] || die "$PKG does not match its SHA256SUMS entry. Nothing was installed."
  say "Checked the SHA-256 of $PKG."
}

if ! command -v crsp >/dev/null 2>&1; then
  if command -v dpkg >/dev/null 2>&1; then
    get_package "crsp-linux-$ARCH.deb"
    $SUDO dpkg -i "$TMP/$PKG"
  elif command -v rpm >/dev/null 2>&1; then
    get_package "crsp-linux-$ARCH.rpm"
    $SUDO rpm -U "$TMP/$PKG"
  else
    get_package "crsp-linux-$ARCH.tar.gz"
    mkdir "$TMP/x"
    tar -xzf "$TMP/$PKG" -C "$TMP/x"
    BIN=$(find "$TMP/x" -type f -name crsp | head -n 1)
    [ -n "$BIN" ] || die 'the archive holds no crsp program.'
    $SUDO install -m 0755 "$BIN" /usr/local/bin/crsp
  fi
fi

CRSP=$(command -v crsp 2>/dev/null || say /usr/local/bin/crsp)
$SUDO "$CRSP" agent install --controller "$CONTROLLER" --fingerprint "$FINGERPRINT" --join-code "$JOIN_CODE"
say 'Done. This machine joins the pool; the console shows it in a moment.'
`

const macosTemplate = `#!/bin/sh
# crsp: join this Mac to the controller. Made <<.Version>>, valid until <<.Expires>>.
# The join code below works once and only until then. Delete this file after you use it.
# Double-click this file, or run: sh <this file>
set -eu

CONTROLLER='<<.Controller>>'
BASE='<<.Base>>'
FINGERPRINT='<<.Fingerprint>>'
JOIN_CODE='<<.Code>>'

say() { printf '%s\n' "$*"; }
die() { printf 'crsp join: %s\n' "$*" >&2; exit 1; }

sum_for() {
  case "$1" in
<<- range .Sums >>
    '<<.Plain>>') say '<<.SHA256>>' ;;
<<- end >>
    *) say '' ;;
  esac
}

name_for() {
  case "$1" in
<<- range .Sums >>
    '<<.Plain>>') say '<<.Name>>' ;;
<<- end >>
    *) say '' ;;
  esac
}

case "$(uname -m)" in
  x86_64) ARCH=amd64 ;;
  arm64) ARCH=arm64 ;;
  *) die "this processor is not supported: $(uname -m)" ;;
esac

TMP=$(mktemp -d)
trap 'rm -rf "$TMP"' EXIT INT TERM

` + shellPin + `
if ! command -v crsp >/dev/null 2>&1 && [ ! -x /usr/local/bin/crsp ]; then
  PLAIN="crsp-darwin-$ARCH.pkg"
  want=$(sum_for "$PLAIN")
  [ -n "$want" ] || die "this controller has no package named $PLAIN."
  PKG=$(name_for "$PLAIN")
  say "Downloading $PKG from the controller..."
  fetch "$BASE/api/v1/packages/$PKG" "$TMP/$PKG" || die "could not download $PKG from $BASE."
  got=$(shasum -a 256 "$TMP/$PKG" | cut -d' ' -f1)
  [ "$got" = "$want" ] || die "$PKG does not match its SHA256SUMS entry. Nothing was installed."
  say "Checked the SHA-256 of $PKG. macOS asks for your password to install it."
  sudo installer -pkg "$TMP/$PKG" -target /
fi

CRSP=$(command -v crsp 2>/dev/null || say /usr/local/bin/crsp)
sudo "$CRSP" agent install --controller "$CONTROLLER" --fingerprint "$FINGERPRINT" --join-code "$JOIN_CODE"
say 'Done. This Mac joins the pool; the console shows it in a moment.'
`

const windowsTemplate = `@echo off
rem crsp: join this PC to the controller. Made <<.Version>>, valid until <<.Expires>>.
rem The join code below works once and only until then. Delete this file after you use it.
rem Double-click this file and accept the administrator prompt.
set "CRSP_SELF=%~f0"
powershell -NoProfile -ExecutionPolicy Bypass -Command "$t=[IO.File]::ReadAllText($env:CRSP_SELF); $i=$t.LastIndexOf('#--PS'+'-BODY--'); & ([ScriptBlock]::Create($t.Substring($i))) $env:CRSP_SELF"
set "CRSP_RC=%errorlevel%"
if "%CRSP_RC%"=="10" exit /b 0
echo.
pause
exit /b %CRSP_RC%
#--PS-BODY--
param($Self)
$ErrorActionPreference = 'Stop'
$Controller = '<<.Controller>>'
$Base = '<<.Base>>'
$Fingerprint = '<<.Fingerprint>>'
$JoinCode = '<<.Code>>'
$Sums = @{
<<- range .Sums >>
  '<<.Plain>>' = '<<.SHA256>>'
<<- end >>
}
$Names = @{
<<- range .Sums >>
  '<<.Plain>>' = '<<.Name>>'
<<- end >>
}
try {
  $admin = ([Security.Principal.WindowsPrincipal][Security.Principal.WindowsIdentity]::GetCurrent()).IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)
  if (-not $admin) {
    Start-Process -FilePath 'cmd.exe' -ArgumentList ('/c ""' + $Self + '""') -Verb RunAs
    exit 10
  }
  $crsp = $null
  $found = Get-Command crsp.exe -ErrorAction SilentlyContinue
  if ($found) { $crsp = $found.Source }
  if (-not $crsp) {
    $installed = Join-Path $env:ProgramFiles 'crsp\crsp.exe'
    if (Test-Path -LiteralPath $installed) { $crsp = $installed }
  }
  if (-not $crsp) {
    $arch = 'amd64'
    if ($env:PROCESSOR_ARCHITECTURE -eq 'ARM64') { $arch = 'arm64' }
    $plain = 'crsp-windows-' + $arch + '.msi'
    $want = $Sums[$plain]
    if (-not $want) { throw ('This controller has no package named ' + $plain + '.') }
    $name = $Names[$plain]
    $dir = Join-Path $env:TEMP ('crsp-join-' + [guid]::NewGuid().ToString('N'))
    New-Item -ItemType Directory -Path $dir | Out-Null
    $msi = Join-Path $dir $name
` + powerShellPin + `    [CrspPin]::Enable($Fingerprint)
    Write-Host ('Downloading ' + $name + ' from the controller...')
    try { (New-Object Net.WebClient).DownloadFile($Base + '/api/v1/packages/' + $name, $msi) }
    catch { throw ('Could not download ' + $name + ' from ' + $Base + '. Its certificate must be the one pinned in this file. ' + $_.Exception.Message) }
    $got = (Get-FileHash -Algorithm SHA256 -LiteralPath $msi).Hash.ToLower()
    if ($got -ne $want) { throw ($name + ' does not match its SHA256SUMS entry. Nothing was installed.') }
    Write-Host ('Checked the SHA-256 of ' + $name + '.')
    $run = Start-Process -FilePath 'msiexec.exe' -ArgumentList @('/i', ('"' + $msi + '"'), '/qn', '/norestart') -Wait -PassThru
    if (($run.ExitCode -ne 0) -and ($run.ExitCode -ne 3010)) { throw ('The installer stopped with code ' + $run.ExitCode + '.') }
    $crsp = Join-Path $env:ProgramFiles 'crsp\crsp.exe'
    Remove-Item -LiteralPath $dir -Recurse -Force -ErrorAction SilentlyContinue
  }
  & $crsp agent install --controller $Controller --fingerprint $Fingerprint --join-code $JoinCode
  if ($LASTEXITCODE -ne 0) { throw ('crsp agent install stopped with code ' + $LASTEXITCODE + '.') }
  Write-Host 'Done. This PC joins the pool; the console shows it in a moment.'
  exit 0
} catch {
  Write-Host ('crsp join: ' + $_.Exception.Message) -ForegroundColor Red
  exit 1
}
`
