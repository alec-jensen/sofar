// Typed client for /api/v1. Errors use the shape { error: { code, message } }
// (api/openapi.yaml); anything else becomes a generic message.
export class ApiError extends Error {
  constructor(
    message: string,
    public status: number,
    public code: string,
  ) {
    super(message);
  }
}

export async function api<T = unknown>(path: string, body?: unknown): Promise<T> {
  let r: Response;
  try {
    r = await fetch("/api/v1" + path, {
      method: body === undefined ? "GET" : "POST",
      credentials: "same-origin",
      headers: body === undefined ? undefined : { "Content-Type": "application/json" },
      body: body === undefined ? undefined : JSON.stringify(body),
    });
  } catch {
    throw new ApiError("could not reach sofar.", 0, "network");
  }
  if (!r.ok) {
    const data = await r.json().catch(() => null);
    const e = data?.error;
    throw new ApiError(
      typeof e?.message === "string" ? e.message : "something went wrong.",
      r.status,
      typeof e?.code === "string" ? e.code : "unknown",
    );
  }
  return r.json() as Promise<T>;
}

export type AuthStatus = { authenticated: boolean; setup: boolean; totpEnabled: boolean };
