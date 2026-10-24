import { describe, expect, it, vi } from "vitest";
import { createCloudClient } from "../src/index.js";

const json = (value: unknown, status = 200) => new Response(JSON.stringify(value), { status, headers: { "Content-Type": "application/json" } });
describe("Cloud pull request operations", () => {
  it("uses the shipped batch review, preference and harness contracts", async () => {
    const fetch = vi.fn<typeof globalThis.fetch>(async () => json({ reviews: [], runs: [] }, 201));
    const client = createCloudClient({ baseUrl: "https://cloud.test", getAccessToken: () => "secret", fetch });
    const signal = new AbortController().signal;
    await client.triggerSessionReviews("org/1", "session?", { signal });
    await client.cancelSessionReviews("org/1", "session?", { signal });
    await client.updateSessionPreferences("org/1", "session?", { reviewerHarness: "codex", autoReviewEnabled: false }, { signal });
    await client.inspectSessionReviewerHarnesses("org/1", "session?", { signal });
    await client.installSessionReviewerHarness("org/1", "session?", "claude-code", { signal });
    expect(fetch.mock.calls.map(([url, init]) => [url, init?.method, init?.body, init?.signal])).toEqual([
      ["https://cloud.test/api/cloud/v1/orgs/org%2F1/sessions/session%3F/reviews/trigger", "POST", undefined, signal],
      ["https://cloud.test/api/cloud/v1/orgs/org%2F1/sessions/session%3F/reviews/cancel", "POST", undefined, signal],
      ["https://cloud.test/api/cloud/v1/orgs/org%2F1/sessions/session%3F/preferences", "PATCH", '{"reviewerHarness":"codex","autoReviewEnabled":false}', signal],
      ["https://cloud.test/api/cloud/v1/orgs/org%2F1/sessions/session%3F/reviewer-harnesses", "GET", undefined, signal],
      ["https://cloud.test/api/cloud/v1/orgs/org%2F1/sessions/session%3F/reviewer-harnesses/claude-code/install", "POST", undefined, signal],
    ]);
  });
  it("fences a squash merge to the displayed URL and head", async () => {
    const fetch = vi.fn<typeof globalThis.fetch>(async () => json({ status: "merge_accepted" }, 202));
    const client = createCloudClient({ baseUrl: "https://cloud.test", getAccessToken: () => "secret", fetch });
    await expect(client.mergePullRequest("org/1", "session?", 7, { prUrl: "https://github.com/a/b/pull/7", expectedHeadSha: "abc" })).resolves.toEqual({ status: "merge_accepted" });
    expect(fetch.mock.calls[0]?.[0]).toBe("https://cloud.test/api/cloud/v1/orgs/org%2F1/sessions/session%3F/pull-requests/7/merge");
    expect(fetch.mock.calls[0]?.[1]?.body).toBe('{"prUrl":"https://github.com/a/b/pull/7","expectedHeadSha":"abc"}');
  });
  it("targets the same reviewer terminal in the ticket and socket", async () => {
    const fetch = vi.fn<typeof globalThis.fetch>(async () => json({ ticket: "ticket" }, 201));
    const client = createCloudClient({ baseUrl: "https://cloud.test", getAccessToken: () => "secret", fetch });
    await client.createTerminalTicket("org", "session", "agent", { terminalId: "review/1" });
    expect(fetch.mock.calls[0]?.[1]?.body).toBe('{"kind":"agent","terminalId":"review/1"}');
    const socket = new URL(client.terminalUrl("ticket", { kind: "agent", terminalId: "review/1" }));
    expect(socket.searchParams.get("terminalId")).toBe("review/1");
  });
  it("acquires fresh browser authorization without placing it in a URL", async () => {
    let token = "one";
    const fetch = vi.fn<typeof globalThis.fetch>(async () => json({ running: false }));
    const client = createCloudClient({ baseUrl: "https://cloud.test", getAccessToken: () => token, fetch });
    await expect(client.getAgentBrowserStats("org/1", "session?")).resolves.toEqual({ running: false });
    token = "two";
    await expect(client.getAgentBrowserSocketRequest("org/1", "session?")).resolves.toEqual({ url: "wss://cloud.test/api/cloud/v1/orgs/org%2F1/sessions/session%3F/agent-browser/ws/cdp", headers: { Authorization: "Bearer two" } });
    const controller = new AbortController(); controller.abort();
    await expect(client.getAgentBrowserSocketRequest("org", "session", { signal: controller.signal })).rejects.toMatchObject({ name: "AbortError" });
    expect(fetch.mock.calls[0]?.[0]).toBe("https://cloud.test/api/cloud/v1/orgs/org%2F1/sessions/session%3F/agent-browser/stats");
  });
  it("constructs native socket URLs without mutating React Native's getter-only protocol", async () => {
    const OriginalURL = globalThis.URL;
    class NativeURL extends OriginalURL { override get protocol() { return super.protocol; } }
    vi.stubGlobal("URL", NativeURL);
    try {
      const client = createCloudClient({ baseUrl: "https://cloud.test", getAccessToken: () => "secret" });
      expect((await client.getAgentBrowserSocketRequest("org", "session")).url).toBe("wss://cloud.test/api/cloud/v1/orgs/org/sessions/session/agent-browser/ws/cdp");
      expect(client.terminalUrl("ticket", { terminalId: "reviewer" })).toContain("wss://cloud.test/api/cloud/v1/terminal?");
    } finally { vi.unstubAllGlobals(); }
  });
});
