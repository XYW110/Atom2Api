[CmdletBinding()]
param(
    [Parameter(Mandatory = $true, Position = 0)]
    [string]$Version,

    [switch]$DryRun
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

function Invoke-NativeCapture {
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
        $output = & $Command @Arguments
        if ($LASTEXITCODE -ne 0) {
            throw "$Command exited with code $LASTEXITCODE"
        }
        return ($output | Out-String).Trim()
    }
    finally {
        Pop-Location
    }
}

$versionPattern = '^v?(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)(?:-[0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*)?(?:\+[0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*)?$'
if ($Version -notmatch $versionPattern) {
    throw "Version must be a semantic version such as v1.2.3 or v1.2.3-rc.1"
}

$tag = "v" + $Version.TrimStart('v')
$repositoryRoot = [IO.Path]::GetFullPath((Split-Path -Parent $PSScriptRoot))
$repositoryFrontend = Join-Path $repositoryRoot "frontend"
$tempRoot = Join-Path ([IO.Path]::GetTempPath()) ("atom2api-release-check-" + [guid]::NewGuid().ToString('N'))
$frontendRoot = Join-Path $tempRoot "frontend"
$frontendOutput = Join-Path $tempRoot "dist"

$status = Invoke-NativeCapture -Command "git" -Arguments @("status", "--porcelain") -WorkingDirectory $repositoryRoot
if ($status) {
    throw "The working tree must be clean before creating a release tag"
}

$branch = Invoke-NativeCapture -Command "git" -Arguments @("branch", "--show-current") -WorkingDirectory $repositoryRoot
if (-not $branch) {
    throw "Release tags cannot be created from a detached HEAD"
}

$upstream = Invoke-NativeCapture -Command "git" -Arguments @(
    "rev-parse", "--abbrev-ref", "--symbolic-full-name", "@{upstream}"
) -WorkingDirectory $repositoryRoot
$remote = Invoke-NativeCapture -Command "git" -Arguments @(
    "config", "--get", "branch.$branch.remote"
) -WorkingDirectory $repositoryRoot
if (-not $remote -or $remote -eq ".") {
    throw "The current branch must track a remote branch"
}

Invoke-NativeCommand -Command "git" -Arguments @("fetch", $remote, "--tags") -WorkingDirectory $repositoryRoot
$headCommit = Invoke-NativeCapture -Command "git" -Arguments @("rev-parse", "HEAD") -WorkingDirectory $repositoryRoot
$upstreamCommit = Invoke-NativeCapture -Command "git" -Arguments @("rev-parse", $upstream) -WorkingDirectory $repositoryRoot
if ($headCommit -ne $upstreamCommit) {
    throw "HEAD must match $upstream before creating a release"
}

Push-Location $repositoryRoot
try {
    & git show-ref --verify --quiet "refs/tags/$tag"
    if ($LASTEXITCODE -eq 0) {
        throw "Tag $tag already exists"
    }
    if ($LASTEXITCODE -ne 1) {
        throw "Unable to check whether tag $tag exists"
    }
}
finally {
    Pop-Location
}

try {
    New-Item -ItemType Directory -Path $frontendRoot -Force | Out-Null
    foreach ($file in @("index.html", "package.json", "package-lock.json", "vite.config.ts", "tailwind.config.js", "postcss.config.js", "tsconfig.json")) {
        $sourceFile = Join-Path $repositoryFrontend $file
        if (-not (Test-Path -LiteralPath $sourceFile -PathType Leaf)) {
            throw "Required frontend file is missing: frontend/$file"
        }
        Copy-Item -LiteralPath $sourceFile -Destination $frontendRoot
    }
    Copy-Item -LiteralPath (Join-Path $repositoryFrontend "src") -Destination $frontendRoot -Recurse
    $publicDirectory = Join-Path $repositoryFrontend "public"
    if (Test-Path -LiteralPath $publicDirectory -PathType Container) {
        Copy-Item -LiteralPath $publicDirectory -Destination $frontendRoot -Recurse
    }

    Invoke-NativeCommand -Command "npm" -Arguments @("ci") -WorkingDirectory $frontendRoot
    Invoke-NativeCommand -Command "npm" -Arguments @(
        "run", "build", "--", "--outDir", $frontendOutput, "--emptyOutDir"
    ) -WorkingDirectory $frontendRoot
    Invoke-NativeCommand -Command "go" -Arguments @("test", "-count=1", "./...") -WorkingDirectory $repositoryRoot
    Invoke-NativeCommand -Command "go" -Arguments @("vet", "./...") -WorkingDirectory $repositoryRoot
}
finally {
    if (Test-Path -LiteralPath $tempRoot) {
        Remove-Item -LiteralPath $tempRoot -Recurse -Force
    }
}

$finalStatus = Invoke-NativeCapture -Command "git" -Arguments @("status", "--porcelain") -WorkingDirectory $repositoryRoot
if ($finalStatus) {
    throw "Verification changed tracked files; review the working tree before releasing"
}

if ($DryRun) {
    Write-Host "Release checks passed for $tag; no tag was created because -DryRun was used"
    exit 0
}

Invoke-NativeCommand -Command "git" -Arguments @(
    "tag", "--annotate", $tag, "--message", "Release $tag"
) -WorkingDirectory $repositoryRoot

try {
    Invoke-NativeCommand -Command "git" -Arguments @(
        "push", $remote, "refs/tags/$tag"
    ) -WorkingDirectory $repositoryRoot
}
catch {
    Write-Error "Tag $tag was created locally but could not be pushed. Fix the remote issue and push refs/tags/$tag."
    throw
}

Write-Host "Pushed $tag to $remote. GitHub Actions will build and publish the release."
