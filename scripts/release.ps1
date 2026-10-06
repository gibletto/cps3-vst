# SPDX-License-Identifier: AGPL-3.0-only
param(
    [Parameter(Mandatory)][string]$Tag,
    [Parameter(Mandatory)][string]$Version,
    [Parameter(Mandatory)][string]$AssetsDirectory
)

$ErrorActionPreference = 'Stop'
$PSNativeCommandUseErrorActionPreference = $false
if ($Tag -ne "v$Version" -or $Version -notmatch '\A[0-9]+\.[0-9]+\.[0-9]+\z') {
    throw 'The release tag and binary version must match'
}

$archiveName = "CPS3Instrument-$Version-Windows-x64.zip"
$archive = Join-Path $AssetsDirectory $archiveName
$checksum = "$archive.sha256"
$expected = (Get-Content -LiteralPath $checksum -Raw).Split(' ')[0]
if ((Get-FileHash -LiteralPath $archive).Hash.ToLowerInvariant() -ne $expected) {
    throw 'Release archive checksum differs'
}

$existing = & gh release view $Tag --repo $env:GH_REPO --json assets 2>$null
if ($LASTEXITCODE -ne 0) {
    & gh release create $Tag $archive $checksum --repo $env:GH_REPO --verify-tag --draft --generate-notes --title "CPS3 Instrument $Version"
    if ($LASTEXITCODE -ne 0) {
        throw 'Cannot create draft release'
    }
    return
}

$names = @(($existing | ConvertFrom-Json).assets.name)
if ($archiveName -in $names -and "$archiveName.sha256" -in $names) {
    Write-Output 'Windows release files are already attached'
    return
}

if ($archiveName -in $names) {
    $download = Join-Path $AssetsDirectory 'existing-release'
    & gh release download $Tag --repo $env:GH_REPO --pattern $archiveName --dir $download
    if ($LASTEXITCODE -ne 0) {
        throw 'Cannot verify the existing release archive'
    }
    $hash = (Get-FileHash -LiteralPath (Join-Path $download $archiveName)).Hash.ToLowerInvariant()
    [IO.File]::WriteAllText($checksum, "$hash  $archiveName`n", [Text.UTF8Encoding]::new($false))
} elseif ("$archiveName.sha256" -in $names) {
    $download = Join-Path $AssetsDirectory 'existing-release'
    & gh release download $Tag --repo $env:GH_REPO --pattern "$archiveName.sha256" --dir $download
    if ($LASTEXITCODE -ne 0) {
        throw 'Cannot verify the existing release checksum'
    }
    $publishedHash = (Get-Content -LiteralPath (Join-Path $download "$archiveName.sha256") -Raw).Split(' ')[0]
    if ($publishedHash -ne $expected) {
        throw 'Existing release checksum belongs to another archive; use a new version'
    }
}

$missing = @()
if ($archiveName -notin $names) {
    $missing += $archive
}
if ("$archiveName.sha256" -notin $names) {
    $missing += $checksum
}
& gh release upload $Tag @missing --repo $env:GH_REPO
if ($LASTEXITCODE -ne 0) {
    throw 'Cannot attach Windows release files'
}
