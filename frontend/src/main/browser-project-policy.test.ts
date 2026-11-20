import { describe, expect, it, vi } from "vitest";
import { createProjectBrowserTemporaryLookup } from "./browser-project-policy";

function jsonResponse(body: unknown, status = 200): Response {
	return new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });
}

describe("createProjectBrowserTemporaryLookup", () => {
	it("reads the session's project config from the local daemon", async () => {
		const fetchMock = vi.fn(async (url: string) =>
			url.endsWith("/api/v1/sessions/ao-7")
				? jsonResponse({ session: { id: "ao-7", projectId: "my proj" } })
				: jsonResponse({ status: "ok", project: { id: "my proj", config: { browserTemporary: true } } }),
		);
		const lookup = createProjectBrowserTemporaryLookup({ daemonPort: () => 4321, fetch: fetchMock as unknown as typeof fetch });

		expect(await lookup("ao-7")).toBe(true);
		expect(fetchMock.mock.calls.map(([url]) => url)).toEqual([
			"http://127.0.0.1:4321/api/v1/sessions/ao-7",
			"http://127.0.0.1:4321/api/v1/projects/my%20proj",
		]);
	});

	it("is false when the project does not opt in, the session is unknown, or the daemon is not ready", async () => {
		const optedOut = createProjectBrowserTemporaryLookup({
			daemonPort: () => 4321,
			fetch: (async (url: string) =>
				url.includes("/sessions/")
					? jsonResponse({ session: { projectId: "p" } })
					: jsonResponse({ status: "ok", project: { config: {} } })) as unknown as typeof fetch,
		});
		expect(await optedOut("s")).toBe(false);

		const missing = createProjectBrowserTemporaryLookup({
			daemonPort: () => 4321,
			fetch: (async () => jsonResponse({ error: { code: "NOT_FOUND" } }, 404)) as unknown as typeof fetch,
		});
		expect(await missing("s")).toBe(false);

		const fetchMock = vi.fn();
		const notReady = createProjectBrowserTemporaryLookup({ daemonPort: () => undefined, readyPollMs: 1, readyTimeoutMs: 10, fetch: fetchMock as unknown as typeof fetch });
		expect(await notReady("s")).toBe(false);
		expect(fetchMock).not.toHaveBeenCalled();
	});

	it("waits briefly for a daemon that is still starting instead of failing open", async () => {
		const ports = [undefined, undefined, 4321];
		const lookup = createProjectBrowserTemporaryLookup({
			daemonPort: () => (ports.length > 1 ? ports.shift() : ports[0]),
			readyPollMs: 1,
			readyTimeoutMs: 1_000,
			fetch: (async (url: string) =>
				url.includes("/sessions/")
					? jsonResponse({ session: { projectId: "p" } })
					: jsonResponse({ status: "ok", project: { config: { browserTemporary: true } } })) as unknown as typeof fetch,
		});
		expect(await lookup("s")).toBe(true);
	});

	it("gives up with false when the daemon never becomes ready", async () => {
		const fetchMock = vi.fn();
		const lookup = createProjectBrowserTemporaryLookup({
			daemonPort: () => undefined,
			readyPollMs: 1,
			readyTimeoutMs: 20,
			fetch: fetchMock as unknown as typeof fetch,
		});
		expect(await lookup("s")).toBe(false);
		expect(fetchMock).not.toHaveBeenCalled();
	});
});
