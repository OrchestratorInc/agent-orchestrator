import { beforeEach, describe, expect, it, vi } from "vitest";

vi.mock("@react-native-async-storage/async-storage", () => ({
	default: { getItem: vi.fn(), setItem: vi.fn(), removeItem: vi.fn() },
}));
vi.mock("expo-secure-store", () => ({
	getItemAsync: vi.fn(), setItemAsync: vi.fn(), deleteItemAsync: vi.fn(),
}));
vi.mock("expo/fetch", () => ({ fetch: vi.fn() }));

const getProjects = vi.fn();
const getSessions = vi.fn();
const killSession = vi.fn();
const delegateTask = vi.fn();
vi.mock("../api", async (importOriginal) => ({
	...(await importOriginal<typeof import("../api")>()),
	getProjects: (...args: unknown[]) => getProjects(...args),
	getSessions: (...args: unknown[]) => getSessions(...args),
	killSession: (...args: unknown[]) => killSession(...args),
	delegateTask: (...args: unknown[]) => delegateTask(...args),
}));

import { DEFAULT_CONFIG, type ServerConfig } from "../config";
import { createLocalSessionSource } from "./local";

const cfg: ServerConfig = { ...DEFAULT_CONFIG, host: "10.0.0.2", password: "pw" };

describe("createLocalSessionSource", () => {
	beforeEach(() => {
		vi.clearAllMocks();
	});

	it("identifies as the local environment", () => {
		expect(createLocalSessionSource(cfg).kind).toBe("local");
	});

	it("keeps PR actions on the bound Local source without a Cloud preview transport", () => {
		const source = createLocalSessionSource(cfg);
		expect(source.pullRequests?.kind).toBe("local");
		expect(source.pullRequests?.scopeKey).toBe(source.workspace?.scopeKey);
		expect(source.preview).toBeUndefined();
	});

	it("keeps workspace reads, images and user-authored feedback on the bound daemon", async () => {
		const requests: Array<{ url: string; init?: RequestInit }> = [];
		vi.stubGlobal("fetch", async (url: string, init?: RequestInit) => { requests.push({ url, init }); return new Response(JSON.stringify({ workspaceVersion: "local-v1", content: "local file", entries: [], results: [] })); });
		try {
			const workspace = createLocalSessionSource({ ...cfg, hostId: "local-host" }).workspace;
			expect(workspace).toBeDefined();
			const controller = new AbortController();
			expect((await workspace!.getManifest("s 1", { signal: controller.signal })).workspaceVersion).toBe("local-v1");
			expect((await workspace!.getFile("s 1", "a b.ts", "staged", { signal: controller.signal })).content).toBe("local file");
			await workspace!.getTree("s 1", "src"); await workspace!.search("s 1", "a b");
			await workspace!.sendFeedback("s 1", [{ target: { path: "a.ts", side: "new", line: 1 }, feedback: "rename" }]);
			expect(requests.map(({ url }) => url)).toEqual([
				"http://10.0.0.2:3011/api/v1/sessions/s%201/workspace/manifest",
				"http://10.0.0.2:3011/api/v1/sessions/s%201/workspace/file?path=a+b.ts&section=staged",
				"http://10.0.0.2:3011/api/v1/sessions/s%201/workspace/tree?path=src",
				"http://10.0.0.2:3011/api/v1/sessions/s%201/workspace/search?query=a+b&limit=50",
				"http://10.0.0.2:3011/api/v1/sessions/s%201/send",
			]);
			expect(JSON.parse(String(requests[4].init?.body))).toMatchObject({ userAuthored: true, message: expect.stringContaining("rename") });
			expect(workspace!.imageSource!("s 1", "a b.png", "after", "v1")).toEqual({ uri: "http://10.0.0.2:3011/api/v1/sessions/s%201/workspace/file/blob?path=a+b.png&side=after&v=v1", headers: { Authorization: "Bearer pw", "X-AO-Expected-Host-ID": "local-host" } });
		} finally { vi.unstubAllGlobals(); }
	});

	it("forwards listProjects to the daemon with the active config", async () => {
		getProjects.mockResolvedValue([{ id: "p1", name: "web" }]);
		const result = await createLocalSessionSource(cfg).listProjects();
		expect(getProjects).toHaveBeenCalledWith(cfg);
		expect(result).toEqual([{ id: "p1", name: "web" }]);
	});

	// getSessions returns the daemon's full board payload; the port only
	// promises the session list, so the adapter unwraps it here rather than
	// widening the interface for one environment's extra fields.
	it("unwraps the daemon's sessions response", async () => {
		getSessions.mockResolvedValue({
			sessions: [{ id: "s1" }], orchestrators: [], orchestratorId: null, stats: {}, projects: [],
		});
		expect(await createLocalSessionSource(cfg).listSessions()).toEqual([{ id: "s1" }]);
		expect(getSessions).toHaveBeenCalledWith(cfg);
	});

	it("spawns through delegateTask and returns the new session id", async () => {
		delegateTask.mockResolvedValue({ id: "s9" });
		const result = await createLocalSessionSource(cfg).createSession({
			projectId: "p1", prompt: "fix the build", mode: "chat",
		});
		expect(result).toEqual({ id: "s9" });
		expect(delegateTask).toHaveBeenCalledWith(cfg, expect.objectContaining({
			projectId: "p1", brief: "fix the build", mode: "chat",
		}));
	});

	it("refuses to spawn without a resolved project", async () => {
		await expect(
			createLocalSessionSource(cfg).createSession({ prompt: "x" }),
		).rejects.toThrow("Pick a project first");
		expect(delegateTask).not.toHaveBeenCalled();
	});

	it("deletes a session by killing it", async () => {
		await createLocalSessionSource(cfg).deleteSession("s1");
		expect(killSession).toHaveBeenCalledWith(cfg, "s1");
	});

	it("rejects resumeSession for local sessions", async () => {
		await expect(
			createLocalSessionSource(cfg).resumeSession("s1"),
		).rejects.toThrow("resumeSession is not supported for the local environment");
	});
});
