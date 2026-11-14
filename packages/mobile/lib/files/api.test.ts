import { beforeEach, describe, expect, it, vi } from "vitest";

const sent = vi.hoisted(() => ({ bodies: [] as Array<{ url: string; body: { message: string; userAuthored?: boolean } }>, failAt: -1 }));

vi.mock("../api", () => ({
	apiRequest: vi.fn(async (_cfg: unknown, url: string, init?: { body?: string }) => {
		if (sent.bodies.length === sent.failAt) throw new Error("offline");
		sent.bodies.push({ url, body: JSON.parse(init?.body ?? "{}") });
		return new Response("{}");
	}),
}));

vi.mock("../config", () => ({
	httpBase: (c: { host: string; httpPort: number }) => `http://${c.host}:${c.httpPort}`,
	authHeaders: (c: { password?: string; hostId?: string }) => ({
		...(c.password ? { Authorization: `Bearer ${c.password}` } : {}),
		...(c.hostId ? { "X-AO-Expected-Host-ID": c.hostId } : {}),
	}),
}));

import { getWorkspaceFile, getWorkspaceHistory, getWorkspaceRangeFiles, getWorkspaceTree, searchWorkspaceFiles, sendFileFeedback, workspaceImageSource } from "./api";
import { daemonFilesBackend } from "./backend";
import { changedPathCount } from "./navigation";

const cfg = { host: "mac.local", httpPort: 3001, password: "pw", hostId: "h1" } as never;

beforeEach(() => {
	sent.bodies = [];
	sent.failAt = -1;
});

describe("sending review feedback", () => {
	it("relays comments as the user's own words, batched like desktop", async () => {
		const count = await sendFileFeedback(cfg, "s 1", [
			{ target: { path: "a.ts", side: "new", line: 3 }, feedback: "rename this" },
			{ target: { path: "b.ts", side: "old", line: 9 }, feedback: "why was this removed?" },
		]);
		expect(count).toBe(2);
		expect(sent.bodies).toHaveLength(1);
		expect(sent.bodies[0].url).toBe("/api/v1/sessions/s%201/send");
		expect(sent.bodies[0].body.userAuthored).toBe(true);
		expect(sent.bodies[0].body.message).toContain("The user left 2 inline feedback comments");
		expect(sent.bodies[0].body.message).toContain("- Path: b.ts");
	});

	it("reports each delivered message so a failure keeps only what was not sent", async () => {
		const long = "x".repeat(1700);
		const comments = Array.from({ length: 4 }, (_, index) => ({ target: { path: `f${index}.ts`, side: "new" as const, line: 1 }, feedback: long }));
		const delivered: number[] = [];
		sent.failAt = 1;
		await expect(sendFileFeedback(cfg, "s", comments, (count) => delivered.push(count))).rejects.toThrow("offline");
		expect(delivered).toHaveLength(1);
		expect(delivered[0]).toBeGreaterThan(0);
		expect(delivered[0]).toBeLessThan(4);
	});
});

describe("image sources", () => {
	it("points at the blob route with auth headers and a cache-busting version", () => {
		expect(workspaceImageSource(cfg, "s", "img/a b.png", "before", "v7")).toEqual({
			uri: "http://mac.local:3001/api/v1/sessions/s/workspace/file/blob?path=img%2Fa+b.png&side=before&v=v7",
			headers: { Authorization: "Bearer pw", "X-AO-Expected-Host-ID": "h1" },
		});
	});
});

describe("reading another branch or a commit", () => {
	const view = { branch: "refs/remotes/origin/main" };

	it("reads one commit, or a view's snapshot of a file, from the file route", async () => {
		await getWorkspaceFile(cfg, "s", "a.ts");
		await getWorkspaceFile(cfg, "s", "a.ts", { commitSha: "abc" });
		// A view is one diff, so a section would mean nothing to it.
		await getWorkspaceFile(cfg, "s", "a.ts", { section: "staged" }, view);
		await getWorkspaceFile(cfg, "s", "a.ts", { commitSha: "abc" }, { prNumber: 4, sourceUrl: "https://x/pull/4" });
		expect(sent.bodies.map((entry) => entry.url)).toEqual([
			"/api/v1/sessions/s/workspace/file?path=a.ts",
			"/api/v1/sessions/s/workspace/file?path=a.ts&commitSha=abc",
			"/api/v1/sessions/s/workspace/file?path=a.ts&snapshot=true&branch=refs%2Fremotes%2Forigin%2Fmain",
			"/api/v1/sessions/s/workspace/file?path=a.ts&snapshot=true&prNumber=4&sourceUrl=https%3A%2F%2Fx%2Fpull%2F4&commitSha=abc",
		]);
	});

	// The file route's `section` scopes only its diff; status and content stay
	// the combined view's. A worktree scope reads its own patch and after side.
	it("reads a worktree scope from its scoped patch and after side", async () => {
		for (const section of ["staged", "unstaged", "uncommitted", "untracked", "committed"] as const) {
			sent.bodies = [];
			await getWorkspaceFile(cfg, "s", "src/a b.ts", { section });
			expect(sent.bodies.map((entry) => entry.url).sort()).toEqual([
				"/api/v1/sessions/s/workspace/diffs",
				`/api/v1/sessions/s/workspace/file/revision?path=src%2Fa+b.ts&scope=${section}&side=after`,
			]);
			expect(sent.bodies.find((entry) => entry.url.endsWith("/diffs"))?.body).toEqual({ paths: ["src/a b.ts"], scope: section, contextLines: 3, ignoreWhitespace: false });
		}
	});

	it("browses and searches a view's tip through its tree", async () => {
		await getWorkspaceTree(cfg, "s", "");
		await getWorkspaceTree(cfg, "s", "src", view);
		await searchWorkspaceFiles(cfg, "s", "app", view);
		expect(sent.bodies.map((entry) => entry.url)).toEqual([
			"/api/v1/sessions/s/workspace/tree",
			"/api/v1/sessions/s/workspace/tree?path=src&snapshot=true&branch=refs%2Fremotes%2Forigin%2Fmain",
			"/api/v1/sessions/s/workspace/tree?filter=app&snapshot=true&branch=refs%2Fremotes%2Forigin%2Fmain",
		]);
	});

	it("asks for a view's history and its whole range of commits", async () => {
		await getWorkspaceHistory(cfg, "s");
		await getWorkspaceHistory(cfg, "s", { prNumber: 4 });
		await getWorkspaceRangeFiles(cfg, "s", view);
		expect(sent.bodies.map((entry) => entry.url)).toEqual([
			"/api/v1/sessions/s/workspace/history",
			"/api/v1/sessions/s/workspace/history?prNumber=4",
			"/api/v1/sessions/s/workspace/history/selection",
		]);
		expect(sent.bodies[2].body).toEqual({ branch: "refs/remotes/origin/main", commitShas: [] });
	});

	it("points a view's or a commit's image at its snapshot", () => {
		expect(workspaceImageSource(cfg, "s", "a.png", "after", "v1", { commitSha: "abc" }, view).uri)
			.toBe("http://mac.local:3001/api/v1/sessions/s/workspace/file/blob?path=a.png&side=after&v=v1&snapshot=true&branch=refs%2Fremotes%2Forigin%2Fmain&commitSha=abc");
	});
});

describe("the machine's image sides", () => {
	const backend = daemonFilesBackend(cfg);

	it("offers no image for a worktree scope, rather than the base and the worktree", async () => {
		for (const section of ["staged", "unstaged", "uncommitted", "committed"] as const) {
			await expect(backend.imageSource("s", "a.png", "before", "v", { section })).resolves.toBeUndefined();
			await expect(backend.imageSource("s", "a.png", "after", "v", { section })).resolves.toBeUndefined();
		}
	});

	it("reads all changes, an untracked file, a commit and a view from the blob route", async () => {
		expect((await backend.imageSource("s", "a.png", "after", "v"))?.uri).toContain("/workspace/file/blob?path=a.png&side=after&v=v");
		expect((await backend.imageSource("s", "a.png", "after", "v", { section: "untracked" }))?.uri).toContain("/workspace/file/blob?");
		expect((await backend.imageSource("s", "a.png", "before", "v", { commitSha: "abc" }))?.uri).toContain("commitSha=abc");
		expect((await backend.git!.at({ branch: "main" }).imageSource("s", "a.png", "before", "v"))?.uri).toContain("snapshot=true&branch=main");
	});
});

describe("changedPathCount", () => {
	it("counts each path once across turns", () => {
		expect(changedPathCount([{ diff: { files: [{ path: "a" }, { path: "b" }] } }, { diff: { files: [{ path: "a" }] } }, {}])).toBe(2);
		expect(changedPathCount(undefined)).toBe(0);
	});
});
