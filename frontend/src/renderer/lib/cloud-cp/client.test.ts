import { describe, expect, it, vi } from "vitest";
import { createCloudCpClient } from "./client";

describe("cloud control-plane session lifecycle", () => {
	it("uses the authenticated transport for account provider preferences", async () => {
		const fetchMock = vi.fn(async (_input: RequestInfo | URL, _init?: RequestInit) => new Response(JSON.stringify({ sandboxProvider: "coder" }), { status: 200, headers: { "Content-Type": "application/json" } }));
		const client = createCloudCpClient({ baseUrl: "https://cloud.example.test", getToken: async () => "token", fetchImpl: fetchMock as typeof fetch });
		await expect(client.getUserPreferences()).resolves.toEqual({ sandboxProvider: "coder" });
		await client.putUserPreferences({ sandboxProvider: "coder", initializeOnly: true });
		expect(fetchMock.mock.calls[0]?.[0]).toBe("https://cloud.example.test/api/cloud/v1/me/preferences");
		expect(new Headers(fetchMock.mock.calls[0]?.[1]?.headers).get("Authorization")).toBe("Bearer token");
		expect(fetchMock.mock.calls[1]?.[1]).toEqual(expect.objectContaining({ method: "PUT", body: '{"sandboxProvider":"coder","initializeOnly":true}' }));
	});
	it("preserves a provider validation error from the control plane", async () => {
		const fetchMock = vi.fn(async () => new Response(JSON.stringify({ error: "Unprocessable Entity", code: "provider_unavailable", message: "not available", requestId: "req-2" }), { status: 422, headers: { "Content-Type": "application/json" } }));
		const client = createCloudCpClient({ baseUrl: "https://cloud.example.test", getToken: async () => "token", fetchImpl: fetchMock as typeof fetch });
		await expect(client.putUserPreferences({ sandboxProvider: "coder" })).rejects.toMatchObject({ status: 422, code: "provider_unavailable", requestId: "req-2" });
	});
	it("posts explicit resume intent for one encoded session", async () => {
		const fetchMock = vi.fn(async () =>
			new Response(
				JSON.stringify({
					session: {
						id: "session/1",
						sandboxProvider: "coder",
						desiredState: "running",
						observedState: "stopped",
					},
				}),
				{ status: 202, headers: { "Content-Type": "application/json" } },
			),
		);
		const client = createCloudCpClient({
			baseUrl: "https://cloud.example.test/",
			getToken: async () => "token",
			fetchImpl: fetchMock as typeof fetch,
		});

		const response = await client.resumeSession("org/1", "session/1");

		expect(response.session.desiredState).toBe("running");
		expect(fetchMock).toHaveBeenCalledWith(
			"https://cloud.example.test/api/cloud/v1/orgs/org%2F1/sessions/session%2F1/resume",
			expect.objectContaining({ method: "POST" }),
		);
	});

	it("posts restore intent for one deleted, encoded session", async () => {
		const fetchMock = vi.fn(async () =>
			new Response(
				JSON.stringify({
					session: {
						id: "session/1",
						desiredState: "running",
					},
				}),
				{ status: 202, headers: { "Content-Type": "application/json" } },
			),
		);
		const client = createCloudCpClient({
			baseUrl: "https://cloud.example.test/",
			getToken: async () => "token",
			fetchImpl: fetchMock as typeof fetch,
		});

		const response = await client.restoreSession("org/1", "session/1");

		expect(response.session.desiredState).toBe("running");
		expect(fetchMock).toHaveBeenCalledWith(
			"https://cloud.example.test/api/cloud/v1/orgs/org%2F1/sessions/session%2F1/restore",
			expect.objectContaining({ method: "POST" }),
		);
	});
});
