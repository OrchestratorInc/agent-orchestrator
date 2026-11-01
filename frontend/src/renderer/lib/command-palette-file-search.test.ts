import { beforeEach, describe, expect, it, vi } from "vitest";
import type { CloudCpClient } from "./cloud-cp";
import {
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
