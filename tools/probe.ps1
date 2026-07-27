[CmdletBinding()]
param(
    [string] $Go = 'go',
    [Parameter(Mandatory)]
    [string] $ZoektBin
)

$ErrorActionPreference = 'Stop'
$repoRoot = [IO.Path]::GetFullPath([IO.Path]::Combine($PSScriptRoot, '..'))
$temporaryBase = [IO.Path]::GetFullPath([IO.Path]::GetTempPath())
$workRoot = [IO.Path]::GetFullPath(
    [IO.Path]::Combine(
        $temporaryBase,
        "bsl-code-search-mcp-probe-$([Guid]::NewGuid().ToString('N'))"
    )
)
if (-not $workRoot.StartsWith(
    $temporaryBase.TrimEnd('\') + '\',
    [StringComparison]::OrdinalIgnoreCase
)) {
    throw "Refusing unexpected temporary path: $workRoot"
}

$corpus = [IO.Path]::Combine($workRoot, 'конфигурация')
$secondCorpus = [IO.Path]::Combine($workRoot, 'другая-конфигурация')
$index = [IO.Path]::Combine($workRoot, 'index')
$bin = [IO.Path]::Combine($workRoot, 'bin')
$server = [IO.Path]::Combine($bin, 'bsl-code-search-mcp.exe')

try {
    [IO.Directory]::CreateDirectory($corpus) | Out-Null
    [IO.Directory]::CreateDirectory($secondCorpus) | Out-Null
    [IO.Directory]::CreateDirectory($index) | Out-Null
    [IO.Directory]::CreateDirectory($bin) | Out-Null
    [IO.File]::WriteAllText(
        [IO.Path]::Combine($corpus, 'ДинамическийСписок.bsl'),
        @'
Процедура СоздатьСписок()
    ТипСписка = Новый ОписаниеТипов("ДинамическийСписок");
КонецПроцедуры
'@,
        [Text.UTF8Encoding]::new($false)
    )
    [IO.File]::WriteAllText(
        [IO.Path]::Combine($corpus, 'НеИндексировать.txt'),
        'МаркерИсключенногоФайла',
        [Text.UTF8Encoding]::new($false)
    )
    [IO.File]::WriteAllText(
        [IO.Path]::Combine($secondCorpus, 'ДругойМодуль.bsl'),
        "Процедура ДругойКорпус()`nКонецПроцедуры`n",
        [Text.UTF8Encoding]::new($false)
    )

    Push-Location $repoRoot
    try {
        & $Go build -o $server .
        if ($LASTEXITCODE -ne 0) {
            throw 'Failed to build bsl-code-search-mcp.'
        }
    }
    finally {
        Pop-Location
    }

    & $server index `
        --index $index `
        --zoekt-bin $ZoektBin `
        --name 'probe-config' `
        --source $corpus `
        --extensions bsl `
        --default
    if ($LASTEXITCODE -ne 0) {
        throw 'Failed to index the probe corpus.'
    }
    & $server index `
        --index $index `
        --zoekt-bin $ZoektBin `
        --name 'other-config' `
        --source $secondCorpus `
        --extensions bsl
    if ($LASTEXITCODE -ne 0) {
        throw 'Failed to index the second probe corpus.'
    }

    & node ([IO.Path]::Combine($PSScriptRoot, 'probe.mjs')) `
        --server $server `
        --index $index `
        --zoekt-bin $ZoektBin
    if ($LASTEXITCODE -ne 0) {
        throw 'MCP protocol probe failed.'
    }

    $startInfo = [Diagnostics.ProcessStartInfo]::new()
    $startInfo.FileName = $server
    $startInfo.UseShellExecute = $false
    $startInfo.CreateNoWindow = $true
    $startInfo.RedirectStandardInput = $true
    $startInfo.RedirectStandardOutput = $true
    $startInfo.RedirectStandardError = $true
    foreach ($argument in @(
        'serve',
        '--index', $index,
        '--zoekt-bin', $ZoektBin
    )) {
        $startInfo.ArgumentList.Add($argument)
    }
    $abrupt = [Diagnostics.Process]::new()
    $abrupt.StartInfo = $startInfo
    try {
        if (-not $abrupt.Start()) {
            throw 'Failed to start the abrupt-lifetime probe.'
        }
        $stderrDrain = $abrupt.StandardError.ReadToEndAsync()
        $abrupt.StandardInput.WriteLine(
            '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"lifetime-probe","version":"1.0.0"}}}'
        )
        $response = $abrupt.StandardOutput.ReadLine()
        if ([string]::IsNullOrWhiteSpace($response)) {
            throw 'The abrupt-lifetime probe did not initialize.'
        }
        $backendProcess = Get-CimInstance Win32_Process |
            Where-Object {
                $_.Name -eq 'zoekt-webserver.exe' -and
                $_.CommandLine -like "*$index*"
            } |
            Select-Object -First 1
        if (-not $backendProcess -or $backendProcess.Name -ne 'zoekt-webserver.exe') {
            throw 'Could not identify the MCP-owned Zoekt backend process.'
        }
        $backendId = [int] $backendProcess.ProcessId
        $abrupt.Kill()
        $abrupt.WaitForExit()
        $stderrDrain.GetAwaiter().GetResult() | Out-Null
        $deadline = [DateTimeOffset]::UtcNow.AddSeconds(5)
        while (
            (Get-Process -Id $backendId -ErrorAction SilentlyContinue) -and
            [DateTimeOffset]::UtcNow -lt $deadline
        ) {
            Start-Sleep -Milliseconds 50
        }
        if (Get-Process -Id $backendId -ErrorAction SilentlyContinue) {
            throw "Zoekt backend $backendId survived abrupt MCP termination."
        }
    }
    finally {
        if (-not $abrupt.HasExited) {
            $abrupt.Kill()
            $abrupt.WaitForExit()
        }
        $abrupt.Dispose()
    }
    [pscustomobject]@{
        BackendLifetime = 'ok'
        TerminatedBackendPid = $backendId
    } | ConvertTo-Json -Compress
}
finally {
    if ([IO.Directory]::Exists($workRoot)) {
        $cleanupDeadline = [DateTimeOffset]::UtcNow.AddSeconds(5)
        do {
            try {
                Remove-Item -LiteralPath $workRoot -Recurse -Force
            }
            catch {
                if ([DateTimeOffset]::UtcNow -ge $cleanupDeadline) {
                    throw
                }
                Start-Sleep -Milliseconds 100
            }
        } while ([IO.Directory]::Exists($workRoot))
    }
}
