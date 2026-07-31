[CmdletBinding()]
param(
    [Parameter(Mandatory = $true, Position = 0)]
    [string]$Version,

    [Parameter(Position = 1)]
    [string]$OutputDirectory = "dist",

    [switch]$SkipFrontendBuild
)

$ErrorActionPreference = "Stop"
Set-StrictMode -Version Latest

function Invoke-NativeCommand {
    param(
        [Parameter(Mandatory = $true)]
        [string]$Command,

        [Parameter(Mandatory = $true)]
        [string[]]$Arguments,

        [Parameter(Mandatory = $true)]
        [string]$WorkingDirectory
    )

    Push-Location $WorkingDirectory
    try {
        & $Command @Arguments
        if ($LASTEXITCODE -ne 0) {
            throw "$Command exited with code $LASTEXITCODE"
        }
    }
    finally {
        Pop-Location
    }
}

$versionPattern = '^v?(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)(?:-[0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*)?(?:\+[0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*)?$'
if ($Version -notmatch $versionPattern) {
    throw "Version must be a semantic version such as v1.2.3 or v1.2.3-rc.1"
}

$releaseVersion = $Version.TrimStart('v')
$repositoryRoot = [IO.Path]::GetFullPath((Split-Path -Parent $PSScriptRoot))
$outputPath = if ([IO.Path]::IsPathRooted($OutputDirectory)) {
    [IO.Path]::GetFullPath($OutputDirectory)
}
else {
    [IO.Path]::GetFullPath((Join-Path $repositoryRoot $OutputDirectory))
}

$repositoryPrefix = $repositoryRoot.TrimEnd([IO.Path]::DirectorySeparatorChar) + [IO.Path]::DirectorySeparatorChar
if (-not $outputPath.StartsWith($repositoryPrefix, [StringComparison]::OrdinalIgnoreCase)) {
    throw "OutputDirectory must be inside the repository"
}
if ($outputPath -eq $repositoryRoot) {
    throw "OutputDirectory cannot be the repository root"
}

$workRoot = Join-Path ([IO.Path]::GetTempPath()) ("atom2api-release-" + [guid]::NewGuid().ToString('N'))
$sourceRoot = Join-Path $workRoot "source"
$frontendOutput = Join-Path $sourceRoot "frontend/dist"
$frontendBuildRoot = Join-Path $workRoot "frontend-build"
$stageRoot = Join-Path $workRoot "stage"

$targets = @(
    @{ OS = "linux"; Arch = "amd64" },
    @{ OS = "linux"; Arch = "arm64" },
    @{ OS = "windows"; Arch = "amd64" },
    @{ OS = "windows"; Arch = "arm64" },
    @{ OS = "darwin"; Arch = "amd64" },
    @{ OS = "darwin"; Arch = "arm64" }
)

$previousGOOS = $env:GOOS
$previousGOARCH = $env:GOARCH
$previousCGOEnabled = $env:CGO_ENABLED

try {
    New-Item -ItemType Directory -Path $frontendOutput -Force | Out-Null
    New-Item -ItemType Directory -Path $stageRoot -Force | Out-Null

    Get-ChildItem -LiteralPath $repositoryRoot -Filter "*.go" -File |
        Copy-Item -Destination $sourceRoot
    Copy-Item -LiteralPath (Join-Path $repositoryRoot "go.mod") -Destination $sourceRoot
    Copy-Item -LiteralPath (Join-Path $repositoryRoot "go.sum") -Destination $sourceRoot

    if ($SkipFrontendBuild) {
        $existingFrontend = Join-Path $repositoryRoot "frontend/dist"
        if (-not (Test-Path -LiteralPath (Join-Path $existingFrontend "index.html") -PathType Leaf)) {
            throw "frontend/dist is missing; run the frontend build or omit -SkipFrontendBuild"
        }
        Copy-Item -Path (Join-Path $existingFrontend "*") -Destination $frontendOutput -Recurse
    }
    else {
        $frontendRoot = Join-Path $repositoryRoot "frontend"
        New-Item -ItemType Directory -Path $frontendBuildRoot -Force | Out-Null
        foreach ($file in @("index.html", "package.json", "package-lock.json", "vite.config.ts", "tailwind.config.js", "postcss.config.js", "tsconfig.json")) {
            $sourceFile = Join-Path $frontendRoot $file
            if (-not (Test-Path -LiteralPath $sourceFile -PathType Leaf)) {
                throw "Required frontend file is missing: frontend/$file"
            }
            Copy-Item -LiteralPath $sourceFile -Destination $frontendBuildRoot
        }
        Copy-Item -LiteralPath (Join-Path $frontendRoot "src") -Destination $frontendBuildRoot -Recurse
        $publicDirectory = Join-Path $frontendRoot "public"
        if (Test-Path -LiteralPath $publicDirectory -PathType Container) {
            Copy-Item -LiteralPath $publicDirectory -Destination $frontendBuildRoot -Recurse
        }

        Invoke-NativeCommand -Command "npm" -Arguments @("ci") -WorkingDirectory $frontendBuildRoot
        Invoke-NativeCommand -Command "npm" -Arguments @(
            "run", "build", "--", "--outDir", $frontendOutput, "--emptyOutDir"
        ) -WorkingDirectory $frontendBuildRoot
    }

    New-Item -ItemType Directory -Path $outputPath -Force | Out-Null

    $env:CGO_ENABLED = "0"
    $archivePaths = [Collections.Generic.List[string]]::new()
    foreach ($target in $targets) {
        $goos = $target.OS
        $goarch = $target.Arch
        $archiveBaseName = "atom2api_${releaseVersion}_${goos}_${goarch}"
        $targetStage = Join-Path $stageRoot $archiveBaseName
        New-Item -ItemType Directory -Path $targetStage -Force | Out-Null

        $binaryName = if ($goos -eq "windows") { "atom2api.exe" } else { "atom2api" }
        $binaryPath = Join-Path $targetStage $binaryName
        $env:GOOS = $goos
        $env:GOARCH = $goarch
        Invoke-NativeCommand -Command "go" -Arguments @(
            "build",
            "-trimpath",
            "-ldflags=-s -w -X main.version=$releaseVersion",
            "-o", $binaryPath,
            "."
        ) -WorkingDirectory $sourceRoot

        foreach ($file in @("README.md", "README.zh-CN.md", "LICENSE", "THIRD_PARTY_NOTICES.md", "config.example.json")) {
            Copy-Item -LiteralPath (Join-Path $repositoryRoot $file) -Destination $targetStage
        }

        if ($goos -eq "windows") {
            $archivePath = Join-Path $outputPath "$archiveBaseName.zip"
            if (Test-Path -LiteralPath $archivePath) {
                Remove-Item -LiteralPath $archivePath -Force
            }
            Compress-Archive -Path (Join-Path $targetStage "*") -DestinationPath $archivePath -CompressionLevel Optimal
        }
        else {
            $archivePath = Join-Path $outputPath "$archiveBaseName.tar.gz"
            Invoke-NativeCommand -Command "tar" -Arguments @(
                "-czf", $archivePath, "-C", $targetStage, "."
            ) -WorkingDirectory $repositoryRoot
        }
        $archivePaths.Add($archivePath)
    }

    $checksumLines = $archivePaths |
        Sort-Object { [IO.Path]::GetFileName($_) } |
        ForEach-Object {
            $hash = (Get-FileHash -LiteralPath $_ -Algorithm SHA256).Hash.ToLowerInvariant()
            "$hash  $([IO.Path]::GetFileName($_))"
        }
    $checksumPath = Join-Path $outputPath "SHA256SUMS"
    [IO.File]::WriteAllLines($checksumPath, $checksumLines, [Text.UTF8Encoding]::new($false))

    Write-Host "Release artifacts written to $outputPath"
}
finally {
    if ($null -eq $previousGOOS) { Remove-Item Env:GOOS -ErrorAction SilentlyContinue } else { $env:GOOS = $previousGOOS }
    if ($null -eq $previousGOARCH) { Remove-Item Env:GOARCH -ErrorAction SilentlyContinue } else { $env:GOARCH = $previousGOARCH }
    if ($null -eq $previousCGOEnabled) { Remove-Item Env:CGO_ENABLED -ErrorAction SilentlyContinue } else { $env:CGO_ENABLED = $previousCGOEnabled }
    if (Test-Path -LiteralPath $workRoot) {
        Remove-Item -LiteralPath $workRoot -Recurse -Force
    }
}
