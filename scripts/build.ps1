# SPDX-License-Identifier: AGPL-3.0-only
param(
    [ValidateSet('Debug', 'Release')]
    [string]$Configuration = 'Release',
    [ValidateRange(1, 64)]
    [int]$Jobs = 2
)

$ErrorActionPreference = 'Stop'
. "$PSScriptRoot/common.ps1"
$root = Split-Path -Parent $PSScriptRoot
$cmake = Get-CMakeExecutable
$version = Get-ProjectVersion -Root $root

if ($env:GITHUB_REF_TYPE -eq 'tag' -and $env:GITHUB_REF_NAME -ne "v$version") {
    throw "Release tag must match VERSION: v$version"
}

& $cmake -S $root -B "$root/build" -G 'Visual Studio 17 2022' -A x64 -DCPS3_BUILD_TESTS=ON
if ($LASTEXITCODE -ne 0) {
    throw 'Configure failed'
}

& $cmake --build "$root/build" --config $Configuration --parallel $Jobs
if ($LASTEXITCODE -ne 0) {
    throw 'Build failed'
}

$ctest = Join-Path (Split-Path -Parent $cmake) 'ctest.exe'
& $ctest --test-dir "$root/build" -C $Configuration --output-on-failure
if ($LASTEXITCODE -ne 0) {
    throw 'Tests failed'
}

Push-Location "$root/tools/sf3bridge"
try {
    & go vet ./...
    if ($LASTEXITCODE -ne 0) {
        throw 'Go vet failed'
    }
} finally {
    Pop-Location
}

& "$PSScriptRoot/package.ps1" -Configuration $Configuration
