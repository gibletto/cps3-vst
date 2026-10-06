# SPDX-License-Identifier: AGPL-3.0-only
param([ValidateSet('Debug', 'Release')][string]$Configuration = 'Release')

$ErrorActionPreference = 'Stop'
. "$PSScriptRoot/common.ps1"
$root = Split-Path -Parent $PSScriptRoot
$version = Get-ProjectVersion -Root $root
$artifacts = Join-Path $root "build/CPS3Instrument_artefacts/$Configuration"
$juceSource = (Get-Content -LiteralPath "$root/build/juce-source.txt" -Raw).Trim()
$destination = [IO.Path]::GetFullPath((Join-Path $root 'dist/CPS3Instrument-Windows-x64'))
$distRoot = [IO.Path]::GetFullPath((Join-Path $root 'dist'))
$archive = Join-Path $distRoot "CPS3Instrument-$version-Windows-x64.zip"

$required = @(
    "$artifacts/VST3/CPS3 Instrument.vst3/Contents/x86_64-win/CPS3 Instrument.vst3",
    "$artifacts/Standalone/CPS3 Instrument.exe",
    "$juceSource/LICENSE.md"
)
foreach ($file in $required) {
    if (!(Test-Path -LiteralPath $file -PathType Leaf)) {
        throw "Build first: missing $file"
    }
}
foreach ($binary in $required[0..1]) {
    if ((Get-Item -LiteralPath $binary).VersionInfo.ProductVersion -ne $version) {
        throw "Binary version differs from VERSION; rebuild before packaging: $binary"
    }
}

if ([IO.Path]::GetDirectoryName($destination) -ne $distRoot) {
    throw 'Package directory must be directly inside the workspace dist directory'
}
if (Test-Path -LiteralPath $destination) {
    Remove-Item -LiteralPath $destination -Recurse -Force
}
New-Item -ItemType Directory -Force "$destination/licenses" | Out-Null
Copy-Item -LiteralPath "$artifacts/VST3/CPS3 Instrument.vst3" -Destination $destination -Recurse
Copy-Item -LiteralPath "$artifacts/Standalone/CPS3 Instrument.exe" -Destination $destination
foreach ($name in @('README.md', 'THIRD_PARTY_NOTICES.md', 'LICENSE', 'VERSION')) {
    Copy-Item -LiteralPath (Join-Path $root $name) -Destination $destination
}

$licences = @{
    'JUCE.md' = "$juceSource/LICENSE.md"
    'TinySoundFont.txt' = "$root/external/TinySoundFont/LICENSE"
    'VST3-SDK.txt' = "$juceSource/modules/juce_audio_processors_headless/format_types/VST3_SDK/LICENSE.txt"
    'zlib.txt' = "$juceSource/modules/juce_core/zip/zlib/LICENSE"
    'libpng.txt' = "$juceSource/modules/juce_graphics/image_formats/pnglib/LICENSE"
    'libjpeg.txt' = "$juceSource/modules/juce_graphics/image_formats/jpglib/README"
    'HarfBuzz.txt' = "$juceSource/modules/juce_graphics/fonts/harfbuzz/COPYING"
    'SheenBidi.txt' = "$juceSource/modules/juce_graphics/unicode/sheenbidi/LICENSE"
    'FLAC.txt' = "$juceSource/modules/juce_audio_formats/codecs/flac/Flac Licence.txt"
    'Ogg-Vorbis.txt' = "$juceSource/modules/juce_audio_formats/codecs/oggvorbis/Ogg Vorbis Licence.txt"
}
$goRoot = & go env GOROOT
if ($LASTEXITCODE -ne 0) {
    throw 'Cannot locate the Go licence'
}
$licences['Go.txt'] = Join-Path $goRoot 'LICENSE'
foreach ($name in $licences.Keys) {
    Copy-Item -LiteralPath $licences[$name] -Destination (Join-Path "$destination/licenses" $name)
}

Compress-Archive -LiteralPath $destination -DestinationPath $archive -Force
$hash = (Get-FileHash -LiteralPath $archive -Algorithm SHA256).Hash.ToLowerInvariant()
[IO.File]::WriteAllText("$archive.sha256", "$hash  $([IO.Path]::GetFileName($archive))`n", [Text.UTF8Encoding]::new($false))
Write-Output "Built package: $archive"
