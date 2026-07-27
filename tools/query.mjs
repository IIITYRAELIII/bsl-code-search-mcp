import { spawn } from "node:child_process";
import { createInterface } from "node:readline";

function argument(name) {
  const index = process.argv.indexOf(name);
  if (index < 0 || index + 1 >= process.argv.length) {
    throw new Error(`Missing ${name}`);
  }
  return process.argv[index + 1];
}

function optionalArgument(name) {
  const index = process.argv.indexOf(name);
  return index >= 0 && index + 1 < process.argv.length
    ? process.argv[index + 1]
    : undefined;
}

const child = spawn(argument("--server"), [
  "serve",
  "--index", argument("--index"),
  "--zoekt-bin", argument("--zoekt-bin"),
], {
  stdio: ["pipe", "pipe", "inherit"],
  windowsHide: true,
});
const query = argument("--query");
const corpus = optionalArgument("--corpus");
const allCorpora = process.argv.includes("--all-corpora");
if (corpus && allCorpora) {
  throw new Error("--corpus and --all-corpora cannot be used together");
}
const pending = new Map();
let nextId = 1;

createInterface({ input: child.stdout, crlfDelay: Infinity }).on("line", (line) => {
  if (!line.trim()) return;
  const message = JSON.parse(line);
  const item = pending.get(message.id);
  if (!item) return;
  pending.delete(message.id);
  if (message.error) item.reject(new Error(JSON.stringify(message.error)));
  else item.resolve(message.result);
});

function request(method, params = {}) {
  const id = nextId++;
  return new Promise((resolve, reject) => {
    const timer = setTimeout(() => reject(new Error(`Timeout waiting for ${method}`)), 30_000);
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

try {
  await request("initialize", {
    protocolVersion: "2025-06-18",
    capabilities: {},
    clientInfo: { name: "bsl-code-search-query", version: "1.0.0" },
  });
  child.stdin.write('{"jsonrpc":"2.0","method":"notifications/initialized","params":{}}\n');
  const started = performance.now();
  const searchArguments = {
    query,
    maxFiles: 10,
    maxMatches: 100,
    contextLines: 1,
  };
  if (corpus) searchArguments.corpus = corpus;
  if (allCorpora) searchArguments.allCorpora = true;
  const result = await request("tools/call", {
    name: "search_code",
    arguments: searchArguments,
  });
  console.log(JSON.stringify({
    elapsedMillis: Number((performance.now() - started).toFixed(1)),
    result: result.structuredContent,
  }));
} finally {
  child.stdin.end();
  await Promise.race([
    new Promise((resolve) => child.once("exit", resolve)),
    new Promise((resolve) => setTimeout(resolve, 5_000)),
  ]);
  if (child.exitCode === null) child.kill();
}
