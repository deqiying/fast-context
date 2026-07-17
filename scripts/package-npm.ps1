param(
    [string]$PrebuiltRoot = "",
    [switch]$SkipSmoke
)

$ErrorActionPreference = "Stop"
Set-StrictMode -Version Latest

$repoRoot = [System.IO.Path]::GetFullPath((Join-Path $PSScriptRoot ".."))
$distRoot = Join-Path $repoRoot "dist"
$stageRoot = Join-Path $distRoot "npm-stage"
$packageDistRoot = Join-Path $distRoot "npm"
$cacheRoot = Join-Path $distRoot "cache"
$versionFile = Join-Path $repoRoot ".deploy\version"
$licenseFile = Join-Path $repoRoot "LICENSE"
$versionPattern = '^\d+\.\d+\.\d+(-[0-9A-Za-z.-]+)?(\+[0-9A-Za-z.-]+)?$'

function Invoke-Checked {
    param(
        [Parameter(Mandatory = $true)]
        [string]$FilePath,
        [string[]]$Arguments = @()
    )

    & $FilePath @Arguments
    if ($LASTEXITCODE -ne 0) {
        throw "$FilePath failed with exit code $LASTEXITCODE"
    }
}

function Get-CheckedOutput {
    param(
        [Parameter(Mandatory = $true)]
        [string]$FilePath,
        [string[]]$Arguments = @()
    )

    $output = & $FilePath @Arguments
    if ($LASTEXITCODE -ne 0) {
        throw "$FilePath failed with exit code $LASTEXITCODE"
    }
    return @($output)
}

function Reset-DistDirectory {
    param(
        [Parameter(Mandatory = $true)]
        [string]$Path
    )

    $fullPath = [System.IO.Path]::GetFullPath($Path)
    $fullDistRoot = [System.IO.Path]::GetFullPath($distRoot)
    $requiredPrefix = $fullDistRoot.TrimEnd([System.IO.Path]::DirectorySeparatorChar, [System.IO.Path]::AltDirectorySeparatorChar) + [System.IO.Path]::DirectorySeparatorChar
    if (-not $fullPath.StartsWith($requiredPrefix, [System.StringComparison]::OrdinalIgnoreCase)) {
        throw "Refusing to reset a directory outside dist: $fullPath"
    }
    if (Test-Path -LiteralPath $fullPath) {
        Remove-Item -LiteralPath $fullPath -Recurse -Force
    }
    New-Item -ItemType Directory -Force -Path $fullPath | Out-Null
}

function Read-PackageManifest {
    param([Parameter(Mandatory = $true)][string]$Path)
    return Get-Content -LiteralPath $Path -Raw | ConvertFrom-Json
}

function Get-JsonPropertyValue {
    param(
        [Parameter(Mandatory = $true)]$Object,
        [Parameter(Mandatory = $true)][string]$Name
    )

    $property = $Object.PSObject.Properties | Where-Object { $_.Name -eq $Name } | Select-Object -First 1
    if ($null -eq $property) {
        return $null
    }
    return $property.Value
}

function Assert-StageFiles {
    param(
        [Parameter(Mandatory = $true)][string]$StageDir,
        [Parameter(Mandatory = $true)][string[]]$Expected
    )

    $actual = @(Get-ChildItem -LiteralPath $StageDir -File -Recurse | ForEach-Object {
        [System.IO.Path]::GetRelativePath($StageDir, $_.FullName).Replace('\', '/')
    } | Sort-Object)
    $wanted = @($Expected | Sort-Object)
    if (($actual -join "`n") -ne ($wanted -join "`n")) {
        throw "Unexpected staging files in $StageDir.`nExpected:`n$($wanted -join "`n")`nActual:`n$($actual -join "`n")"
    }
}

function Get-NativeTargetID {
    $architecture = [System.Runtime.InteropServices.RuntimeInformation]::OSArchitecture.ToString().ToLowerInvariant()
    if ($architecture -eq "amd64") {
        $architecture = "x64"
    }
    if ([System.Runtime.InteropServices.RuntimeInformation]::IsOSPlatform([System.Runtime.InteropServices.OSPlatform]::Windows)) {
        return "win32-$architecture"
    }
    if ([System.Runtime.InteropServices.RuntimeInformation]::IsOSPlatform([System.Runtime.InteropServices.OSPlatform]::Linux)) {
        return "linux-$architecture"
    }
    if ([System.Runtime.InteropServices.RuntimeInformation]::IsOSPlatform([System.Runtime.InteropServices.OSPlatform]::OSX)) {
        return "darwin-$architecture"
    }
    return "unsupported-$architecture"
}

if (-not (Test-Path -LiteralPath $versionFile)) {
    throw "Missing .deploy/version"
}
if (-not (Test-Path -LiteralPath $licenseFile)) {
    throw "Missing LICENSE"
}

$version = (Get-Content -LiteralPath $versionFile -Raw).Trim()
if ($version -notmatch $versionPattern) {
    throw ".deploy/version is not valid SemVer: $version"
}

$platforms = @(
    @{
        ID = "win32-x64"
        SourceDir = "npm\packages\win32-x64"
        PackageName = "@deqiying/fast-context-win32-x64"
        GOOS = "windows"
        GOARCH = "amd64"
        BinaryName = "fast-context.exe"
    },
    @{
        ID = "linux-x64"
        SourceDir = "npm\packages\linux-x64"
        PackageName = "@deqiying/fast-context-linux-x64"
        GOOS = "linux"
        GOARCH = "amd64"
        BinaryName = "fast-context"
    },
    @{
        ID = "darwin-arm64"
        SourceDir = "npm\packages\darwin-arm64"
        PackageName = "@deqiying/fast-context-darwin-arm64"
        GOOS = "darwin"
        GOARCH = "arm64"
        BinaryName = "fast-context"
    }
)

$entrySourceDir = Join-Path $repoRoot "npm\fast-context"
$entryManifestPath = Join-Path $entrySourceDir "package.json"
$entryManifest = Read-PackageManifest -Path $entryManifestPath
$entryPackageName = "@deqiying/fast-context"
if ($entryManifest.name -ne $entryPackageName) {
    throw "npm/fast-context/package.json must use package name $entryPackageName"
}
if ($entryManifest.version -ne $version) {
    throw "npm/fast-context/package.json version $($entryManifest.version) does not match $version"
}
if ((Get-JsonPropertyValue -Object $entryManifest.dependencies -Name "@vscode/ripgrep") -ne "1.18.0") {
    throw "@vscode/ripgrep must be pinned to 1.18.0"
}

foreach ($platform in $platforms) {
    $manifestPath = Join-Path $repoRoot (Join-Path $platform.SourceDir "package.json")
    $manifest = Read-PackageManifest -Path $manifestPath
    if ($manifest.name -ne $platform.PackageName) {
        throw "$manifestPath has unexpected package name $($manifest.name)"
    }
    if ($manifest.version -ne $version) {
        throw "$manifestPath version $($manifest.version) does not match $version"
    }
    $dependencyVersion = Get-JsonPropertyValue -Object $entryManifest.optionalDependencies -Name $platform.PackageName
    if ($dependencyVersion -ne $version) {
        throw "Entry optional dependency $($platform.PackageName) version $dependencyVersion does not match $version"
    }
}

$commit = ((Get-CheckedOutput -FilePath "git" -Arguments @("rev-parse", "HEAD")) -join "").Trim()
$dirtyState = (Get-CheckedOutput -FilePath "git" -Arguments @("status", "--porcelain", "--untracked-files=all")) -join "`n"
if (-not [string]::IsNullOrWhiteSpace($dirtyState)) {
    $commit += "-dirty"
}
$buildDate = [DateTime]::UtcNow.ToString("yyyy-MM-ddTHH:mm:ssZ", [Globalization.CultureInfo]::InvariantCulture)
$ldflags = "-s -w -X github.com/deqiying/fast-context/internal/version.Version=$version -X github.com/deqiying/fast-context/internal/version.Commit=$commit -X github.com/deqiying/fast-context/internal/version.Date=$buildDate"

Reset-DistDirectory -Path $stageRoot
Reset-DistDirectory -Path $packageDistRoot
New-Item -ItemType Directory -Force -Path $cacheRoot | Out-Null

$oldEnvironment = @{
    CGO_ENABLED = $env:CGO_ENABLED
    GOOS = $env:GOOS
    GOARCH = $env:GOARCH
    GOCACHE = $env:GOCACHE
    GOMODCACHE = $env:GOMODCACHE
    GOPATH = $env:GOPATH
    GOTELEMETRY = $env:GOTELEMETRY
    npm_config_cache = $env:npm_config_cache
    FC_RG_PATH = $env:FC_RG_PATH
    WINDSURF_API_KEY = $env:WINDSURF_API_KEY
}

try {
    $env:GOCACHE = Join-Path $cacheRoot "go-build"
    $env:GOMODCACHE = Join-Path $cacheRoot "go-mod"
    $env:GOPATH = Join-Path $cacheRoot "go-path"
    $env:GOTELEMETRY = "off"
    $env:npm_config_cache = Join-Path $cacheRoot "npm"
    New-Item -ItemType Directory -Force -Path $env:GOCACHE, $env:GOMODCACHE, $env:GOPATH, $env:npm_config_cache | Out-Null

    foreach ($platform in $platforms) {
        $stageDir = Join-Path $stageRoot $platform.ID
        $binDir = Join-Path $stageDir "bin"
        New-Item -ItemType Directory -Force -Path $binDir | Out-Null
        Copy-Item -LiteralPath (Join-Path $repoRoot (Join-Path $platform.SourceDir "package.json")) -Destination (Join-Path $stageDir "package.json")
        Copy-Item -LiteralPath $licenseFile -Destination (Join-Path $stageDir "LICENSE")

        $outputPath = Join-Path $binDir $platform.BinaryName
        if ([string]::IsNullOrWhiteSpace($PrebuiltRoot)) {
            $env:CGO_ENABLED = "0"
            $env:GOOS = $platform.GOOS
            $env:GOARCH = $platform.GOARCH
            Invoke-Checked -FilePath "go" -Arguments @("build", "-trimpath", "-ldflags", $ldflags, "-o", $outputPath, ".\cmd\fast-context")
        }
        else {
            $prebuiltPath = Join-Path ([System.IO.Path]::GetFullPath($PrebuiltRoot)) (Join-Path $platform.ID $platform.BinaryName)
            if (-not (Test-Path -LiteralPath $prebuiltPath -PathType Leaf)) {
                throw "Missing prebuilt binary: $prebuiltPath"
            }
            Copy-Item -LiteralPath $prebuiltPath -Destination $outputPath
        }

        if ($platform.GOOS -ne "windows") {
            Invoke-Checked -FilePath "chmod" -Arguments @("+x", $outputPath)
        }
        Assert-StageFiles -StageDir $stageDir -Expected @("bin/$($platform.BinaryName)", "LICENSE", "package.json")
    }

    $entryStageDir = Join-Path $stageRoot "fast-context"
    New-Item -ItemType Directory -Force -Path (Join-Path $entryStageDir "bin") | Out-Null
    Copy-Item -LiteralPath $entryManifestPath -Destination (Join-Path $entryStageDir "package.json")
    Copy-Item -LiteralPath (Join-Path $entrySourceDir "README.md") -Destination (Join-Path $entryStageDir "README.md")
    Copy-Item -LiteralPath (Join-Path $entrySourceDir "bin\fast-context.js") -Destination (Join-Path $entryStageDir "bin\fast-context.js")
    Copy-Item -LiteralPath $licenseFile -Destination (Join-Path $entryStageDir "LICENSE")
    Assert-StageFiles -StageDir $entryStageDir -Expected @("bin/fast-context.js", "LICENSE", "package.json", "README.md")

    $rootLicenseHash = (Get-FileHash -LiteralPath $licenseFile -Algorithm SHA256).Hash
    foreach ($stageDir in @(Get-ChildItem -LiteralPath $stageRoot -Directory)) {
        $stageHash = (Get-FileHash -LiteralPath (Join-Path $stageDir.FullName "LICENSE") -Algorithm SHA256).Hash
        if ($stageHash -ne $rootLicenseHash) {
            throw "LICENSE hash mismatch in $($stageDir.FullName)"
        }
    }

    $nativeTargetID = Get-NativeTargetID
    $nativePlatform = $platforms | Where-Object { $_.ID -eq $nativeTargetID } | Select-Object -First 1
    if ($null -eq $nativePlatform) {
        throw "Local smoke is unsupported on $nativeTargetID"
    }
    $nativeBinary = Join-Path $stageRoot (Join-Path $nativePlatform.ID (Join-Path "bin" $nativePlatform.BinaryName))
    $nativeVersion = (Get-CheckedOutput -FilePath $nativeBinary -Arguments @("--version")) -join "`n"
    if ($nativeVersion -notmatch [regex]::Escape($version)) {
        throw "Native binary version output does not contain $version`: $nativeVersion"
    }

    $packResults = @()
    $packOrder = @($platforms | ForEach-Object { @{ Name = $_.PackageName; StageDir = (Join-Path $stageRoot $_.ID) } })
    $packOrder += @{ Name = $entryPackageName; StageDir = $entryStageDir }
    foreach ($package in $packOrder) {
        Push-Location $package.StageDir
        try {
            $dryRunText = (& npm pack --dry-run --json | Out-String)
            if ($LASTEXITCODE -ne 0) {
                throw "npm pack --dry-run failed for $($package.Name)"
            }
            $dryRun = @($dryRunText | ConvertFrom-Json)[0]
            $dryRunFiles = @($dryRun.files | ForEach-Object { $_.path } | Sort-Object)
            $expectedFiles = if ($package.Name -eq $entryPackageName) {
                @("LICENSE", "README.md", "bin/fast-context.js", "package.json")
            }
            else {
                $platform = $platforms | Where-Object { $_.PackageName -eq $package.Name } | Select-Object -First 1
                @("LICENSE", "bin/$($platform.BinaryName)", "package.json")
            }
            if (($dryRunFiles -join "`n") -ne (($expectedFiles | Sort-Object) -join "`n")) {
                throw "npm pack allowlist mismatch for $($package.Name). Files: $($dryRunFiles -join ', ')"
            }

            $packText = (& npm pack --json --pack-destination $packageDistRoot | Out-String)
            if ($LASTEXITCODE -ne 0) {
                throw "npm pack failed for $($package.Name)"
            }
            $packed = @($packText | ConvertFrom-Json)[0]
            $tarballPath = Join-Path $packageDistRoot $packed.filename
            $packResults += [pscustomobject]@{
                name = $packed.name
                version = $packed.version
                filename = $packed.filename
                sha256 = (Get-FileHash -LiteralPath $tarballPath -Algorithm SHA256).Hash.ToLowerInvariant()
                shasum = $packed.shasum
                integrity = $packed.integrity
                files = @($packed.files | ForEach-Object { $_.path })
            }
        }
        finally {
            Pop-Location
        }
    }

    $packResults | ConvertTo-Json -Depth 20 | Set-Content -LiteralPath (Join-Path $packageDistRoot "pack-results.json") -Encoding utf8NoBOM
    $checksumLines = @(Get-ChildItem -LiteralPath $packageDistRoot -Filter "*.tgz" | Sort-Object Name | ForEach-Object {
        "$((Get-FileHash -LiteralPath $_.FullName -Algorithm SHA256).Hash.ToLowerInvariant())  $($_.Name)"
    })
    $checksumLines | Set-Content -LiteralPath (Join-Path $packageDistRoot "checksums.txt") -Encoding utf8NoBOM

    if (-not $SkipSmoke) {
        $smokePrefix = Join-Path $distRoot "npm-smoke"
        Reset-DistDirectory -Path $smokePrefix
        $entryTarball = Join-Path $packageDistRoot (($packResults | Where-Object { $_.name -eq $entryPackageName }).filename)
        $platformTarball = Join-Path $packageDistRoot (($packResults | Where-Object { $_.name -eq $nativePlatform.PackageName }).filename)
        Invoke-Checked -FilePath "npm" -Arguments @("install", "--global", "--prefix", $smokePrefix, $platformTarball, $entryTarball)

        $commandPath = if ($nativeTargetID -eq "win32-x64") {
            Join-Path $smokePrefix "fast-context.cmd"
        }
        else {
            Join-Path $smokePrefix "bin\fast-context"
        }
        $installedVersion = (Get-CheckedOutput -FilePath $commandPath -Arguments @("--version")) -join "`n"
        if ($installedVersion -notmatch [regex]::Escape($version)) {
            throw "Installed launcher version output does not contain $version`: $installedVersion"
        }

        & $commandPath "__launcher_exit_code_probe__" *> $null
        if ($LASTEXITCODE -ne 2) {
            throw "Launcher did not preserve Go exit code 2; got $LASTEXITCODE"
        }

        $env:FC_RG_PATH = $null
        $env:WINDSURF_API_KEY = "fixture-api-key-123456"
        $doctorText = (Get-CheckedOutput -FilePath $commandPath -Arguments @("doctor", "--project", $repoRoot, "--format", "json")) -join "`n"
        $doctor = $doctorText | ConvertFrom-Json
        if (-not $doctor.ripgrep.ok -or $doctor.ripgrep.source -ne "fc_rg_path") {
            throw "Installed launcher did not inject bundled ripgrep: $doctorText"
        }
        if ($doctor.ripgrep.path -notmatch '@vscode[\\/]ripgrep') {
            throw "Doctor ripgrep path does not belong to @vscode/ripgrep: $($doctor.ripgrep.path)"
        }
    }

    Write-Host "Created npm packages for fast-context $version in $packageDistRoot"
}
finally {
    $env:CGO_ENABLED = $oldEnvironment.CGO_ENABLED
    $env:GOOS = $oldEnvironment.GOOS
    $env:GOARCH = $oldEnvironment.GOARCH
    $env:GOCACHE = $oldEnvironment.GOCACHE
    $env:GOMODCACHE = $oldEnvironment.GOMODCACHE
    $env:GOPATH = $oldEnvironment.GOPATH
    $env:GOTELEMETRY = $oldEnvironment.GOTELEMETRY
    $env:npm_config_cache = $oldEnvironment.npm_config_cache
    $env:FC_RG_PATH = $oldEnvironment.FC_RG_PATH
    $env:WINDSURF_API_KEY = $oldEnvironment.WINDSURF_API_KEY
}
