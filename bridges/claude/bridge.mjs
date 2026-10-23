import { query } from "@anthropic-ai/claude-agent-sdk";
import readline from "node:readline";

function optionalModel(model) {
  return model ? { resolved_model: model } : {};
}

function errorFrame(id, error) {
  return { id, error: { code: -32000, message: String(error.message ?? error) } };
}

function queryOptions({ cwd, model, read_only, session_id }) {
  const optional = [
    ["model", model, model],
    ["disallowedTools", ["Edit", "Write", "Bash", "NotebookEdit"], read_only],
    ["resume", session_id, session_id],
  ];
  return { cwd, permissionMode: "default", ...Object.fromEntries(optional.filter(([, , enabled]) => enabled).map(([key, value]) => [key, value])) };
}

function attemptUsage(message, tokens, cost) {
  const sample = message.usage;
  const count = key => typeof sample[key] === "number" && Number.isFinite(sample[key]) && sample[key] >= 0 ? sample[key] : null;
  return { input_tokens: count("input_tokens"), output_tokens: count("output_tokens"), cached_tokens: count("cache_read_input_tokens"),
    cache_write_tokens: count("cache_creation_input_tokens"), total_tokens: tokens, cost, scope: "attempt",
    source: "claude", raw: sample, completeness: tokens == null ? "partial" : "reported_complete", cached_input_semantics: "separate_from_input" };
}
function nativeTokenTotal(sample) {
  const values = ["input_tokens", "output_tokens", "cache_read_input_tokens", "cache_creation_input_tokens"].map(key => sample[key]);
  return values.every(value => typeof value === "number" && Number.isFinite(value) && value >= 0) ? values.reduce((sum, value) => sum + value, 0) : null;
}

// Only SDK status metadata enters the catalog. Configuration, credentials, and
// raw connection errors must never escape into persisted integration evidence.
function nativeCatalog(statuses, checkMCP, providerID) {
  const tools = new Map();
  let trustedCheck = false;
  const integrations = [];
  for (const server of statuses) {
    if (typeof server.name !== "string" || !server.name) continue;
    if (checkMCP && server.name === "whirlwind_checks" && server.source === "sdk") {
      trustedCheck = server.status === "connected" && server.tools?.some(tool => tool.name === "run_check" || tool.name === "mcp__whirlwind_checks__run_check");
      continue;
    }
    const ref = `mcp://${server.name}`;
    const prefix = `mcp__${server.name.replace(/[^a-zA-Z0-9_-]/g, "_")}__`;
    const callable = server.status === "connected";
    const catalogTools = (server.tools ?? []).filter(tool => typeof tool.name === "string" && tool.name).map(tool => {
      const name = tool.name.startsWith(prefix) ? tool.name : prefix + tool.name;
      const readOnly = tool.annotations?.readOnly === true && tool.annotations?.destructive !== true;
      // A colliding full tool name is ambiguous; fail closed for permissions
      // and evidence rather than attributing one server's tool to another.
      if (callable) tools.set(name, tools.has(name) ? null : { ref, readOnly });
      return { name, ...(typeof tool.description === "string" ? { description: tool.description } : {}), read_only: readOnly };
    });
    integrations.push({ provider: providerID, ref, name: server.name, kind: "mcp", enabled: server.status !== "disabled", callable,
      ...(!callable ? { error: `MCP server ${["failed", "needs-auth", "pending", "disabled"].includes(server.status) ? server.status : "unavailable"}` } : {}), tools: catalogTools });
  }
  return { integrations, tools, trustedCheck };
}

function requireIntegrations(required, integrations, providerID) {
  for (const item of required ?? []) {
    if (item.provider !== providerID || !integrations.some(integration => integration.ref === item.ref && integration.callable)) {
      throw new Error(`Required integration unavailable for Claude: ${item.ref}`);
    }
  }
}

function recordToolCalls(message, catalog, pendingCalls, completedCalls) {
  const content = message.message?.content;
  if (!Array.isArray(content)) return;
  for (const block of content) {
    if (message.type === "assistant" && block.type === "tool_use" && typeof block.id === "string") {
      const tool = catalog.tools.get(block.name);
      if (tool) pendingCalls.set(block.id, { ref: tool.ref, tool: block.name, call_id: block.id, success: true });
    }
    if (message.type === "user" && block.type === "tool_result") {
      const call = pendingCalls.get(block.tool_use_id);
      if (call) completedCalls.push({...call,success:!block.is_error});
      pendingCalls.delete(block.tool_use_id);
    }
  }
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
readline.createInterface({ input: process.stdin }).on("line", async (line) => {
  let frame;
  try { frame = JSON.parse(line); } catch { process.exitCode = 1; return; }
  if (!frame.method) { pending.get(frame.id)?.(frame.result, frame.error); return; }
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
  let session, releasePrompt;
  try {
    const { cwd, prompt, session_id, read_only, model, check_mcp, native_integrations, catalog_only, required_integrations, effort, provider_id } = frame.params;
    const providerID = provider_id || "claude";
    let catalog = { integrations: [], tools: new Map(), trustedCheck: false };
    const options = {
      ...queryOptions({ cwd, model, read_only, session_id }),
      ...(check_mcp ? {mcpServers:{whirlwind_checks:check_mcp}} : {}),
      ...(native_integrations && effort ? { effort } : {}),
      // Ask overrides native allow rules, ensuring the host sees MCP calls.
      ...(native_integrations ? { settings: { permissions: { ask: ["mcp__*"] } } } : {}),
      canUseTool: async (tool, input, { signal }) => {
        if (tool === "AskUserQuestion") { const questions = input.questions.map((q) => ({ id: q.question, text: q.question, options: (q.options ?? []).map((o) => o.label) })); const response = await permission(tool, { questions }, signal, "question/request"); return { behavior: "allow", updatedInput: { questions: input.questions, answers: response.answers } }; }
        const allowCheck = check_mcp && (!native_integrations || catalog.trustedCheck);
        if (allowCheck && tool === "mcp__whirlwind_checks__run_check") return { behavior: "allow", updatedInput: input };
        if (read_only) return ["Read", "Glob", "Grep", ...(allowCheck ? ["mcp__whirlwind_checks__run_check"] : [])].includes(tool) || native_integrations && catalog.tools.get(tool)?.readOnly === true
          ? { behavior: "allow", updatedInput: input }
          : { behavior: "deny", message: "Read-only role" };
        const answer = await permission(tool, input, signal);
        return answer.allow ? { behavior: "allow", updatedInput: input } : { behavior: "deny", message: "Whirlwind permission denied" };
      },
    };
    let text = "", sid = "", resolvedModel = "", cost = null, tokens = null, usage = null, succeeded = false, failure = "Claude did not complete successfully";
    const promptReady = native_integrations ? new Promise(resolve => { releasePrompt = resolve; }) : Promise.resolve(true);
    async function* input() { if (await promptReady) yield { type: "user", message: { role: "user", content: prompt }, parent_tool_use_id: null, session_id: session_id ?? "" }; }
    async function* empty() {}
    session = query({ prompt: catalog_only ? empty() : input(), options });
    if (native_integrations) {
      catalog = nativeCatalog(await session.mcpServerStatus(), check_mcp, providerID);
      requireIntegrations(required_integrations, catalog.integrations, providerID);
      if (catalog_only) {
        send({ id: frame.id, result: { integrations: catalog.integrations } });
        return;
      }
      releasePrompt(true);
    }
    const pendingCalls = new Map(), completedCalls = [];
    for await (const message of session) {
      if (native_integrations) recordToolCalls(message, catalog, pendingCalls, completedCalls);
      sid = message.session_id ?? sid;
      if (message.type === "system" && message.subtype === "init" && typeof message.model === "string") resolvedModel = message.model;
      if (message.type === "assistant") {
        if (["rate_limit", "billing_error"].includes(message.error)) failure = `Claude quota exhausted: ${message.error}`;
        text = message.message.content.filter((content) => content.type === "text").reduce((current, content) => current + content.text, text);
      }
      if (message.type === "result") {
        succeeded = message.subtype === "success" && !message.is_error;
        if (message.errors?.length) failure = message.errors.join("\n");
        if (typeof message.result === "string") text = message.result;
        cost = message.total_cost_usd ?? null;
        tokens = message.usage ? nativeTokenTotal(message.usage) : null;
        if(message.usage) usage = attemptUsage(message, tokens, cost);
      }
      send({ method: "agent/event", params: { type: message.type, session_id: sid, ...optionalModel(resolvedModel), ...(["assistant", "user"].includes(message.type) ? {message:{role:message.message?.role,content:message.message?.content?.filter(content=>!["thinking","redacted_thinking"].includes(content.type))}} : {}), ...(message.type === "result" ? {subtype:message.subtype,stop_reason:message.stop_reason,usage,cost,errors:message.errors} : {}) } });
    }
    if (!succeeded) throw new Error(failure);
    send({ id: frame.id, result: { text, session_id: sid, cost, tokens, usage, ...optionalModel(resolvedModel), ...(native_integrations ? { integrations: catalog.integrations, tool_calls: completedCalls } : {}) } });
  } catch (error) { send(errorFrame(frame.id, error)); }
  finally { releasePrompt?.(false); session?.close(); running = false; }
});
