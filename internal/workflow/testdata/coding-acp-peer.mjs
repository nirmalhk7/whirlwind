import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import { appendFileSync } from "node:fs";
import { createInterface } from "node:readline";
import { join } from "node:path";

const [scenario, project] = process.argv.slice(2);
const input = createInterface({ input: process.stdin });
const sessionId = "coding-fixture";
const replies = new Map();
let nextCall = 100;
let cwd;
let servers;

function send(message) {
  process.stdout.write(JSON.stringify({ jsonrpc: "2.0", ...message }) + "\n");
}

function call(method, params) {
  const id = nextCall++;
  return new Promise((resolve, reject) => {
    replies.set(id, { resolve, reject });
    send({ id, method, params: { sessionId, ...params } });
  });
}

function check(phase) {
  const result = mcpTool("run_check", { check_id: "regression" });
  assert.equal(result.isError, false, result.content[0].text);
  assert.equal(JSON.parse(result.content[0].text).phase, phase);
}

function mcpTool(name, args) {
  const server = servers.find((server) => server.name === "whirlwind_checks");
  assert.ok(server, "Expected the scoped Whirlwind tool broker");
  const request = {
    jsonrpc: "2.0",
    id: 1,
    method: "tools/call",
    params: {
      name,
      arguments: args,
    },
  };
  const output = spawnSync(server.command, server.args, {
    encoding: "utf8",
    input: JSON.stringify(request) + "\n",
    timeout: 30_000,
  });
  assert.equal(output.status, 0, output.stderr);
  return JSON.parse(output.stdout).result;
}

async function code() {
  const outside = [
    join(cwd, ".git"),
    join(cwd, ".whirlwind.yaml"),
    join(cwd, "result.txt"),
    join(cwd, "..", "review-preparation", "result.txt"),
    join(project, "result.txt"),
    join(project, ".git", "config"),
  ];
  for (const file of outside) {
    assert.throws(() => appendFileSync(file, "unexpected write"), { code: /EPERM|EACCES/ });
  }
  await assert.rejects(
    call("fs/write_text_file", { path: "result.txt", content: "unexpected write" }),
  );
  await call("fs/write_text_file", {
    path: "src/test_result.sh",
    content: 'test "$(sh src/result.sh)" = verified\n',
  });
  check("red");
  const command = "printf '%s\\n' 'printf verified' > result.sh && rm -- obsolete.txt";
  const { terminalId } = await call("terminal/create", {
    cwd: "src",
    command: "/bin/sh",
    args: ["-c", command],
  });
  const completed = await call("terminal/wait_for_exit", { terminalId });
  if (completed.exitCode !== 0) {
    throw new Error(
      "terminal failed: " + JSON.stringify(await call("terminal/output", { terminalId })),
    );
  }
  await call("terminal/release", { terminalId });
  check("green");
  return "Recorded red and green using the approved regression check.";
}

async function handle({ id, method, params }) {
  switch (method) {
    case "initialize":
      return { protocolVersion: 1, agentCapabilities: { loadSession: true }, authMethods: [] };
    case "session/new":
    case "session/load":
      ({ cwd, mcpServers: servers } = params);
      return {
        sessionId,
        models: {
          currentModelId: "fixture-model",
          availableModels: [{ modelId: "fixture-model", name: "Fixture model" }],
        },
      };
    case "session/prompt": {
      if (scenario === "transport-failure") process.exit(1);
      const text = await respond(params.prompt[0].text);
      send({
        method: "session/update",
        params: {
          sessionId,
          update: {
            sessionUpdate: "agent_message_chunk",
            content: { type: "text", text },
          },
        },
      });
      return { stopReason: "end_turn" };
    }
    default:
      throw new Error("unexpected ACP request " + method);
  }
}

async function respond(prompt) {
  switch (scenario) {
    case "review":
      if (prompt.startsWith("Prepare a requirement checklist")) {
        return JSON.stringify({
          checklist: [
            {
              requirement_id: "REQ-RESULT",
              inspection: "Compare the verified script result and regression.",
            },
          ],
        });
      }
      return JSON.stringify({
        requirements: [
          {
            id: "REQ-RESULT",
            status: "pass",
            code: ["src/result.sh"],
            evidence: ["Independent regression passed."],
            explanation: "The result and regression match the frozen scope.",
          },
        ],
        findings: [],
        summary: "Verified.",
      });
    case "finalizer": {
      const receipt = mcpTool("finalize_delivery", {});
      assert.equal(receipt.isError, false, receipt.content[0].text);
      return "Committed the exact accepted candidate locally.";
    }
    default:
      return code();
  }
}

input.on("line", (line) => {
  const message = JSON.parse(line);
  if (message.method) {
    if (message.id !== undefined) {
      handle(message).then(
        (result) => send({ id: message.id, result }),
        (error) => send({ id: message.id, error: { code: -32000, message: error.message } }),
      );
    }
  } else if (replies.has(message.id)) {
    const { resolve, reject } = replies.get(message.id);
    replies.delete(message.id);
    if (message.error) reject(new Error(message.error.message));
    else resolve(message.result);
  }
});
