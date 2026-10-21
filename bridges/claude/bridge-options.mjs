export function queryOptions({ cwd, model, read_only, session_id, memory }) {
  const optional = [
    ["model", model, model],
    ["disallowedTools", ["Edit", "Write", "Bash", "NotebookEdit"], read_only],
    ["resume", session_id, session_id],
  ];
  return { cwd, permissionMode: "default", ...memoryOptions(memory), ...Object.fromEntries(optional.filter(([, , enabled]) => enabled).map(([key, value]) => [key, value])) };
}

function memoryOptions(memory) {
  if (!memory) return {};
  return { mcpServers: { "whirlwind-memory": { command: memory.command, args: ["memory-mcp"], env: { WHIRLWIND_MEMORY_URL: memory.url, WHIRLWIND_MEMORY_TOKEN: memory.token } } }, allowedTools: ["mcp__whirlwind-memory__search_lessons"] };
}
