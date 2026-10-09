#!/usr/bin/env pwsh
# motita installer for Windows - one line, no Go, no Docker, no runtime on the target.
#
#   irm https://madkoding.github.io/motita/install.ps1 | iex
#
# It detects the architecture, downloads the matching static binary from the GitHub
# release, checks it against the release's SHA256SUMS, and puts it on the PATH.
#
# Overrides:
#   $env:MOTITA_VERSION      install a specific release instead of the latest
#   $env:MOTITA_INSTALL_DIR  install somewhere else
#   $env:MOTITA_INSECURE_SKIP_VERIFY = '1'
#                            install even when the release cannot be verified (no
#                            SHA256SUMS, or no entry for this platform). A checksum
#                            MISMATCH is refused regardless.
#
# On failure this THROWS rather than exiting, and the difference is not cosmetic: this
# script is normally run through `irm ... | iex`, and `exit` inside Invoke-Expression ends
# the CALLER's PowerShell session - a failed download would close the user's window. A
# throw is reported, is catchable, and still gives exit status 1 when the file is run
# directly.
#
# It is deliberately ASCII-only: the script is fetched and evaluated in one process, and a
# non-ASCII byte is how a pipe-to-interpreter fails for the people who cannot debug it.
#
# This file is published TWICE: here, and as site/install.ps1, which is the URL above.
# GitHub Pages uploads site/ on its own and cannot see scripts/. The two copies are held
# byte-identical by internal/policy's published-copy test, so edit this one and run:
#   cp scripts/install.ps1 site/install.ps1
$ErrorActionPreference = 'Stop'

$Repo = 'madkoding/motita'
$BinName = 'motita'

# The ed25519 key releases sign SHA256SUMS with, as the base64 body of its PEM public key.
# It is the same value as ReleasePublicKey in internal/updater, and
# scripts/release-signing-key.sh prints it. Empty is the placeholder: no key has been
# configured yet, and the signature is not checked.
$ReleasePublicKey = ''

function Say { param([string]$Message) Write-Host $Message }
function Warn { param([string]$Message) Write-Host "warning: $Message" -ForegroundColor Yellow }
function Fail {
    param([string]$Message)
    Write-Host "error: $Message" -ForegroundColor Red
    throw $Message
}

# A release that cannot be verified is refused unless the user opted out. This used to be
# a warning, and an install that went ahead anyway.
function Unverified {
    param([string]$Message)
    if ($env:MOTITA_INSECURE_SKIP_VERIFY -eq '1') {
        Warn "$Message; installing UNVERIFIED because MOTITA_INSECURE_SKIP_VERIFY=1"
    } else {
        Fail "$Message, so the download cannot be verified. Nothing was installed.`n  To install anyway (not recommended): `$env:MOTITA_INSECURE_SKIP_VERIFY = '1'"
    }
}

# OpenSSL writes to stderr, and under 'Stop' Windows PowerShell 5.1 turns a redirected stderr
# line from a native command into a terminating error. The exit code is the answer here, so
# the preference is relaxed for the call only.
function Invoke-OpenSsl {
    param([string]$Path, [string[]]$Arguments)
    $Saved = $ErrorActionPreference
    $ErrorActionPreference = 'Continue'
    try {
        $Output = & $Path @Arguments 2>&1 | Out-String
        $Code = $LASTEXITCODE
    } finally {
        $ErrorActionPreference = $Saved
    }
    return @{ Code = $Code; Output = $Output }
}

# --- 1. which system is this? -------------------------------------------------
# 32-bit PowerShell on 64-bit Windows reports x86 in PROCESSOR_ARCHITECTURE and the REAL
# architecture in PROCESSOR_ARCHITEW6432. The second one wins: without it a 64-bit machine
# is handed the 32-bit binary, which runs - so the mistake is invisible until something
# needs the address space.
function Get-Arch {
    $arch = $env:PROCESSOR_ARCHITECTURE
    if ($env:PROCESSOR_ARCHITEW6432) { $arch = $env:PROCESSOR_ARCHITEW6432 }
    switch ($arch) {
        'AMD64' { return 'amd64' }
        'ARM64' { return 'arm64' }
        'x86'   { return '386' }
        default { return '' }
    }
}

# --- 2. resolve the release ---------------------------------------------------
$Version = if ($env:MOTITA_VERSION) { $env:MOTITA_VERSION } else { 'latest' }
$Arch = Get-Arch
if (-not $Arch) {
    Fail "unsupported architecture: $($env:PROCESSOR_ARCHITECTURE). Published are windows/386, windows/amd64 and windows/arm64."
}

$Asset = "$BinName-windows-$Arch.exe"
if ($Version -eq 'latest') {
    $Base = "https://github.com/$Repo/releases/latest/download"
} else {
    $Base = "https://github.com/$Repo/releases/download/$Version"
}

Say 'motita installer'
Say "  system: windows/$Arch"
Say "  asset:  $Asset"
Say "  from:   $Version"

# --- 3. download into a temporary directory -----------------------------------
# Everything lands here first, and the binary is moved into place only after it is
# downloaded AND verified, so a failure at any point leaves the system as it was.
$Tmp = Join-Path ([IO.Path]::GetTempPath()) ("motita." + [guid]::NewGuid().ToString('N'))
New-Item -ItemType Directory -Path $Tmp -Force | Out-Null
try {
    $AssetPath = Join-Path $Tmp $Asset
    $SumsPath = Join-Path $Tmp 'SHA256SUMS'

    # -UseBasicParsing is accepted and ignored on PowerShell 7; on 5.1 it is what keeps
    # the call from depending on the Internet Explorer HTML engine.
    try {
        Invoke-WebRequest -Uri "$Base/$Asset" -OutFile $AssetPath -UseBasicParsing
    } catch {
        Fail "could not download $Asset from $Version`n  The release may not publish this platform (windows/386, windows/amd64, windows/arm64).`n  Check $Base/$Asset`n  $($_.Exception.Message)"
    }

    # --- 4. verify against the release's own checksums ------------------------
    # The checksum comes from the same release as the binary, so on its own it catches a
    # corrupted or truncated download (a proxy, a flaky connection). The signature over
    # SHA256SUMS is what makes it a defence against a compromised release.
    $Verified = $false
    $Signed = $false
    $SumsAvailable = $true
    try {
        Invoke-WebRequest -Uri "$Base/SHA256SUMS" -OutFile $SumsPath -UseBasicParsing
    } catch {
        $SumsAvailable = $false
    }

    if ($SumsAvailable -and (Test-Path $SumsPath)) {
        # Windows ships no ed25519 verifier, so the signature is checked with OpenSSL 3 when
        # one is on the PATH (Git for Windows carries it), and skipped otherwise.
        $OpenSsl = Get-Command openssl -ErrorAction SilentlyContinue
        if (-not $ReleasePublicKey) {
            Say '  signature: not checked (this installer carries no release key yet)'
        } elseif (-not $OpenSsl -or -not ((Invoke-OpenSsl $OpenSsl.Source @('pkeyutl', '-help')).Output -match '-rawin')) {
            Say '  signature: not checked (needs OpenSSL 3 with ed25519 on the PATH)'
        } else {
            $SigPath = Join-Path $Tmp 'SHA256SUMS.sig'
            $SigAvailable = $true
            try {
                Invoke-WebRequest -Uri "$Base/SHA256SUMS.sig" -OutFile $SigPath -UseBasicParsing
            } catch {
                $SigAvailable = $false
            }
            if (-not $SigAvailable) {
                Unverified 'SHA256SUMS.sig is not available in this release'
            } else {
                $PubPath = Join-Path $Tmp 'release.pub'
                Set-Content -Path $PubPath -Encoding ascii -Value @('-----BEGIN PUBLIC KEY-----', $ReleasePublicKey, '-----END PUBLIC KEY-----')
                $Check = Invoke-OpenSsl $OpenSsl.Source @('pkeyutl', '-verify', '-pubin', '-inkey', $PubPath, '-rawin', '-in', $SumsPath, '-sigfile', $SigPath)
                if ($Check.Code -ne 0) {
                    Fail "SHA256SUMS.sig does not verify against the release key.`n  The release may have been tampered with. Nothing was installed."
                }
                Say '  signature: ok'
                $Signed = $true
            }
        }

        $Line = Select-String -Path $SumsPath -Pattern ([regex]::Escape($Asset) + '$') |
                Select-Object -First 1
        if (-not $Line) {
            Unverified "SHA256SUMS has no entry for $Asset"
        } else {
            $Expected = ($Line.Line -split '\s+')[0].ToLower()
            $Actual = (Get-FileHash -Path $AssetPath -Algorithm SHA256).Hash.ToLower()
            if ($Expected -ne $Actual) {
                Fail "checksum mismatch for $Asset`n  expected $Expected`n  got      $Actual`n  The download is corrupt. Nothing was installed."
            }
            Say '  checksum: ok'
            $Verified = $true
        }
    } else {
        Unverified 'SHA256SUMS is not available in this release'
    }

    # --- 5. install it somewhere on the PATH --------------------------------
    # A per-user directory is preferred over a machine-wide one: it keeps the installer
    # from needing administrator rights, which is what lets it run from a pipe.
    if ($env:MOTITA_INSTALL_DIR) {
        $Dir = $env:MOTITA_INSTALL_DIR
    } elseif ($env:LOCALAPPDATA) {
        $Dir = Join-Path $env:LOCALAPPDATA "Programs\$BinName"
    } else {
        # No LOCALAPPDATA is a stripped environment rather than a normal one, and the
        # motita home is the same folder the program keeps its own state in.
        $Dir = Join-Path ([Environment]::GetFolderPath('UserProfile')) ".motita\bin"
    }

    if (-not (Test-Path $Dir)) {
        New-Item -ItemType Directory -Path $Dir -Force | Out-Null
    }
    $Target = Join-Path $Dir "$BinName.exe"

    # Windows cannot overwrite a RUNNING binary, so a previous copy is moved aside first -
    # the same rename-aside the in-program updater does, for the same reason. A failed
    # rename falls back to removing the old file rather than failing the install.
    if (Test-Path $Target) {
        $Old = "$Target.old"
        Remove-Item $Old -Force -ErrorAction SilentlyContinue
        try { Move-Item -Path $Target -Destination $Old -Force }
        catch { Remove-Item $Target -Force -ErrorAction SilentlyContinue }
    }
    Copy-Item -Path $AssetPath -Destination $Target -Force
    Say "  installed: $Target"
    if ($Verified) { Say '             (checksum verified against the release)' }
} finally {
    Remove-Item $Tmp -Recurse -Force -ErrorAction SilentlyContinue
}

# --- 6. prove it runs ---------------------------------------------------------
# Reporting success without executing the binary would make this script unable to catch the
# one failure that matters here: a binary that does not run on this machine (the usual
# symptom of a wrong architecture).
try {
    $Reported = & $Target -version 2>&1 | Select-Object -First 1
    Say "  verified:  it runs ($Reported)"
} catch {
    Warn "$Target was installed but does not run on this system"
}

# --- 7. put it on the PATH ----------------------------------------------------
# The User scope and not the Machine one: no administrator rights, and it is the scope a
# NEW shell reads - which is why the last line tells the user to open one.
$UserPath = [Environment]::GetEnvironmentVariable('Path', 'User')
$OnPath = $false
if ($UserPath) {
    foreach ($entry in ($UserPath -split ';')) {
        if ($entry.TrimEnd('\') -ieq $Dir.TrimEnd('\')) { $OnPath = $true }
    }
}
if ($OnPath) {
    Say '  path:      already on your PATH'
} else {
    $NewPath = if ($UserPath) { "$UserPath;$Dir" } else { $Dir }
    [Environment]::SetEnvironmentVariable('Path', $NewPath, 'User')
    $env:Path = "$env:Path;$Dir"
    Say "  path:      added $Dir to your PATH (open a new terminal to pick it up)"
}

if (-not $Signed) {
    Say '  provenance: check where this binary was built with'
    Say "    gh attestation verify `"$Target`" --repo $Repo"
}

Say ''
Say "Next: $BinName    (writes a configuration that works)"
