// Only SDK status metadata enters the catalog. Configuration, credentials, and
// raw connection errors must never escape into persisted integration evidence.
export function nativeCatalog(statuses, checkMCP, providerID) {
  const tools = new Map();
  let trustedCheck = false;
  const integrations = [];
  for (const server of statuses) {
    if (typeof server.name !== "string" || !server.name) continue;
    if (isTrustedCheckServer(server, checkMCP)) {
      trustedCheck = server.status === "connected" && server.tools?.some(tool => tool.name === "run_check" || tool.name === "mcp__whirlwind_checks__run_check");
      continue;
    }
    integrations.push(catalogIntegration(server, providerID, tools));
  }
  return { integrations, tools, trustedCheck };
}

function isTrustedCheckServer(server, checkMCP) {
  return checkMCP && server.name === "whirlwind_checks" && server.source === "sdk";
}

function catalogIntegration(server, providerID, tools) {
  const ref = `mcp://${server.name}`;
  const prefix = `mcp__${server.name.replace(/[^a-zA-Z0-9_-]/g, "_")}__`;
  const callable = server.status === "connected";
  const catalogTools = (server.tools ?? [])
    .filter(tool => typeof tool.name === "string" && tool.name)
    .map(tool => catalogTool(tool, prefix, ref, callable, tools));
  return { provider: providerID, ref, name: server.name, kind: "mcp", enabled: server.status !== "disabled", callable,
    ...(!callable ? { error: `MCP server ${["failed", "needs-auth", "pending", "disabled"].includes(server.status) ? server.status : "unavailable"}` } : {}), tools: catalogTools };
}

function catalogTool(tool, prefix, ref, callable, tools) {
  const name = tool.name.startsWith(prefix) ? tool.name : prefix + tool.name;
  const readOnly = tool.annotations?.readOnly === true && tool.annotations?.destructive !== true;
  // A colliding full tool name is ambiguous; fail closed for permissions
  // and evidence rather than attributing one server's tool to another.
  if (callable) tools.set(name, tools.has(name) ? null : { ref, readOnly });
  return { name, ...(typeof tool.description === "string" ? { description: tool.description } : {}), read_only: readOnly };
}

export function requireIntegrations(required, integrations, providerID) {
  for (const item of required ?? []) {
    if (item.provider !== providerID || !integrations.some(integration => integration.ref === item.ref && integration.callable)) {
      throw new Error(`Required integration unavailable for Claude: ${item.ref}`);
    }
  }
}

export function recordToolCalls(message, catalog, pendingCalls, completedCalls) {
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
