# SPDX-License-Identifier: AGPL-3.0-only

function Get-ProjectVersion {
    param([string]$Root)

    $version = (Get-Content -LiteralPath (Join-Path $Root 'VERSION') -Raw).Trim()
    if ($version -notmatch '\A(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\z') {
        throw 'VERSION must contain a version such as 0.1.1'
    }
    return $version
}

function Get-CMakeExecutable {
    $nativeCMake = Join-Path $env:ProgramFiles 'CMake/bin/cmake.exe'
    if (Test-Path -LiteralPath $nativeCMake) {
        return $nativeCMake
    }
    return (Get-Command cmake -ErrorAction Stop).Source
}
