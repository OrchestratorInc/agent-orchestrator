import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { CloudCpClient } from "./cloud-cp";
import { CloudCpError } from "./cloud-cp/errors";
import {
	CLOUD_CHAT_SEARCH_BACKOFF_MS,
	CLOUD_CHAT_SEARCH_DEBOUNCE_MS,
	COMMAND_PALETTE_FILE_SEARCH_LIMIT,
	commandPaletteCloudChatSearchQueryOptions,
	commandPaletteContentSearchQueryOptions,
	commandPaletteFileSearchAvailable,
	commandPaletteFileSearchQueryOptions,
	markSearchMatches,
} from "./command-palette-file-search";

const { localGet, remoteGet } = vi.hoisted(() => ({ localGet: vi.fn(), remoteGet: vi.fn() }));

vi.mock("./host-clients", () => ({
	clientForSessionHost: (hostId?: string) => ({ GET: hostId ? remoteGet : localGet }),
}));

beforeEach(() => vi.clearAllMocks());
afterEach(() => vi.useRealTimers());

describe("command palette file search", () => {
	it("uses the bounded local or connected-host workspace search", async () => {
		localGet.mockResolvedValue({ data: { results: [{ path: "src/App.tsx", status: "modified", binary: false }], truncated: false } });
		remoteGet.mockResolvedValue({ data: { results: [{ path: "README.md", status: "unmodified", binary: false }], truncated: false } });
		const cloud = { client: {} as CloudCpClient, baseUrl: "", ready: false };
		const local = commandPaletteFileSearchQueryOptions({
			target: { projectId: "p", sessionId: "s" }, query: "App", errorMessage: "failed", cloud,
		});
		const remote = commandPaletteFileSearchQueryOptions({
			target: { projectId: "p", sessionId: "s", hostId: "host-a" }, query: "read", errorMessage: "failed", cloud,
		});

		expect(local.queryKey).toEqual(["session-workspace-search", "s", "App", COMMAND_PALETTE_FILE_SEARCH_LIMIT]);
		expect(remote.queryKey).toEqual(["session-workspace-search", "host-a", "s", "read", COMMAND_PALETTE_FILE_SEARCH_LIMIT]);
		expect((await local.queryFn()).results[0]?.path).toBe("src/App.tsx");
		expect((await remote.queryFn()).results[0]?.path).toBe("README.md");
		expect(localGet).toHaveBeenCalledWith(
			"/api/v1/sessions/{sessionId}/workspace/search",
			expect.objectContaining({ params: { path: { sessionId: "s" }, query: { query: "App", limit: 20 } } }),
		);
		expect(remoteGet).toHaveBeenCalledTimes(1);
	});

	it("uses the Cloud review search without falling back to the daemon", async () => {
		const searchWorkspaceReview = vi.fn().mockResolvedValue({
			results: [{ path: "cloud.ts", status: "added", size: 3, binary: false, fileFingerprint: "v1" }],
			truncated: false,
		});
		const options = commandPaletteFileSearchQueryOptions({
			target: { projectId: "p", sessionId: "s", cloudOrgId: "org-1" },
			query: "cloud",
			errorMessage: "failed",
			cloud: {
				client: { searchWorkspaceReview } as unknown as CloudCpClient,
				baseUrl: "https://cloud.example",
				ready: true,
			},
		});
		const controller = new AbortController();
		expect((await options.queryFn({ signal: controller.signal })).results[0]?.path).toBe("cloud.ts");
		expect(searchWorkspaceReview).toHaveBeenCalledWith(
			"org-1",
			"s",
			{ query: "cloud", limit: 20 },
			{ signal: controller.signal },
		);
		expect(localGet).not.toHaveBeenCalled();
	});

	it("requires Cloud readiness only for Cloud targets", () => {
		expect(commandPaletteFileSearchAvailable({ projectId: "p", sessionId: "s" }, false)).toBe(true);
		expect(commandPaletteFileSearchAvailable({ projectId: "p", sessionId: "s", cloudOrgId: "org" }, false)).toBe(false);
		expect(commandPaletteFileSearchAvailable({ projectId: "p", sessionId: "s", cloudOrgId: "org" }, true)).toBe(true);
	});
});

describe("command palette Cloud content and chat search", () => {
	it("marks every case-insensitive match", () => {
		expect(markSearchMatches("Needle and needle", "needle")).toBe("\u0002Needle\u0003 and \u0002needle\u0003");
		expect(markSearchMatches("nothing", "needle")).toBe("nothing");
	});

	it("searches a Cloud session's lines through its worker, one row per match", async () => {
		const searchWorkspaceReview = vi.fn().mockResolvedValue({
			query: "needle", truncated: false,
			results: [
				{ path: "src/app.ts", status: "modified", size: 1, binary: false, fileFingerprint: "f", matches: [{ line: 3, text: "a needle" }] },
				{ path: "docs/needle.md", status: "unmodified", size: 1, binary: false, fileFingerprint: "g" },
			],
		});
		const options = commandPaletteContentSearchQueryOptions(
			{ projectId: "p", sessionId: "s", cloudOrgId: "org" }, "needle", "failed",
			{ client: { searchWorkspaceReview } as unknown as CloudCpClient, baseUrl: "https://cp" },
		);
		expect(await options.queryFn()).toEqual({ results: [{ path: "src/app.ts", line: 3, text: "a \u0002needle\u0003" }] });
		expect(searchWorkspaceReview).toHaveBeenCalledWith("org", "s", { query: "needle", limit: COMMAND_PALETTE_FILE_SEARCH_LIMIT, mode: "content" }, expect.anything());
		expect(localGet).not.toHaveBeenCalled();
	});

	it("waits for typing to pause, and asks nothing for a query replaced while it waits", async () => {
		vi.useFakeTimers();
		const searchChats = vi.fn().mockResolvedValue({ results: [] });
		const cloud = { client: { searchChats } as unknown as CloudCpClient, baseUrl: "https://cp-debounce" };
		const replaced = new AbortController();
		const first = commandPaletteCloudChatSearchQueryOptions(cloud, ["org-a"], "lo").queryFn({ signal: replaced.signal });
		const settled = first.catch((error: unknown) => error);
		await vi.advanceTimersByTimeAsync(CLOUD_CHAT_SEARCH_DEBOUNCE_MS - 50);
		replaced.abort();
		expect(await settled).toBeInstanceOf(Error);
		const second = commandPaletteCloudChatSearchQueryOptions(cloud, ["org-a"], "login").queryFn({ signal: new AbortController().signal });
		await vi.advanceTimersByTimeAsync(CLOUD_CHAT_SEARCH_DEBOUNCE_MS);
		await second;
		expect(searchChats).toHaveBeenCalledTimes(1);
		expect(searchChats).toHaveBeenCalledWith("org-a", { query: "login", limit: 20 }, expect.anything());
	});

	it("rests an organization that answered 429 for as long as it asks, without failing the search", async () => {
		vi.useFakeTimers();
		const searchChats = vi.fn(async (orgId: string) => {
			if (orgId === "org-a") throw new CloudCpError("Too many searches. Try again in a minute.", { status: 429, code: "rate_limited", retryAfterMs: 60_000 });
			return { results: [{ sessionId: "s2", sessionTitle: "Other", projectId: "p2", sequence: 3, role: "assistant", snippet: "login fixed", createdAt: "2026-11-01T00:00:00Z" }] };
		});
		const cloud = { client: { searchChats } as unknown as CloudCpClient, baseUrl: "https://cp-limited" };
		const search = async (query: string) => {
			const pending = commandPaletteCloudChatSearchQueryOptions(cloud, ["org-a", "org-b"], query).queryFn({ signal: new AbortController().signal });
			await vi.advanceTimersByTimeAsync(CLOUD_CHAT_SEARCH_DEBOUNCE_MS);
			return pending;
		};
		expect((await search("login")).results.map((hit) => hit.sessionId)).toEqual(["s2"]);
		expect(searchChats.mock.calls.map(([orgId]) => orgId)).toEqual(["org-a", "org-b"]);
		// Within the minute only the other organization is asked.
		await vi.advanceTimersByTimeAsync(30_000);
		expect((await search("login bug")).results).toHaveLength(1);
		expect(searchChats.mock.calls.map(([orgId]) => orgId)).toEqual(["org-a", "org-b", "org-b"]);
		// Once it has rested, it is asked again.
		await vi.advanceTimersByTimeAsync(30_000);
		await search("login bug 2");
		expect(searchChats.mock.calls.map(([orgId]) => orgId).filter((orgId) => orgId === "org-a")).toHaveLength(2);
	});

	it("rests a minute after a 429 that names no wait", async () => {
		vi.useFakeTimers();
		const searchChats = vi.fn().mockRejectedValue(new CloudCpError("Too many searches.", { status: 429 }));
		const cloud = { client: { searchChats } as unknown as CloudCpClient, baseUrl: "https://cp-limited-default" };
		const search = async (query: string) => {
			const pending = commandPaletteCloudChatSearchQueryOptions(cloud, ["org-a"], query).queryFn({ signal: new AbortController().signal });
			await vi.advanceTimersByTimeAsync(CLOUD_CHAT_SEARCH_DEBOUNCE_MS);
			return pending;
		};
		await expect(search("a")).resolves.toEqual({ results: [] });
		await vi.advanceTimersByTimeAsync(CLOUD_CHAT_SEARCH_BACKOFF_MS - 1_000);
		await search("ab");
		expect(searchChats).toHaveBeenCalledTimes(1);
		await vi.advanceTimersByTimeAsync(1_000);
		await search("abc");
		expect(searchChats).toHaveBeenCalledTimes(2);
	});

	it("searches each Cloud organization's chats and keeps the ones that answer", async () => {
		const searchChats = vi.fn()
			.mockResolvedValueOnce({ results: [{ sessionId: "s1", sessionTitle: "Fix login", projectId: "p1", sequence: 7, role: "user", snippet: "the login bug", createdAt: "2026-11-01T00:00:00Z" }] })
			.mockRejectedValueOnce(new Error("offline"));
		const options = commandPaletteCloudChatSearchQueryOptions({ client: { searchChats } as unknown as CloudCpClient, baseUrl: "https://cp" }, ["org-a", "org-b"], "login");
		expect(await options.queryFn()).toEqual({
			results: [{ messageId: "cloud-event-7", projectId: "p1", sessionId: "s1", text: "the \u0002login\u0003 bug", title: "Fix login" }],
		});
	});
});
