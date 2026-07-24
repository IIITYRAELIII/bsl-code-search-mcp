[CmdletBinding()]
param(
    [string] $Go = 'go',
    [Parameter(Mandatory)]
    [string] $ZoektSource,
    [Parameter(Mandatory)]
    [string] $OutputDirectory,
    [string] $Version = 'dev'
)

$ErrorActionPreference = 'Stop'
$pinnedZoektCommit = '8080dcef6e12eeec0ca03336dbb71a918bc7bdd1'
$repoRoot = [IO.Path]::GetFullPath([IO.Path]::Combine($PSScriptRoot, '..'))
$zoektRoot = [IO.Path]::GetFullPath($ZoektSource)
$output = [IO.Path]::GetFullPath($OutputDirectory)

$actualCommit = (& git -C $zoektRoot rev-parse HEAD).Trim()
if ($LASTEXITCODE -ne 0 -or $actualCommit -ne $pinnedZoektCommit) {
    throw "Zoekt checkout must be pinned to $pinnedZoektCommit; got $actualCommit"
}

[IO.Directory]::CreateDirectory($output) | Out-Null

Push-Location $repoRoot
try {
    & $Go build `
        -trimpath `
        -ldflags "-s -w -X main.appVersion=$Version" `
        -o ([IO.Path]::Combine($output, 'bsl-code-search-mcp.exe')) `
        .
    if ($LASTEXITCODE -ne 0) {
        throw 'Failed to build bsl-code-search-mcp.'
    }
}
finally {
    Pop-Location
}

Push-Location $zoektRoot
try {
    & $Go build -trimpath `
        -o ([IO.Path]::Combine($output, 'zoekt-index.exe')) `
        ./cmd/zoekt-index
    if ($LASTEXITCODE -ne 0) {
        throw 'Failed to build zoekt-index.'
    }
    & $Go build -trimpath `
        -o ([IO.Path]::Combine($output, 'zoekt-webserver.exe')) `
        ./cmd/zoekt-webserver
    if ($LASTEXITCODE -ne 0) {
        throw 'Failed to build zoekt-webserver.'
    }
}
finally {
    Pop-Location
}

Copy-Item -LiteralPath ([IO.Path]::Combine($repoRoot, 'README.md')) `
    -Destination $output -Force
Copy-Item -LiteralPath ([IO.Path]::Combine($repoRoot, 'LICENSE')) `
    -Destination $output -Force
Copy-Item -LiteralPath ([IO.Path]::Combine($repoRoot, 'THIRD_PARTY_NOTICES.md')) `
    -Destination $output -Force

$licenseRoot = [IO.Path]::Combine($output, 'licenses')
[IO.Directory]::CreateDirectory($licenseRoot) | Out-Null

function Copy-GoModuleLicenses {
    param(
        [Parameter(Mandatory)]
        [string] $SourceRoot,
        [Parameter(Mandatory)]
        [string] $Prefix
    )

    Push-Location $SourceRoot
    try {
        $modules = & $Go list -m `
            -f '{{if .Version}}{{.Path}}|{{.Version}}|{{.Dir}}{{end}}' `
            all
        if ($LASTEXITCODE -ne 0) {
            throw "Failed to enumerate Go modules under $SourceRoot."
        }
    }
    finally {
        Pop-Location
    }

    foreach ($module in $modules) {
        if ([string]::IsNullOrWhiteSpace($module)) {
            continue
        }
        $parts = $module -split '\|', 3
        if ($parts.Count -ne 3 -or -not [IO.Directory]::Exists($parts[2])) {
            continue
        }
        $safeName = "$Prefix-$($parts[0])@$($parts[1])" `
            -replace '[^A-Za-z0-9._@-]', '_'
        Get-ChildItem -LiteralPath $parts[2] -File |
            Where-Object { $_.Name -match '^(LICENSE|COPYING|NOTICE)(\.|$)' } |
            ForEach-Object {
                Copy-Item -LiteralPath $_.FullName `
                    -Destination ([IO.Path]::Combine(
                        $licenseRoot,
                        "$safeName-$($_.Name)"
                    )) `
                    -Force
            }
    }
}

Copy-GoModuleLicenses -SourceRoot $repoRoot -Prefix 'mcp'
Copy-GoModuleLicenses -SourceRoot $zoektRoot -Prefix 'zoekt'
Copy-Item -LiteralPath ([IO.Path]::Combine($zoektRoot, 'LICENSE')) `
    -Destination ([IO.Path]::Combine($licenseRoot, 'zoekt-LICENSE')) `
    -Force

Get-ChildItem -LiteralPath $output -Recurse -File |
    ForEach-Object {
        if ($_.IsReadOnly) {
            $_.IsReadOnly = $false
        }
    }
