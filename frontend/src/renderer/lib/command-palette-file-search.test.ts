import { beforeEach, describe, expect, it, vi } from "vitest";
import type { CloudCpClient } from "./cloud-cp";
import { commandPaletteFileSearchAvailable, commandPaletteFileSearchQueryOptions } from "./command-palette-file-search";

const { localGet, remoteGet } = vi.hoisted(() => ({ localGet: vi.fn(), remoteGet: vi.fn() }));

vi.mock("./host-clients", () => ({
	clientForSessionHost: (hostId?: string) => ({ GET: hostId ? remoteGet : localGet }),
}));

beforeEach(() => vi.clearAllMocks());


function searchResponse(overrides: Record<string, unknown> = {}) {
	return {
		data: { fileNames: [], fileContent: [], messages: [], files: "ready", ...overrides },
	};
}

describe("command palette search", () => {
	it("searches files and chats on the session's own daemon", async () => {
		localGet.mockResolvedValue(searchResponse({
			fileNames: [{ path: "src/App.tsx", score: 9, exactName: true }],
			fileContent: [{ path: "src/App.tsx", score: 4, decisive: false, snippet: { text: "export function App()", line: 3, highlights: [] } }],
			messages: [{ messageId: "m1", conversationTitle: "Work", score: 3, decisive: false, olderVersion: false, snippet: { text: "App crash", highlights: [] } }],
		}));
		remoteGet.mockResolvedValue(searchResponse({ fileNames: [{ path: "README.md", score: 5, exactName: false }] }));
		const cloud = { client: {} as CloudCpClient, baseUrl: "", ready: false };
		const local = commandPaletteFileSearchQueryOptions({
			target: { projectId: "p", sessionId: "s" }, query: "App", includeMessages: true, errorMessage: "failed", cloud,
		});
		const remote = commandPaletteFileSearchQueryOptions({
			target: { projectId: "p", sessionId: "s", hostId: "host-a" }, query: "read", includeMessages: false, errorMessage: "failed", cloud,
		});

		expect(local.queryKey).toEqual(["palette-search", "", "s", "fileNames,fileContent,messages", "App"]);
		expect(remote.queryKey).toEqual(["palette-search", "host-a", "s", "fileNames,fileContent", "read"]);
		const localResult = await local.queryFn();
		expect(localResult.fileNames?.[0]?.path).toBe("src/App.tsx");
		expect(localResult.fileContent?.[0]?.snippet.line).toBe(3);
		expect(localResult.messages?.[0]?.messageId).toBe("m1");
		expect(localResult.results[0]?.path).toBe("src/App.tsx");
		expect((await remote.queryFn()).fileNames?.[0]?.path).toBe("README.md");
		expect(localGet).toHaveBeenCalledWith(
			"/api/v1/search",
			expect.objectContaining({ params: { query: { q: "App", sessionId: "s", kinds: "fileNames,fileContent,messages" } } }),
		);
		expect(remoteGet).toHaveBeenCalledTimes(1);
	});

	it("searches chats alone when no session is on screen", async () => {
		localGet.mockResolvedValue(searchResponse());
		const options = commandPaletteFileSearchQueryOptions({
			query: "deploy", includeMessages: true, errorMessage: "failed", cloud: { client: {} as CloudCpClient, baseUrl: "", ready: false },
		});
		await options.queryFn();
		expect(localGet).toHaveBeenCalledWith(
			"/api/v1/search",
			expect.objectContaining({ params: { query: { q: "deploy", kinds: "messages" } } }),
		);
	});

	it("surfaces daemon errors", async () => {
		localGet.mockResolvedValue({ error: { code: "SEARCH_QUERY_TOO_LONG", message: "query is longer than 512 bytes" } });
		const options = commandPaletteFileSearchQueryOptions({
			target: { projectId: "p", sessionId: "s" }, query: "x", includeMessages: true, errorMessage: "failed",
			cloud: { client: {} as CloudCpClient, baseUrl: "", ready: false },
		});
		await expect(options.queryFn()).rejects.toThrow();
	});

	it("uses the Cloud review search without falling back to the daemon", async () => {
		const searchWorkspaceReview = vi.fn().mockResolvedValue({
			results: [{ path: "cloud.ts", status: "added", size: 3, binary: false, fileFingerprint: "v1" }],
			truncated: false,
		});
		const options = commandPaletteFileSearchQueryOptions({
			target: { projectId: "p", sessionId: "s", cloudOrgId: "org-1" },
			query: "cloud",
			includeMessages: true,
			errorMessage: "failed",
			cloud: {
				client: { searchWorkspaceReview } as unknown as CloudCpClient,
				baseUrl: "https://cloud.example",
				ready: true,
			},
		});
		const controller = new AbortController();
		const result = await options.queryFn({ signal: controller.signal });
		expect(result.results[0]?.path).toBe("cloud.ts");
		expect(result.fileNames).toBeUndefined();
		expect(searchWorkspaceReview).toHaveBeenCalledWith(
			"org-1",
			"s",
			{ query: "cloud", limit: 20 },
			{ signal: controller.signal },
		);
		expect(localGet).not.toHaveBeenCalled();
	});

	it("requires Cloud readiness only for Cloud targets, and a target or chats", () => {
		expect(commandPaletteFileSearchAvailable({ projectId: "p", sessionId: "s" }, false)).toBe(true);
		expect(commandPaletteFileSearchAvailable({ projectId: "p", sessionId: "s", cloudOrgId: "org" }, false)).toBe(false);
		expect(commandPaletteFileSearchAvailable({ projectId: "p", sessionId: "s", cloudOrgId: "org" }, true)).toBe(true);
		expect(commandPaletteFileSearchAvailable(undefined, false, true)).toBe(true);
		expect(commandPaletteFileSearchAvailable(undefined, false, false)).toBe(false);
	});
});
