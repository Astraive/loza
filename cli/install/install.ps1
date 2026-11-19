$ErrorActionPreference = "Stop"

$DefaultRepo = "astraive/loza"
$Repo = if ($env:LOZA_REPO) { $env:LOZA_REPO } else { $DefaultRepo }
# Keep the override (e.g. for case variants) but pin it to the official repo.
# A third party could otherwise publish a matching (but malicious) checksum.
if ($Repo -ine $DefaultRepo) {
    throw "LOZA_REPO must be '$DefaultRepo'; arbitrary repositories are not allowed."
}
$InstallDir = if ($env:LOZA_INSTALL_DIR) { $env:LOZA_INSTALL_DIR } else { Join-Path $env:LOCALAPPDATA "Programs\loza\bin" }
$TempDir = Join-Path ([System.IO.Path]::GetTempPath()) ("loza-install-" + [System.Guid]::NewGuid().ToString("N"))

function Assert-HttpsGitHubUrl {
    param([string]$Url, [string]$Kind)
    if ([string]::IsNullOrWhiteSpace($Url)) {
        throw "Missing $Kind URL in release metadata."
    }
    $uri = [Uri]::new($Url)
    if ($uri.Scheme -ne [Uri]::UriSchemeHttps -or $uri.Host -ine "github.com") {
        throw "Refusing non-HTTPS or untrusted $Kind URL: $Url"
    }
}

New-Item -ItemType Directory -Force -Path $TempDir | Out-Null

try {
    $arch = switch ([System.Runtime.InteropServices.RuntimeInformation]::OSArchitecture) {
        "X64" { "amd64" }
        "Arm64" { "arm64" }
        default { throw "Unsupported architecture: $($_)" }
    }

    $releases = Invoke-RestMethod -Uri "https://api.github.com/repos/$Repo/releases" -Headers @{ "User-Agent" = "loza-installer" }
    $cliRelease = $releases | Where-Object {
        $_.assets | Where-Object { $_.name -like "loza_*_windows_$arch.zip" }
    } | Select-Object -First 1
    if (-not $cliRelease) {
        throw "Could not find a LOZA CLI release."
    }

    $asset = $cliRelease.assets | Where-Object { $_.name -eq "loza_$($cliRelease.tag_name.TrimStart('cli/v'))_windows_$arch.zip" } | Select-Object -First 1
    if (-not $asset) {
        $asset = $cliRelease.assets | Where-Object { $_.name -like "loza_*_windows_$arch.zip" } | Select-Object -First 1
    }
    if (-not $asset) {
        throw "Could not find a LOZA CLI Windows $arch asset."
    }

    Assert-HttpsGitHubUrl $asset.browser_download_url "release asset"

    # Fixed local names keep release-controlled asset names out of the filesystem path.
    $zip = Join-Path $TempDir "loza.zip"
    Invoke-WebRequest -Uri $asset.browser_download_url -OutFile $zip

    # The release workflow publishes "<asset>.sha256" beside every archive. A
    # missing, malformed, or mismatched checksum aborts before anything is installed.
    $checksumUrl = "$($asset.browser_download_url).sha256"
    Assert-HttpsGitHubUrl $checksumUrl "release checksum"
    $checksumPath = Join-Path $TempDir "loza.zip.sha256"
    Invoke-WebRequest -Uri $checksumUrl -OutFile $checksumPath

    $reader = [System.IO.File]::OpenText($checksumPath)
    try {
        $line = $reader.ReadLine()
    }
    finally {
        $reader.Dispose()
    }
    if ([string]::IsNullOrWhiteSpace($line)) {
        throw "Release checksum file is empty."
    }
    $fields = $line.Trim() -split '\s+', 2
    if ($fields.Count -ne 2) {
        throw "Malformed release checksum file."
    }
    $expected = $fields[0]
    $named = $fields[1].Trim()
    if ($expected -notmatch '^[0-9a-fA-F]{64}$') {
        throw "Malformed release checksum value."
    }
    if ($named -cne $asset.name) {
        throw "Release checksum is for a different asset."
    }

    $actual = (Get-FileHash -Algorithm SHA256 -Path $zip).Hash.ToLowerInvariant()
    if ($actual -ne $expected.ToLowerInvariant()) {
        throw "Checksum mismatch: release asset did not match published SHA-256."
    }

    Expand-Archive -Path $zip -DestinationPath $TempDir -Force

    New-Item -ItemType Directory -Force -Path $InstallDir | Out-Null
    Copy-Item -Force -Path (Join-Path $TempDir "loza.exe") -Destination (Join-Path $InstallDir "loza.exe")

    Write-Host "Installed loza to $(Join-Path $InstallDir 'loza.exe') (SHA-256 verified)"
    if (($env:PATH -split ";") -notcontains $InstallDir) {
        Write-Host "Add $InstallDir to PATH to run loza from any PowerShell session."
    }
}
finally {
    Remove-Item -Recurse -Force -Path $TempDir -ErrorAction SilentlyContinue
}
