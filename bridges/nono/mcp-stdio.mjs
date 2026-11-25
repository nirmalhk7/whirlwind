// The scoped local tool gateway is independent of nono's filtered public proxy.
// It receives only an ephemeral tool capability, never the host broker secret.
if (!process.env.WHIRLWIND_TOOL_PROXY) throw new Error("Scoped tool gateway unavailable");
process.env.HTTP_PROXY = process.env.WHIRLWIND_TOOL_PROXY;
process.env.http_proxy = process.env.WHIRLWIND_TOOL_PROXY;
await import("../openshell/mcp-stdio.mjs");
