# BSL Code Search MCP

Read-only local code search for 1C/BSL source dumps, backed by
[Zoekt](https://github.com/IIITYRAELIII/zoekt) and exposed through MCP.

This repository does **not** contain ERP, ERP Holding, ZUP, or any other 1C
configuration. Before starting the MCP, every user must choose and index one
or more source dumps available on their own computer.

Download the Windows x64 archive from
[Releases](https://github.com/IIITYRAELIII/bsl-code-search-mcp/releases). Keep
all three executables from the archive in the same directory:

- `bsl-code-search-mcp.exe`;
- `zoekt-index.exe`;
- `zoekt-webserver.exe`.

## 1. Choose a source dump

Use a configuration exported to files by Designer or EDT. The source may live
anywhere on your machine and is never copied into this repository or sent over
the network.

Give each dump a stable local name:

```powershell
.\bsl-code-search-mcp.exe index `
  --name "my-configuration" `
  --source "C:\path\to\configuration\sources"
```

The default indexes only `*.bsl`. To include metadata XML too:

```powershell
.\bsl-code-search-mcp.exe index `
  --name "my-configuration" `
  --source "C:\path\to\configuration\sources" `
  --extensions "bsl,xml"
```

Run the same command again after the dump changes. Add another configuration
with a different `--name`; both will remain searchable in the same local
index. Use `--index PATH` if you do not want the platform-specific user cache
directory.

The index is local generated data. It can be large (roughly twice the indexed
BSL text in the ERP Holding test corpus), must not be committed, and can be
deleted and rebuilt from the original source dump.

## 2. Check attached configurations

```powershell
.\bsl-code-search-mcp.exe status
```

This reads a small local manifest and lists corpus names, selected extensions,
file counts, shard counts, and sizes without starting the search backend or
returning source paths.

## 3. Connect the MCP client

Use the executable with the `serve` command:

```json
{
  "mcpServers": {
    "bsl-code-search-mcp": {
      "command": "C:\\path\\to\\bsl-code-search-mcp.exe",
      "args": ["serve"]
    }
  }
}
```

If `--index` was used while indexing, pass the same argument after `serve` or
set `BSL_CODE_SEARCH_INDEX`.

The server keeps the index open for fast repeated searches. Its MCP tools are
strictly read-only and carry the standard MCP `readOnlyHint` and
`idempotentHint` annotations:

- `search_code` runs bounded Zoekt queries and returns structured file/line
  matches with context;
- `list_corpora` lists attached corpus names and index statistics.

Examples:

```text
ДинамическийСписок
repo:^my-configuration$ ДинамическийСписок
file:\.bsl$ case:yes "ОписаниеТипов"
regex:"ИзменитьРеквизиты\\("
```

Maintainers can run a one-shot protocol query against an existing index:

```powershell
node .\tools\query.mjs `
  --server .\bin\bsl-code-search-mcp.exe `
  --index "C:\path\to\index" `
  --zoekt-bin "C:\path\to\release" `
  --query 'repo:^my-configuration$ ДинамическийСписок'
```

## Build

Requirements:

- Go 1.25 or newer;
- Windows x64 for the currently tested native workflow.

```powershell
go build -o bin\bsl-code-search-mcp.exe .
go test ./...
```

The MCP binary has no compile-time dependency on Zoekt. A release bundle builds
the two backend executables from the pinned fork:

```powershell
.\scripts\build-release.ps1 `
  -ZoektSource "C:\path\to\pinned\zoekt" `
  -OutputDirectory ".\dist\windows-x64" `
  -Version "dev"
```

Dependencies and backend source are pinned:

- official `modelcontextprotocol/go-sdk` 1.6.1;
- Windows-enabled Zoekt fork commit
  `8080dcef6e12eeec0ca03336dbb71a918bc7bdd1`.

## Privacy and scope

`index` reads only the source directory explicitly supplied by the user and
writes only the selected local index directory. It records the local source
path in the private index manifest so the owner can identify the corpus, but
MCP tools never return that path. `serve` performs no source or index writes,
makes no external network calls, and can search only the already indexed local
corpus. Its private backend listens on a random loopback port and is terminated
with the MCP process.

The index contains searchable source text. Protect it like the original
configuration dump and do not publish it unless you have the right to publish
that source.

See [THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md) for dependency licensing.
