import { query } from "@anthropic-ai/claude-agent-sdk";
import readline from "node:readline";
import { queryOptions } from "./bridge-options.mjs";
import { optionalModel, attemptUsage, contextOccupancy } from "./bridge-telemetry.mjs";

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
  signal?.addEventListener("abort", abort, { once: true });
  const params = {
    "question/request": { questions: input.questions },
    "permission/request": { tool, input, paths: [input.file_path].filter(Boolean) },
  };
  send({ id, method, params: params[method] });
});
let running = false;
let activeQuery;
readline.createInterface({ input: process.stdin }).on("line", async (line) => {
  let frame;
  try { frame = JSON.parse(line); } catch { process.exitCode = 1; return; }
  if (!frame.method) { pending.get(frame.id)?.(frame.result, frame.error); return; }
  if (frame.method === "cancel") { await activeQuery?.interrupt(); return; }
  if (frame.method === "models" && !running) {
    running = true;
    let session;
    try {
      async function* empty() {}
      session = query({ prompt: empty(), options: { cwd: frame.params.cwd, permissionMode: "default", canUseTool: async () => ({ behavior: "deny", message: "Model discovery only" }) } });
      const models = await session.supportedModels();
      const catalog = new Map(models.map((model) => {
        const id = model.resolvedModel || model.value;
        return [id, { id, name: model.displayName }];
      }));
      send({ id: frame.id, result: { models: [...catalog.values()] } });
    } catch (error) { send(errorFrame(frame.id, error)); }
    finally { session?.close(); running = false; }
    return;
  }
  if (frame.method !== "run" || running) { send({ id: frame.id, error: { code: -32601, message: "Unsupported or concurrent run" } }); return; }
  running = true;
  try {
    const { cwd, prompt, session_id, read_only, model, memory } = frame.params;
    const options = {
      ...queryOptions({ cwd, model, read_only, session_id, memory }),
      canUseTool: async (tool, input, { signal }) => {
        if (tool === "AskUserQuestion") { const questions = input.questions.map((q) => ({ id: q.question, text: q.question, options: (q.options ?? []).map((o) => o.label) })); const response = await permission(tool, { questions }, signal, "question/request"); return { behavior: "allow", updatedInput: { questions: input.questions, answers: response.answers } }; }
        if (memory && tool === "mcp__whirlwind-memory__search_lessons") return { behavior: "allow", updatedInput: input };
        if (read_only) return ["Read", "Glob", "Grep"].includes(tool)
          ? { behavior: "allow", updatedInput: input }
          : { behavior: "deny", message: "Read-only role" };
        const answer = await permission(tool, input, signal);
        return answer.allow ? { behavior: "allow", updatedInput: input } : { behavior: "deny", message: "Whirlwind permission denied" };
      },
    };
    let text = "", sid = "", resolvedModel = "", cost = null, tokens = null, usage = null, context = null, lastUsage = null, succeeded = false, failure = "Claude did not complete successfully";
    async function* input() { yield { type: "user", message: { role: "user", content: prompt }, parent_tool_use_id: null, session_id: session_id ?? "" }; }
    activeQuery = query({ prompt: input(), options });
    for await (const message of activeQuery) {
      sid = message.session_id ?? sid;
      if (message.type === "system" && message.subtype === "init" && typeof message.model === "string") resolvedModel = message.model;
      if (message.type === "assistant") {
        lastUsage = message.message?.usage ?? lastUsage;
        if (["rate_limit", "billing_error"].includes(message.error)) failure = `Claude quota exhausted: ${message.error}`;
        text = message.message.content.filter((content) => content.type === "text").reduce((current, content) => current + content.text, text);
      }
      if (message.type === "result") {
        succeeded = message.subtype === "success" && !message.is_error;
        if (message.errors?.length) failure = message.errors.join("\n");
        if (typeof message.result === "string") text = message.result;
        cost = message.total_cost_usd ?? null;
        tokens = message.usage ? ["input_tokens", "output_tokens"].map((key) => message.usage[key] ?? 0).reduce((input, output) => input + output) : null;
        if(message.usage) usage = attemptUsage(message, tokens, cost);
        context = contextOccupancy(lastUsage, message.modelUsage, resolvedModel);
      }
      send({ method: "agent/event", params: { type: message.type, session_id: sid, ...optionalModel(resolvedModel), ...(["assistant", "user"].includes(message.type) ? {message:{role:message.message?.role,content:message.message?.content?.filter(content=>!["thinking","redacted_thinking"].includes(content.type))}} : {}), ...(message.type === "result" ? {subtype:message.subtype,stop_reason:message.stop_reason,usage,cost,context,errors:message.errors} : {}) } });
    }
    if (!succeeded) throw new Error(failure);
    send({ id: frame.id, result: { text, session_id: sid, cost, tokens, usage, context, ...optionalModel(resolvedModel) } });
  } catch (error) { send(errorFrame(frame.id, error)); }
  finally { activeQuery?.close(); activeQuery = undefined; running = false; }
});
