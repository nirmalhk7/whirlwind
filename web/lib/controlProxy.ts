import { NextRequest } from "next/server";
import { randomUUID } from "node:crypto";

const MAX_BODY = 1024 * 1024;
const headers = { "cache-control": "no-store, private", "x-content-type-options": "nosniff", "referrer-policy": "no-referrer" };

export function proxyError(status: number, code: string, message: string) {
  return Response.json({ error: { code, message, request_id: randomUUID() } }, { status, headers });
}

export function validateLocalRequest(request: NextRequest): Response | undefined {
  const host = request.headers.get("host") ?? "";
  if (!/^(localhost|127\.0\.0\.1|\[::1\])(?::\d+)?$/.test(host)) return proxyError(403, "local_host_required", "Loopback Host required.");
  const origin = request.headers.get("origin");
  if (origin && origin !== `http://${host}`) return proxyError(403, "origin_rejected", "Origin must match local host.");
}

export function backendURL(): URL | Response {
  let backend: URL;
  try { backend = new URL(process.env.WHIRLWIND_BACKEND_URL ?? "http://127.0.0.1:9877"); }
  catch { return proxyError(503, "backend_unavailable", "Invalid backend URL."); }
  const localHTTP = backend.protocol === "http:" && ["localhost", "127.0.0.1", "[::1]"].includes(backend.hostname);
  if (!localHTTP || backend.username || backend.password) return proxyError(503, "backend_unavailable", "Backend must use loopback HTTP.");
  return backend;
}

export async function requestBody(request: NextRequest): Promise<Uint8Array | Response | undefined> {
  if (request.method === "GET" || request.method === "HEAD") return;
  if (Number(request.headers.get("content-length") ?? 0) > MAX_BODY) return bodyTooLarge();
  try { return await readBody(request.body?.getReader()); }
  catch { return proxyError(422, "invalid_input", "Request body unavailable."); }
}

function bodyTooLarge() {
  return proxyError(413, "request_too_large", "Request exceeds 1 MiB.");
}

async function readBody(reader?: ReadableStreamDefaultReader<Uint8Array>): Promise<Uint8Array | Response> {
  const chunks: Uint8Array[] = [];
  let size = 0;
  if (reader) {
    for (;;) {
      const next = await reader.read();
      if (next.done) break;
      size += next.value.byteLength;
      if (size > MAX_BODY) {
        await reader.cancel();
        return bodyTooLarge();
      }
      chunks.push(next.value);
    }
  }
  return Buffer.concat(chunks);
}

function copyHeaders(source: Headers, names: string[], initial?: HeadersInit): Headers {
  const result = new Headers(initial);
  for (const name of names) {
    const value = source.get(name);
    if (value) result.set(name, value);
  }
  return result;
}

export async function forwardRequest(request: NextRequest, backend: URL, body?: Uint8Array) {
  try {
    const response = await fetch(new URL(request.nextUrl.pathname + request.nextUrl.search, backend), {
      method: request.method,
      headers: copyHeaders(request.headers, ["content-type", "idempotency-key", "if-match", "last-event-id"]),
      body: body as BodyInit | undefined, cache: "no-store", redirect: "error",
      signal: request.nextUrl.pathname === "/api/v1/events" ? request.signal : AbortSignal.any([request.signal, AbortSignal.timeout(130_000)]),
    });
    const resultHeaders = copyHeaders(response.headers, ["content-type", "etag", "x-accel-buffering", "x-evidence-truncated"], headers);
    return new Response(response.body, { status: response.status, headers: resultHeaders });
  } catch { return proxyError(503, "backend_unavailable", "Whirlwind backend unavailable. Start Go server and retry."); }
}
