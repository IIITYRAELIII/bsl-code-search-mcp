import { spawn } from "node:child_process";
import { createInterface } from "node:readline";

function argument(name) {
  const index = process.argv.indexOf(name);
  if (index < 0 || index + 1 >= process.argv.length) {
    throw new Error(`Missing ${name}`);
  }
  return process.argv[index + 1];
}

const serverPath = argument("--server");
const indexPath = argument("--index");
const zoektBin = argument("--zoekt-bin");
const started = performance.now();
const child = spawn(serverPath, [
  "serve",
  "--index", indexPath,
  "--zoekt-bin", zoektBin,
], {
  stdio: ["pipe", "pipe", "pipe"],
  windowsHide: true,
});

const pending = new Map();
let stderr = "";
let nextId = 1;

createInterface({ input: child.stdout, crlfDelay: Infinity }).on("line", (line) => {
  if (!line.trim()) return;
  let message;
  try {
    message = JSON.parse(line);
  } catch {
    fail(`stdout was polluted by non-JSON data: ${line}`);
    return;
  }
  if (message.id !== undefined && pending.has(message.id)) {
    const item = pending.get(message.id);
    pending.delete(message.id);
    if (message.error) item.reject(new Error(JSON.stringify(message.error)));
    else item.resolve(message.result);
  }
});

child.stderr.setEncoding("utf8");
child.stderr.on("data", (chunk) => {
  stderr += chunk;
  if (process.env.BSL_CODE_SEARCH_PROBE_DEBUG === "1") {
    process.stderr.write(chunk);
  }
});
child.on("exit", (code, signal) => {
  for (const { reject } of pending.values()) {
    reject(new Error(`Server exited before replying (code=${code}, signal=${signal})`));
  }
  pending.clear();
});

function request(method, params = {}) {
  const id = nextId++;
  return new Promise((resolve, reject) => {
    const timer = setTimeout(() => {
      pending.delete(id);
      reject(new Error(`Timeout waiting for ${method}`));
    }, 30_000);
    pending.set(id, {
      resolve(value) {
        clearTimeout(timer);
        resolve(value);
      },
      reject(error) {
        clearTimeout(timer);
        reject(error);
      },
    });
    child.stdin.write(`${JSON.stringify({ jsonrpc: "2.0", id, method, params })}\n`);
  });
}

function notify(method, params = {}) {
  child.stdin.write(`${JSON.stringify({ jsonrpc: "2.0", method, params })}\n`);
}

function assert(condition, message) {
  if (!condition) throw new Error(message);
}

function structured(result) {
  assert(result && result.structuredContent, "Missing structuredContent");
  return result.structuredContent;
}

let failed = false;
function fail(message) {
  if (failed) return;
  failed = true;
  console.error(`${message}\n${stderr}`);
  child.kill();
  child.stdin.destroy();
  child.stdout.destroy();
  child.stderr.destroy();
  process.exitCode = 1;
}

try {
  const initialized = await request("initialize", {
    protocolVersion: "2025-06-18",
    capabilities: {},
    clientInfo: { name: "bsl-code-search-mcp-probe", version: "1.0.0" },
  });
  assert(initialized.serverInfo.name === "bsl-code-search-mcp", "Unexpected server name");
  notify("notifications/initialized");

  const listed = await request("tools/list");
  const tools = listed.tools.map((tool) => tool.name);
  assert(tools.length === 2, `Expected 2 tools, got ${tools.length}`);
  assert(tools.includes("search_code"), "Missing search_code");
  assert(tools.includes("list_corpora"), "Missing list_corpora");
  for (const tool of listed.tools) {
    assert(tool.annotations?.readOnlyHint === true, `${tool.name} is not marked read-only`);
    assert(tool.annotations?.idempotentHint === true, `${tool.name} is not marked idempotent`);
  }

  const corpora = structured(await request("tools/call", {
    name: "list_corpora",
    arguments: {},
  }));
  if (process.env.BSL_CODE_SEARCH_PROBE_DEBUG === "1") {
    console.error(JSON.stringify(corpora));
  }
  assert(corpora.corpora.some((item) => item.name === "probe-config"), "Primary corpus is missing");
  assert(corpora.corpora.some((item) => item.name === "other-config"), "Second corpus is missing");
  assert(corpora.total.repositories === 2, "Expected two independently selected corpora");

  const searchStarted = performance.now();
  const found = structured(await request("tools/call", {
    name: "search_code",
    arguments: {
      query: "repo:^probe-config$ case:yes ДинамическийСписок",
      maxFiles: 10,
      contextLines: 1,
    },
  }));
  const searchMillis = performance.now() - searchStarted;
  assert(found.fileCount === 1, `Expected one result file, got ${found.fileCount}`);
  assert(found.files[0].path === "ДинамическийСписок.bsl", "Result path is not corpus-relative");
  assert(found.files[0].matches[0].lineNumber > 0, "Line number is missing");

  const excluded = structured(await request("tools/call", {
    name: "search_code",
    arguments: { query: "МаркерИсключенногоФайла" },
  }));
  assert(excluded.fileCount === 0, "Excluded txt file was indexed");

  console.log(JSON.stringify({
    ok: true,
    tools,
    corpora: corpora.corpora.map((item) => item.name),
    sourceFiles: corpora.total.sourceFiles,
    readyMillis: Math.round(performance.now() - started),
    searchMillis: Number(searchMillis.toFixed(1)),
  }));
} catch (error) {
  failed = true;
  console.error(`${error.message}\n${stderr}`);
} finally {
  child.stdin.end();
  const exited = child.exitCode !== null || await Promise.race([
    new Promise((resolve) => child.once("exit", () => resolve(true))),
    new Promise((resolve) => setTimeout(() => resolve(false), 5_000)),
  ]);
  if (!exited) {
    child.kill();
    fail("Server did not exit after stdin closed");
  }
}
