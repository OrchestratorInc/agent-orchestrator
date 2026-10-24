import { describe, expect, it, vi } from "vitest";
import { CloudApiError, createCloudClient } from "../src/index.js";

const login = { id: "login-1", harness: "codex", status: "waiting", expiresAt: "2026-10-23T12:10:00Z", authorizationUrl: "https://auth.openai.com/codex/device", userCode: "ABCD-1234" };
const setup = (result: unknown = { login }, status = 200) => {
  const fetch = vi.fn<typeof globalThis.fetch>(async () => status === 204 ? new Response(null, { status }) : new Response(JSON.stringify(result), { status, headers: { "Content-Type": "application/json" } }));
  return { fetch, client: createCloudClient({ baseUrl: "https://cloud.test", getAccessToken: () => "account-token", fetch }) };
};

describe("personal native agent login", () => {
  it("starts an idempotent personal attempt without putting credentials in its URL", async () => {
    const { client, fetch } = setup();
    await expect(client.startAgentLogin({ orgId: "org-1", harness: "codex" }, "request-1")).resolves.toEqual({ login });
    const [url, init] = fetch.mock.calls[0]!;
    expect(url).toBe("https://cloud.test/api/cloud/v1/me/agent-logins");
    expect(init).toMatchObject({ method: "POST", cache: "no-store", redirect: "error", body: '{"orgId":"org-1","harness":"codex"}' });
    expect(new Headers(init?.headers).get("Authorization")).toBe("Bearer account-token");
    expect(new Headers(init?.headers).get("Idempotency-Key")).toBe("request-1");
  });

  it("fetches capabilities and status without caching one-time codes", async () => {
    const { client, fetch } = setup({ harnesses: ["codex", "claude-code"] });
    await expect(client.getAgentLoginCapabilities()).resolves.toEqual({ harnesses: ["codex", "claude-code"] });
    expect(fetch.mock.calls[0]?.[0]).toBe("https://cloud.test/api/cloud/v1/me/agent-logins/capabilities");
    await client.getAgentLogin("login/1");
    expect(fetch.mock.calls[1]?.[0]).toBe("https://cloud.test/api/cloud/v1/me/agent-logins/login%2F1");
    expect(fetch.mock.calls[1]?.[1]).toMatchObject({ cache: "no-store", redirect: "error" });
  });

  it("sends a Claude return code only in the body and supports cancellation", async () => {
    const { client, fetch } = setup();
    const abort = new AbortController();
    await client.submitAgentLoginCode("login-1", { code: "private-code#state" }, { signal: abort.signal });
    expect(fetch.mock.calls[0]?.[0]).toBe("https://cloud.test/api/cloud/v1/me/agent-logins/login-1/code");
    expect(fetch.mock.calls[0]?.[1]).toMatchObject({ method: "POST", signal: abort.signal, cache: "no-store", redirect: "error", body: '{"code":"private-code#state"}' });
    const cancelled = setup(undefined, 204);
    await expect(cancelled.client.cancelAgentLogin("login-1")).resolves.toBeUndefined();
    expect(cancelled.fetch.mock.calls[0]?.[1]).toMatchObject({ method: "DELETE", cache: "no-store", redirect: "error" });
  });

  it("uploads keys to the personal connection endpoint, including OpenCode", async () => {
    const { client, fetch } = setup({ providerConnection: { provider: "opencode", label: "default", validationState: "valid" } });
    await client.putUserAgentConnection("opencode", { credentialType: "openrouter_api_key", secret: "private-key" });
    expect(fetch.mock.calls[0]?.[0]).toBe("https://cloud.test/api/cloud/v1/me/providers/opencode");
    expect(fetch.mock.calls[0]?.[1]).toMatchObject({ method: "PUT", cache: "no-store", redirect: "error", body: '{"credentialType":"openrouter_api_key","secret":"private-key"}' });
  });

  it("retains typed unavailable errors for older and disabled deployments", async () => {
    const envelope = { code: "agent_login_unavailable", message: "Native login is unavailable.", requestId: "request-1" };
    const { client } = setup(envelope, 503);
    await expect(client.getAgentLoginCapabilities()).rejects.toMatchObject({ status: 503, code: "agent_login_unavailable" });
    await expect(client.startAgentLogin({ orgId: "org-1", harness: "codex" }, "request-1")).rejects.toBeInstanceOf(CloudApiError);
  });

  it("disconnects only the personal agent connection without caching credentials", async () => {
    const { client, fetch } = setup(undefined, 204);
    const controller = new AbortController();
    await expect(client.deleteUserAgentConnection("codex", { signal: controller.signal })).resolves.toBeUndefined();
    expect(fetch.mock.calls).toHaveLength(1);
    expect(fetch.mock.calls[0]?.[0]).toBe("https://cloud.test/api/cloud/v1/me/providers/codex");
    expect(fetch.mock.calls[0]?.[1]).toMatchObject({ method: "DELETE", signal: controller.signal, cache: "no-store", redirect: "error" });
    expect(fetch.mock.calls[0]?.[1]?.body).toBeUndefined();
  });

  it("retains disconnect errors and refuses insecure personal disconnects", async () => {
    const { client } = setup({ code: "forbidden", message: "Not allowed.", requestId: "request-1" }, 403);
    await expect(client.deleteUserAgentConnection("claude-code")).rejects.toMatchObject({ status: 403, code: "forbidden" });
    const fetch = vi.fn<typeof globalThis.fetch>();
    const unsafe = createCloudClient({ baseUrl: "http://cloud.test", getAccessToken: () => "token", fetch });
    await expect(unsafe.deleteUserAgentConnection("opencode")).rejects.toThrow("HTTPS");
    expect(fetch).not.toHaveBeenCalled();
  });

  it("refuses credential transfer over non-loopback HTTP or URL credentials", async () => {
    for (const baseUrl of ["http://cloud.test", "https://user:password@cloud.test"]) {
      const fetch = vi.fn<typeof globalThis.fetch>();
      const client = createCloudClient({ baseUrl, getAccessToken: () => "token", fetch });
      await expect(client.putUserAgentConnection("codex", { credentialType: "api_key", secret: "private-key" })).rejects.toThrow();
      expect(fetch).not.toHaveBeenCalled();
    }
  });
});
