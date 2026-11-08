import { query } from "@anthropic-ai/claude-agent-sdk";
import readline from "node:readline";
import { runProviderFrame } from "./bridge-run.mjs";

function errorFrame(id, error) {
  return { id, error: { code: -32000, message: String(error.message ?? error) } };
}

const pending = new Map();
let sequence = 0;
const send = (frame) => process.stdout.write(JSON.stringify({ jsonrpc: "2.0", ...frame }) + "\n");
const permission = (tool, input, signal, method = "permission/request") => new Promise((resolve, reject) => {
  const id = `permission-${++sequence}`;
  const cleanup = () => { pending.delete(id); signal?.removeEventListener("abort", abort); };
  const abort = () => { cleanup(); reject(new Error("Permission cancelled")); };
  pending.set(id, (result, error) => { cleanup(); if (error && method === "question/request") reject(new Error(error.message)); else resolve(result ?? { allow: false }); });
  const params = {
    "question/request": { questions: input.questions },
    "permission/request": { tool, input, paths: [input.file_path].filter(Boolean) },
  };
  send({ id, method, params: params[method] });
});

let running = false;
readline.createInterface({ input: process.stdin }).on("line", async (line) => {
  let frame;
  try { frame = JSON.parse(line); } catch { process.exitCode = 1; return; }
  if (!frame.method) { pending.get(frame.id)?.(frame.result, frame.error); return; }
  if (frame.method === "models" && !running) {
    running = true;
    let session;
    try {
      session = query({ prompt: emptyPrompt(), options: { cwd: frame.params.cwd, permissionMode: "default", canUseTool: async () => ({ behavior: "deny", message: "Model discovery only" }) } });
      const models = await session.supportedModels();
      const catalog = new Map(models.map(model => {
        const id = model.resolvedModel || model.value;
        return [id, { id, name: model.displayName }];
      }));
      send({ id: frame.id, result: { models: [...catalog.values()] } });
    } catch (error) { send(errorFrame(frame.id, error)); }
    finally { session?.close(); running = false; }
    return;
  }
  if (frame.method !== "run" || running) {
    send({ id: frame.id, error: { code: -32601, message: "Unsupported or concurrent run" } });
    return;
  }
  running = true;
  try { await runProviderFrame(frame, send, permission); }
  catch (error) { send(errorFrame(frame.id, error)); }
  finally { running = false; }
});

async function* emptyPrompt() {}
