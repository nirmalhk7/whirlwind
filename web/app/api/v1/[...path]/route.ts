import { NextRequest } from "next/server";
import { randomUUID } from "node:crypto";
export const runtime = "nodejs";
export const dynamic = "force-dynamic";
const MAX_BODY = 1024 * 1024;
const headers = { "cache-control": "no-store, private", "x-content-type-options": "nosniff", "referrer-policy": "no-referrer" };
function error(status: number, code: string, message: string) { return Response.json({ error: { code, message, request_id: randomUUID() } }, { status, headers }); }
async function handle(request: NextRequest) {
  const host = request.headers.get("host") ?? "";
  if (!/^(localhost|127\.0\.0\.1|\[::1\])(?::\d+)?$/.test(host)) return error(403, "local_host_required", "Loopback Host required.");
  const origin = request.headers.get("origin");
  if (origin && origin !== `http://${host}`) return error(403, "origin_rejected", "Origin must match local host.");
  let backend: URL;
  try { backend = new URL(process.env.WHIRLWIND_BACKEND_URL ?? "http://127.0.0.1:8090"); } catch { return error(503, "backend_unavailable", "Invalid backend URL."); }
  if (backend.protocol !== "http:" || !["localhost", "127.0.0.1", "[::1]"].includes(backend.hostname) || backend.username || backend.password) return error(503, "backend_unavailable", "Backend must use loopback HTTP.");
  const forwarded = new Headers();
  for (const name of ["content-type", "idempotency-key", "if-match", "last-event-id"]) { const value = request.headers.get(name); if (value) forwarded.set(name, value); }
  let body: Uint8Array | undefined;
  if (request.method !== "GET" && request.method !== "HEAD") {
    if (Number(request.headers.get("content-length") ?? 0) > MAX_BODY) return error(413, "request_too_large", "Request exceeds 1 MiB.");
    const chunks: Uint8Array[] = []; let size = 0; const reader = request.body?.getReader();
    if (reader) { try { for (;;) { const next = await reader.read(); if (next.done) break; size += next.value.byteLength; if (size > MAX_BODY) { await reader.cancel(); return error(413, "request_too_large", "Request exceeds 1 MiB."); } chunks.push(next.value); } } catch { return error(422, "invalid_input", "Request body unavailable."); } }
    body = Buffer.concat(chunks);
  }
  try {
    const response = await fetch(new URL(request.nextUrl.pathname + request.nextUrl.search, backend), { method: request.method, headers: forwarded, body: body as BodyInit | undefined, cache: "no-store", redirect: "error", signal: request.nextUrl.pathname === "/api/v1/events" ? request.signal : AbortSignal.any([request.signal, AbortSignal.timeout(130_000)]) });
    const resultHeaders = new Headers(headers); for (const name of ["content-type", "etag", "x-accel-buffering"]) { const value = response.headers.get(name); if (value) resultHeaders.set(name, value); }
    return new Response(response.body, { status: response.status, headers: resultHeaders });
  } catch { return error(503, "backend_unavailable", "Whirlwind backend unavailable. Start Go server and retry."); }
}
export const GET = handle; export const POST = handle; export const PATCH = handle;
