import { query } from "@anthropic-ai/claude-agent-sdk";
import readline from "node:readline";

const pending = new Map();
let sequence = 0;
const send = (frame) => process.stdout.write(JSON.stringify({ jsonrpc: "2.0", ...frame }) + "\n");
const permission = (tool, input, signal, method = "permission/request") => new Promise((resolve, reject) => {
  const id = `permission-${++sequence}`;
  const cleanup = () => { pending.delete(id); signal?.removeEventListener("abort", abort); };
  const abort = () => { cleanup(); reject(new Error("Permission cancelled")); };
  pending.set(id, (result, error) => { cleanup(); if (error && method === "question/request") reject(new Error(error.message)); else resolve(result ?? { allow: false }); });
  signal?.addEventListener("abort", abort, { once: true });
  send({ id, method, params: method === "question/request" ? { questions: input.questions } : { tool, input, paths: input.file_path ? [input.file_path] : [] } });
});
let running = false;
readline.createInterface({ input: process.stdin }).on("line", async (line) => {
  let frame;
  try { frame = JSON.parse(line); } catch { process.exitCode = 1; return; }
  if (!frame.method) { pending.get(frame.id)?.(frame.result, frame.error); return; }
  if (frame.method !== "run" || running) { send({ id: frame.id, error: { code: -32601, message: "Unsupported or concurrent run" } }); return; }
  running = true;
  try {
    const { cwd, prompt, session_id, read_only } = frame.params;
    const options = {
      cwd,
      permissionMode: "default",
      ...(read_only ? { disallowedTools: ["Edit", "Write", "Bash", "NotebookEdit"] } : {}),
      ...(session_id ? { resume: session_id } : {}),
      canUseTool: async (tool, input, { signal }) => {
        if (tool === "AskUserQuestion") { const questions = input.questions.map((q) => ({ id: q.question, text: q.question, options: q.options?.map((o) => o.label) ?? [] })); const response = await permission(tool, { questions }, signal, "question/request"); return { behavior: "allow", updatedInput: { questions: input.questions, answers: response.answers } }; }
        if (read_only && !["Read", "Glob", "Grep"].includes(tool)) return { behavior: "deny", message: "Read-only role" };
        if (read_only) return { behavior: "allow", updatedInput: input };
        const answer = await permission(tool, input, signal);
        return answer.allow ? { behavior: "allow", updatedInput: input } : { behavior: "deny", message: "Whirlwind permission denied" };
      },
    };
    let text = "", sid = "", cost = null, tokens = null, succeeded = false;
    async function* input() { yield { type: "user", message: { role: "user", content: prompt }, parent_tool_use_id: null, session_id: session_id ?? "" }; }
    for await (const message of query({ prompt: input(), options })) {
      sid = message.session_id ?? sid;
      if (message.type === "assistant") {
        for (const content of message.message.content) if (content.type === "text") text += content.text;
      }
      if (message.type === "result") {
        succeeded = message.subtype === "success" && !message.is_error;
        if (typeof message.result === "string") text = message.result;
        cost = message.total_cost_usd ?? null;
        tokens = message.usage ? (message.usage.input_tokens ?? 0) + (message.usage.output_tokens ?? 0) : null;
      }
      send({ method: "agent/event", params: { type: message.type, session_id: sid } });
    }
    if (!succeeded) throw new Error("Claude did not complete successfully");
    send({ id: frame.id, result: { text, session_id: sid, cost, tokens } });
  } catch (error) { send({ id: frame.id, error: { code: -32000, message: String(error.message ?? error) } }); }
  finally { running = false; }
});
