import { NextRequest } from "next/server";
import { backendURL, forwardRequest, requestBody, validateLocalRequest } from "../../../../lib/controlProxy";
export const runtime = "nodejs";
export const dynamic = "force-dynamic";
async function handle(request: NextRequest) {
  const invalid = validateLocalRequest(request);
  if (invalid) return invalid;
  const backend = backendURL();
  if (backend instanceof Response) return backend;
  const body = await requestBody(request);
  if (body instanceof Response) return body;
  return forwardRequest(request, backend, body);
}
export const GET = handle; export const POST = handle; export const PATCH = handle;

export const DELETE = handle;
