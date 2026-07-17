$ErrorActionPreference = "Stop"
Set-StrictMode -Version Latest

$repoRoot = [System.IO.Path]::GetFullPath((Join-Path $PSScriptRoot ".."))
$versionFile = Join-Path $PSScriptRoot "version"
$versionPattern = '^\d+\.\d+\.\d+(-[0-9A-Za-z.-]+)?(\+[0-9A-Za-z.-]+)?$'

function Invoke-Checked {
    param(
        [Parameter(Mandatory = $true)][string]$FilePath,
        [string[]]$Arguments = @()
    )
    & $FilePath @Arguments
    if ($LASTEXITCODE -ne 0) {
        throw "$FilePath failed with exit code $LASTEXITCODE"
    }
}

function Get-CheckedOutput {
    param(
        [Parameter(Mandatory = $true)][string]$FilePath,
        [string[]]$Arguments = @()
    )
    $output = & $FilePath @Arguments
    if ($LASTEXITCODE -ne 0) {
        throw "$FilePath failed with exit code $LASTEXITCODE"
    }
    return @($output)
}

function Write-Utf8NoBom {
    param(
        [Parameter(Mandatory = $true)][string]$Path,
        [Parameter(Mandatory = $true)][string]$Content
    )
    [System.IO.File]::WriteAllText($Path, $Content, [System.Text.UTF8Encoding]::new($false))
}

function Update-PackageVersion {
    param(
        [Parameter(Mandatory = $true)][string]$Path,
        [Parameter(Mandatory = $true)][string]$Version
    )
    $package = Get-Content -LiteralPath $Path -Raw | ConvertFrom-Json
    $package.version = $Version
    if ($package.name -eq "@deqiying/fast-context") {
        foreach ($name in @(
            "@deqiying/fast-context-win32-x64",
            "@deqiying/fast-context-linux-x64",
            "@deqiying/fast-context-darwin-arm64"
        )) {
            $property = $package.optionalDependencies.PSObject.Properties | Where-Object { $_.Name -eq $name } | Select-Object -First 1
            if ($null -eq $property) {
                throw "Missing optional dependency $name in $Path"
            }
            $property.Value = $Version
        }
    }
    return ($package | ConvertTo-Json -Depth 20) + [Environment]::NewLine
}

if (-not (Test-Path -LiteralPath $versionFile)) {
    throw "Missing .deploy/version"
}
$version = (Get-Content -LiteralPath $versionFile -Raw).Trim()
if ($version -notmatch $versionPattern) {
    throw ".deploy/version must contain a valid SemVer value"
}
$tagName = "v$version"

Push-Location $repoRoot
try {
    $versionFiles = @(
        ".deploy/version",
        "npm/fast-context/package.json",
        "npm/packages/win32-x64/package.json",
        "npm/packages/linux-x64/package.json",
        "npm/packages/darwin-arm64/package.json"
    )

    $dirtyPaths = @()
    foreach ($line in @(Get-CheckedOutput -FilePath "git" -Arguments @("status", "--porcelain", "--untracked-files=all"))) {
        if ($line.Length -lt 4) {
            continue
        }
        $path = $line.Substring(3).Trim('"').Replace('\', '/')
        if ($path -like "* -> *") {
            $path = ($path -split " -> ")[-1]
        }
        $dirtyPaths += $path
    }
    $unexpected = @($dirtyPaths | Where-Object { $versionFiles -notcontains $_ })
    if ($unexpected.Count -gt 0) {
        throw "Working tree has unrelated changes. Commit or stash them before release preparation:`n$($unexpected -join "`n")"
    }

    & git rev-parse -q --verify "refs/tags/$tagName" *> $null
    if ($LASTEXITCODE -eq 0) {
        throw "Tag $tagName already exists"
    }

    foreach ($path in $versionFiles) {
        if ($path -eq ".deploy/version") {
            continue
        }
        $fullPath = Join-Path $repoRoot $path
        Write-Utf8NoBom -Path $fullPath -Content (Update-PackageVersion -Path $fullPath -Version $version)
    }

    Invoke-Checked -FilePath "git" -Arguments (@("add", "--") + $versionFiles)
    & git diff --cached --quiet -- @versionFiles
    if ($LASTEXITCODE -gt 1) {
        throw "git diff failed with exit code $LASTEXITCODE"
    }
    if ($LASTEXITCODE -eq 1) {
        Invoke-Checked -FilePath "git" -Arguments @("commit", "-m", "发布 $version")
    }
    else {
        Write-Host "No version changes detected; tagging current HEAD."
    }

    Invoke-Checked -FilePath "git" -Arguments @("tag", $tagName)
    $head = ((Get-CheckedOutput -FilePath "git" -Arguments @("rev-parse", "--short", "HEAD")) -join "").Trim()
    Write-Host "Prepared $tagName at $head. This script did not push or publish."
    Write-Host "Review the commit and tag, then push them explicitly when ready."
}
finally {
    Pop-Location
}
