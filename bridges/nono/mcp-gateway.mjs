import http from "node:http";
import { timingSafeEqual } from "node:crypto";
import { isAbsolute } from "node:path";

// This gateway has one Unix-socket destination and no DNS, CONNECT, or public
// forwarding path. The child gets an ephemeral capability; the real host broker
// credential is injected here and never enters the sandbox.
export function createMCPGateway({ token, broker, brokerToken }) {
  if (
    typeof token !== "string" ||
    token.length < 32 ||
    typeof brokerToken !== "string" ||
    brokerToken.length < 32 ||
    !broker ||
    !isAbsolute(broker.socket_path) ||
    broker.host !== "whirlwind-mcp.openshell.internal" ||
    broker.path !== "/mcp" ||
    !/^[A-Za-z0-9._-]{1,128}$/.test(broker.id)
  )
    throw new Error("Invalid scoped MCP gateway configuration");
  const authorization = Buffer.from(
    `Basic ${Buffer.from(`whirlwind:${token}`).toString("base64")}`,
  );
  const denied = (response, status = 403) => {
    response.writeHead(status, { "content-type": "text/plain", connection: "close" });
    response.end("Scoped MCP gateway denied request");
  };
  const server = http.createServer(async (request, response) => {
    const supplied = Buffer.from(request.headers["proxy-authorization"] ?? "");
    if (supplied.length !== authorization.length || !timingSafeEqual(supplied, authorization)) {
      denied(response, 407);
      return;
    }
    let target;
    try {
      target = new URL(request.url);
    } catch {
      denied(response);
      return;
    }
    if (
      request.method !== "POST" ||
      target.href !== `http://${broker.host}${broker.path}` ||
      request.headers["x-whirlwind-broker-id"] !== broker.id
    ) {
      denied(response);
      return;
    }
    try {
      const chunks = [];
      let bytes = 0;
      for await (const chunk of request) {
        bytes += chunk.length;
        if (bytes > 1024 * 1024) {
          denied(response, 413);
          return;
        }
        chunks.push(chunk);
      }
      const body = Buffer.concat(chunks, bytes);
      const upstream = http.request(
        {
          socketPath: broker.socket_path,
          path: broker.path,
          method: "POST",
          headers: {
            "content-type": "application/json",
            "content-length": String(bytes),
            "x-whirlwind-mcp-token": brokerToken,
          },
        },
        (reply) => {
          const chunks = [];
          let bytes = 0;
          reply.on("data", (chunk) => {
            bytes += chunk.length;
            if (bytes > 1024 * 1024) {
              reply.destroy(new Error("MCP response exceeds limit"));
              return;
            }
            chunks.push(chunk);
          });
          reply.once("end", () => {
            response.writeHead(reply.statusCode ?? 502, { "content-type": "application/json" });
            response.end(Buffer.concat(chunks, bytes));
          });
          reply.once("error", () => {
            if (!response.headersSent) denied(response, 502);
            else response.destroy();
          });
        },
      );
      upstream.once("error", () => {
        if (!response.headersSent) denied(response, 502);
        else response.destroy();
      });
      upstream.setTimeout(300000, () =>
        upstream.destroy(new Error("MCP attempt deadline exceeded")),
      );
      response.once("close", () => upstream.destroy());
      upstream.end(body);
    } catch {
      if (!response.headersSent) denied(response, 502);
      else response.destroy();
    }
  });
  server.on("connect", (_request, socket) =>
    socket.end("HTTP/1.1 403 Forbidden\r\nConnection: close\r\nContent-Length: 0\r\n\r\n"),
  );
  return server;
}
