export async function api<T>(
  path: string,
  method = "GET",
  data?: unknown,
  version?: number,
): Promise<T> {
  const response = await fetch(`/api/v1/${path}`, {
    method,
    cache: "no-store",
    headers:
      method === "GET"
        ? {}
        : {
            "Content-Type": "application/json",
            "Idempotency-Key": crypto.randomUUID(),
            ...(version !== undefined ? { "If-Match": String(version) } : {}),
          },
    body: method === "GET" ? undefined : JSON.stringify(data ?? {}),
  });
  const result = await response.json();
  if (!response.ok)
    throw new Error(
      result.error?.message ?? "Request failed. Refresh and retry.",
    );
  return result;
}
