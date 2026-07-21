param(
    [Parameter(Position = 0)]
    [string]$Version
)

$ErrorActionPreference = "Stop"
Set-StrictMode -Version Latest

$repoRoot = [System.IO.Path]::GetFullPath((Join-Path $PSScriptRoot ".."))
$versionFile = Join-Path $PSScriptRoot "version"
# SemVer 2.0.0: numeric identifiers must not have leading zeroes, while
# build metadata may contain any non-empty dot-separated identifier.
$versionPattern = '^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-((0|[1-9][0-9]*|[0-9]*[A-Za-z-][0-9A-Za-z-]*)(\.(0|[1-9][0-9]*|[0-9]*[A-Za-z-][0-9A-Za-z-]*))*))?(\+([0-9A-Za-z-]+(\.[0-9A-Za-z-]+)*))?$'
$versionWasProvided = $PSBoundParameters.ContainsKey("Version")

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

function Get-ReleaseCommitMessage {
    param(
        [Parameter(Mandatory = $true)][string]$Version
    )

    # Keep the script literal ASCII-safe for Windows PowerShell 5.1.
    $releasePrefix = [string]::Concat([char]0x53D1, [char]0x5E03)
    return "$releasePrefix $Version"
}

function Invoke-GitCommitWithUtf8Message {
    param(
        [Parameter(Mandatory = $true)][string]$Message
    )

    $messageFile = [System.IO.Path]::GetTempFileName()
    try {
        Write-Utf8NoBom -Path $messageFile -Content ($Message + "`n")
        Invoke-Checked -FilePath "git" -Arguments @("commit", "-F", $messageFile)
    }
    finally {
        Remove-Item -LiteralPath $messageFile -Force -ErrorAction SilentlyContinue
    }
}

function Update-JsonStringProperty {
    param(
        [Parameter(Mandatory = $true)][string]$Content,
        [Parameter(Mandatory = $true)][string]$PropertyName,
        [Parameter(Mandatory = $true)][string]$Value,
        [Parameter(Mandatory = $true)][string]$Path
    )

    $escapedPropertyName = [regex]::Escape($PropertyName)
    $regex = [regex]('(?m)^(\s*"' + $escapedPropertyName + '"\s*:\s*")([^"]*)(")')
    $matches = $regex.Matches($Content)
    if ($matches.Count -ne 1) {
        throw "Expected exactly one $PropertyName property in $Path"
    }

    return $regex.Replace(
        $Content,
        { param($match) $match.Groups[1].Value + $Value + $match.Groups[3].Value },
        1
    )
}

function Update-PackageVersion {
    param(
        [Parameter(Mandatory = $true)][string]$Path,
        [Parameter(Mandatory = $true)][string]$Version
    )
    $content = Get-Content -LiteralPath $Path -Raw
    $package = $content | ConvertFrom-Json
    $content = Update-JsonStringProperty -Content $content -PropertyName "version" -Value $Version -Path $Path
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
            $content = Update-JsonStringProperty -Content $content -PropertyName $name -Value $Version -Path $Path
        }
    }
    return $content
}

if (-not (Test-Path -LiteralPath $versionFile)) {
    throw "Missing .deploy/version"
}
$releaseVersion = if ($versionWasProvided) {
    $Version
}
else {
    (Get-Content -LiteralPath $versionFile -Raw).Trim()
}
if ($releaseVersion -notmatch $versionPattern) {
    throw "Release version must be a valid SemVer value"
}
$tagName = "v$releaseVersion"

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

    if ($versionWasProvided) {
        Write-Utf8NoBom -Path $versionFile -Content ($releaseVersion + "`n")
    }

    foreach ($path in $versionFiles) {
        if ($path -eq ".deploy/version") {
            continue
        }
        $fullPath = Join-Path $repoRoot $path
        Write-Utf8NoBom -Path $fullPath -Content (Update-PackageVersion -Path $fullPath -Version $releaseVersion)
    }

    Invoke-Checked -FilePath "git" -Arguments (@("add", "--") + $versionFiles)
    & git diff --cached --quiet -- @versionFiles
    if ($LASTEXITCODE -gt 1) {
        throw "git diff failed with exit code $LASTEXITCODE"
    }
    if ($LASTEXITCODE -eq 1) {
        $commitMessage = Get-ReleaseCommitMessage -Version $releaseVersion
        Invoke-GitCommitWithUtf8Message -Message $commitMessage
    }
    else {
        Write-Host "No version changes detected; tagging current HEAD."
    }

    Invoke-Checked -FilePath "git" -Arguments @("tag", $tagName)
    $head = ((Get-CheckedOutput -FilePath "git" -Arguments @("rev-parse", "--short", "HEAD")) -join "").Trim()
    Write-Host "Prepared $tagName at $head. This script did not push or publish."
    Write-Host "Review the commit and tag, then push them explicitly when ready."
    Write-Host "  git push origin main"
    Write-Host "  git push origin $tagName"
}
finally {
    Pop-Location
}
