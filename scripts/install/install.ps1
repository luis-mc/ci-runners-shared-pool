# One command installs crsp on Windows (operations R5.1):
#   irm <release>/install.ps1 | iex
# It downloads the archive for this architecture, checks its SHA-256 against
# SHA256SUMS, checks its build provenance (operations R5.1, R5.34), and runs
# `crsp install`. It does nothing else.
$ErrorActionPreference = 'Stop'

$Repo = if ($env:CRSP_REPO) { $env:CRSP_REPO } else { 'luis-mc/ci-runners-shared-pool' }
$Base = if ($env:CRSP_RELEASE_URL) { $env:CRSP_RELEASE_URL } else { "https://github.com/$Repo/releases/latest/download" }

function Stop-Install([string]$Message) {
    throw "crsp install: $Message"
}

$arch = if ($env:PROCESSOR_ARCHITECTURE -eq 'ARM64') { 'arm64' } else { 'amd64' }
$work = Join-Path ([IO.Path]::GetTempPath()) ("crsp-install-" + [Guid]::NewGuid().ToString('N'))
New-Item -ItemType Directory -Path $work | Out-Null
try {
    $sums = Join-Path $work 'SHA256SUMS'
    Invoke-WebRequest -Uri "$Base/SHA256SUMS" -OutFile $sums -UseBasicParsing
    # The archive is named Crsp-<version>-win-<arch>.zip (OPS69: arch x64 or arm64). SHA256SUMS has a fixed
    # name and lists it. For one transition release the older names are tried second:
    # crsp-windows-<arch>_<version>.zip and, up to 0.1.14, crsp-windows-<arch>.zip.
    $newArch = if ($arch -eq 'amd64') { 'x64' } else { $arch }
    $names = Get-Content $sums | ForEach-Object { ($_ -split '\s+', 2)[1] -replace '^\*', '' }
    $name = $names | Where-Object { $_ -match "^Crsp-[0-9A-Za-z.+-]+-win-$newArch\.zip$" } | Select-Object -First 1
    if (-not $name) {
        $name = $names | Where-Object { $_ -match "^crsp-windows-$arch(_[0-9A-Za-z.+-]+)?\.zip$" } | Select-Object -First 1
    }
    if (-not $name) { Stop-Install "no archive for windows-$arch is in SHA256SUMS" }
    $file = Join-Path $work $name
    Invoke-WebRequest -Uri "$Base/$name" -OutFile $file -UseBasicParsing

    # Step 1: the SHA-256 must match the line for this file.
    $line = Get-Content $sums | Where-Object { ($_ -split '\s+', 2)[1] -replace '^\*', '' -eq $name } | Select-Object -First 1
    if (-not $line) { Stop-Install "$name is not in SHA256SUMS" }
    $want = ($line -split '\s+')[0].ToLowerInvariant()
    $have = (Get-FileHash -Algorithm SHA256 $file).Hash.ToLowerInvariant()
    if ($want -ne $have) { Stop-Install "SHA-256 of $name does not match SHA256SUMS; nothing installed" }
    Write-Host "crsp install: SHA-256 of $name matches SHA256SUMS"

    # Step 2: build provenance. With the GitHub CLI, a failure stops the install.
    # Without it, say so and how to check by hand, then go on.
    if (Get-Command gh -ErrorAction SilentlyContinue) {
        & gh attestation verify $file --repo $Repo
        if ($LASTEXITCODE -ne 0) { Stop-Install "build provenance of $name did not verify; nothing installed" }
        Write-Host "crsp install: build provenance of $name verified"
    } else {
        Write-Warning "crsp install: only the checksum was checked. The GitHub CLI (gh) is not installed, so the build provenance was not."
        Write-Warning "crsp install: To check by hand, install gh (https://cli.github.com) and run: gh attestation verify $name --repo $Repo"
    }

    Expand-Archive -Path $file -DestinationPath $work -Force
    & (Join-Path $work "crsp-windows-$arch\crsp.exe") install @args
    if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }
} finally {
    Remove-Item -Recurse -Force $work -ErrorAction SilentlyContinue
}
