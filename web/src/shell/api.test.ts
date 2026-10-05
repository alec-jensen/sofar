import { afterEach, describe, expect, it, vi } from "vitest";
import { api, ApiError } from "./api";

afterEach(() => vi.unstubAllGlobals());

function stubFetch(status: number, body: unknown) {
  const fn = vi.fn(async () => new Response(JSON.stringify(body), { status }));
  vi.stubGlobal("fetch", fn);
  return fn;
}

describe("api client", () => {
  it("calls /api/v1 with credentials and posts JSON when given a body", async () => {
    const fn = stubFetch(200, { ok: true });
    await api("/auth/login", { username: "a" });
    const [url, init] = fn.mock.calls[0] as unknown as [string, RequestInit];
    expect(url).toBe("/api/v1/auth/login");
    expect(init.method).toBe("POST");
    expect(init.credentials).toBe("same-origin");
    expect(init.body).toBe('{"username":"a"}');
  });

  it("uses GET without a body", async () => {
    const fn = stubFetch(200, { authenticated: false });
    await api("/auth/status");
    expect((fn.mock.calls[0] as unknown as [string, RequestInit])[1].method).toBe("GET");
  });

  it("turns the standard error shape into an ApiError", async () => {
    stubFetch(401, { error: { code: "bad_credentials", message: "Check your details." } });
    const err = await api("/auth/login", {}).catch((e) => e);
    expect(err).toBeInstanceOf(ApiError);
    expect(err).toMatchObject({ status: 401, code: "bad_credentials", message: "Check your details." });
  });

  it("copes with a non-JSON failure and with a network error", async () => {
    vi.stubGlobal("fetch", vi.fn(async () => new Response("<html>", { status: 502 })));
    expect(await api("/x").catch((e) => e)).toMatchObject({ status: 502, code: "unknown" });
    vi.stubGlobal(
      "fetch",
      vi.fn(async () => {
        throw new TypeError("offline");
      }),
    );
    expect(await api("/x").catch((e) => e)).toMatchObject({ status: 0, code: "network" });
  });
});
