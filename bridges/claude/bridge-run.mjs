import { query } from "@anthropic-ai/claude-agent-sdk";
import { nativeCatalog, recordToolCalls, requireIntegrations } from "./native-catalog.mjs";

function optionalModel(model) {
  return model ? { resolved_model: model } : {};
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

function permissionHandler({ check_mcp, native_integrations, read_only }, state, permission) {
  return async (tool, input, { signal }) => {
    if (tool === "AskUserQuestion") {
      const questions = input.questions.map(q => ({ id: q.question, text: q.question, options: (q.options ?? []).map(o => o.label) }));
      const response = await permission(tool, { questions }, signal, "question/request");
      return { behavior: "allow", updatedInput: { questions: input.questions, answers: response.answers } };
    }
    const allowCheck = check_mcp && (!native_integrations || state.catalog.trustedCheck);
    if (allowCheck && tool === "mcp__whirlwind_checks__run_check") return { behavior: "allow", updatedInput: input };
    if (read_only) return readOnlyPermission(tool, input, allowCheck, native_integrations, state.catalog);
    const answer = await permission(tool, input, signal);
    return answer.allow ? { behavior: "allow", updatedInput: input } : { behavior: "deny", message: "Whirlwind permission denied" };
  };
}

function readOnlyPermission(tool, input, allowCheck, nativeIntegrations, catalog) {
  const allowed = ["Read", "Glob", "Grep", ...(allowCheck ? ["mcp__whirlwind_checks__run_check"] : [])].includes(tool);
  if (allowed || (nativeIntegrations && catalog.tools.get(tool)?.readOnly === true)) return { behavior: "allow", updatedInput: input };
  return { behavior: "deny", message: "Read-only role" };
}

export async function runProviderFrame(frame, send, permission) {
  const request = frame.params;
  const providerID = request.provider_id || "claude";
  const state = createRunState();
  const promptReady = request.native_integrations ? new Promise(resolve => { state.releasePrompt = resolve; }) : Promise.resolve(true);
  const options = {
    ...queryOptions({ cwd: request.cwd, model: request.model, read_only: request.read_only, session_id: request.session_id }),
    ...(request.check_mcp ? {mcpServers:{whirlwind_checks:request.check_mcp}} : {}),
    ...(request.native_integrations && request.effort ? { effort: request.effort } : {}),
    ...(request.native_integrations ? { settings: { permissions: { ask: ["mcp__*"] } } } : {}),
    canUseTool: permissionHandler(request, state, permission),
  };
  let session;
  try {
    session = query({ prompt: providerPrompt(request, promptReady), options });
    if (request.native_integrations) {
      state.catalog = nativeCatalog(await session.mcpServerStatus(), request.check_mcp, providerID);
      requireIntegrations(request.required_integrations, state.catalog.integrations, providerID);
      if (request.catalog_only) {
        send({ id: frame.id, result: { integrations: state.catalog.integrations } });
        return;
      }
      state.releasePrompt(true);
    }
    await collectProviderMessages(session, state, request.native_integrations, send);
    if (!state.succeeded) throw new Error(state.failure);
    send({ id: frame.id, result: providerResult(state, request.native_integrations) });
  } finally {
    state.releasePrompt?.(false);
    session?.close();
  }
}

function createRunState() {
  return { catalog: { integrations: [], tools: new Map(), trustedCheck: false }, pendingCalls: new Map(), completedCalls: [],
    text: "", sid: "", resolvedModel: "", cost: null, tokens: null, usage: null, succeeded: false,
    failure: "Claude did not complete successfully", releasePrompt: null };
}

function providerPrompt(request, promptReady) {
  if (request.catalog_only) return emptyPrompt();
  return inputPrompt(request, promptReady);
}

async function* inputPrompt(request, promptReady) {
  if (await promptReady) yield { type: "user", message: { role: "user", content: request.prompt }, parent_tool_use_id: null, session_id: request.session_id ?? "" };
}

async function* emptyPrompt() {}

async function collectProviderMessages(session, state, nativeIntegrations, send) {
  for await (const message of session) {
    if (nativeIntegrations) recordToolCalls(message, state.catalog, state.pendingCalls, state.completedCalls);
    captureProviderMessage(message, state);
    send({ method: "agent/event", params: providerEvent(message, state) });
  }
}

function captureProviderMessage(message, state) {
  state.sid = message.session_id ?? state.sid;
  if (message.type === "system" && message.subtype === "init" && typeof message.model === "string") state.resolvedModel = message.model;
  if (message.type === "assistant") captureAssistantMessage(message, state);
  if (message.type === "result") captureResultMessage(message, state);
}

function captureAssistantMessage(message, state) {
  if (["rate_limit", "billing_error"].includes(message.error)) state.failure = `Claude quota exhausted: ${message.error}`;
  state.text = message.message.content.filter(content => content.type === "text").reduce((current, content) => current + content.text, state.text);
}

function captureResultMessage(message, state) {
  state.succeeded = message.subtype === "success" && !message.is_error;
  if (message.errors?.length) state.failure = message.errors.join("\n");
  if (typeof message.result === "string") state.text = message.result;
  state.cost = message.total_cost_usd ?? null;
  state.tokens = message.usage ? nativeTokenTotal(message.usage) : null;
  if (message.usage) state.usage = attemptUsage(message, state.tokens, state.cost);
}

function providerEvent(message, state) {
  const scrubbed = ["assistant", "user"].includes(message.type)
    ? {message:{role:message.message?.role,content:message.message?.content?.filter(content=>!['thinking','redacted_thinking'].includes(content.type))}}
    : {};
  const result = message.type === "result" ? {subtype:message.subtype,stop_reason:message.stop_reason,usage:state.usage,cost:state.cost,errors:message.errors} : {};
  return { type: message.type, session_id: state.sid, ...optionalModel(state.resolvedModel), ...scrubbed, ...result };
}

function providerResult(state, nativeIntegrations) {
  return { text: state.text, session_id: state.sid, cost: state.cost, tokens: state.tokens, usage: state.usage, ...optionalModel(state.resolvedModel),
    ...(nativeIntegrations ? { integrations: state.catalog.integrations, tool_calls: state.completedCalls } : {}) };
}
