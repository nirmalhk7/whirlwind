// The scoped local tool gateway is independent of nono's filtered public proxy.
if (process.argv[2] !== "--direct") {
  if (!process.env.WHIRLWIND_TOOL_PROXY) throw new Error("Scoped tool gateway unavailable");
  process.env.HTTP_PROXY = process.env.WHIRLWIND_TOOL_PROXY;
  process.env.http_proxy = process.env.WHIRLWIND_TOOL_PROXY;
}
await import("../openshell/mcp-stdio.mjs");
