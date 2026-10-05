import { describe, expect, it, vi } from "vitest";
import { createCloudClient } from "../src/index.js";

const response = (body: unknown, status = 200) => new Response(JSON.stringify(body), {
  status,
  headers: { "Content-Type": "application/json" },
});

describe("Cloud account preferences", () => {
  it("reads and updates the account's sandbox preference with bearer auth", async () => {
    const fetchMock = vi.fn(async (_input: RequestInfo | URL, _init?: RequestInit) => response({ sandboxProvider: "coder" }));
    const client = createCloudClient({ baseUrl: "https://cloud.example.com", getAccessToken: () => "access-token", fetch: fetchMock as typeof fetch });
    await expect(client.getUserPreferences()).resolves.toEqual({ sandboxProvider: "coder" });
    await client.putUserPreferences({ sandboxProvider: "coder", initializeOnly: true });
    await client.putUserPreferences({ sandboxProvider: "coder" });
    expect(fetchMock.mock.calls[0]?.[0]).toBe("https://cloud.example.com/api/cloud/v1/me/preferences");
    expect(new Headers(fetchMock.mock.calls[0]?.[1]?.headers).get("Authorization")).toBe("Bearer access-token");
    expect(fetchMock.mock.calls[1]?.[1]).toEqual(expect.objectContaining({ method: "PUT", body: '{"sandboxProvider":"coder","initializeOnly":true}' }));
    expect(fetchMock.mock.calls[2]?.[1]).toEqual(expect.objectContaining({ method: "PUT", body: '{"sandboxProvider":"coder"}' }));
  });

  it("keeps preference conflict details in the standard error envelope", async () => {
    const fetchMock = vi.fn(async () => response({ error: "Conflict", code: "preference_conflict", message: "already set", requestId: "req-1" }, 409));
    const client = createCloudClient({ baseUrl: "https://cloud.example.com", getAccessToken: () => "access-token", fetch: fetchMock as typeof fetch });
    await expect(client.putUserPreferences({ sandboxProvider: "coder", initializeOnly: true })).rejects.toMatchObject({ status: 409, code: "preference_conflict", requestId: "req-1" });
  });
});
