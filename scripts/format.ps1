# SPDX-License-Identifier: AGPL-3.0-only
param([switch]$Check)

$ErrorActionPreference = 'Stop'
$root = Split-Path -Parent $PSScriptRoot
$formatter = Get-Command clang-format -ErrorAction SilentlyContinue
if ($formatter) {
    $clangFormat = $formatter.Source
} else {
    $vswhere = Join-Path ${env:ProgramFiles(x86)} 'Microsoft Visual Studio/Installer/vswhere.exe'
    if (!(Test-Path -LiteralPath $vswhere)) {
        throw 'Install clang-format 19.1.5, or the Visual Studio LLVM tools'
    }
    $installation = & $vswhere -latest -products '*' -property installationPath
    $clangFormat = Join-Path $installation 'VC/Tools/Llvm/x64/bin/clang-format.exe'
    if (!(Test-Path -LiteralPath $clangFormat)) {
        throw 'Install clang-format 19.1.5, or the Visual Studio LLVM tools'
    }
}

$formatterVersion = & $clangFormat --version
if ($LASTEXITCODE -ne 0 -or $formatterVersion -notmatch 'version 19\.') {
    throw 'Use clang-format 19 (CI uses 19.1.5)'
}

$cppFiles = @(Get-ChildItem -LiteralPath "$root/Source", "$root/tests" -Recurse -File |
    Where-Object Extension -In '.cpp', '.h' | Sort-Object FullName | Select-Object -ExpandProperty FullName)
$goFiles = @(Get-ChildItem -LiteralPath "$root/tools/sf3bridge", "$root/tests/fixtures" -Recurse -Filter '*.go' -File |
    Sort-Object FullName | Select-Object -ExpandProperty FullName)

if ($Check) {
    & $clangFormat --dry-run --Werror @cppFiles
    if ($LASTEXITCODE -ne 0) {
        throw 'C++ formatting differs; run scripts/format.ps1'
    }
    $unformatted = @(& gofmt -l @goFiles)
    if ($LASTEXITCODE -ne 0 -or $unformatted.Count -gt 0) {
        throw "Go formatting differs; run scripts/format.ps1: $($unformatted -join ', ')"
    }
} else {
    & $clangFormat -i @cppFiles
    if ($LASTEXITCODE -ne 0) {
        throw 'C++ formatting failed'
    }
    & gofmt -w @goFiles
    if ($LASTEXITCODE -ne 0) {
        throw 'Go formatting failed'
    }
}
