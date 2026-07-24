# Repository guidance

- Work only on `main`.
- Preserve UTF-8; stdout is reserved exclusively for MCP JSON-RPC in `serve`.
- Keep MCP tools read-only. Index creation and updates are explicit CLI commands.
- Never commit user configurations, source dumps, indexes, caches, binaries, logs,
  credentials, or machine-specific paths.
- Keep the Zoekt fork and official MCP SDK pinned in `go.mod`.
- Verify unit tests, an MCP protocol probe, and a real BSL corpus before publishing.
- Commit and push verified changes directly to `main`.

