import { beforeEach, describe, expect, it, vi } from "vitest";

// A scripted daemon: each request takes the next answer, so the worktree can
// move between a scoped read's two halves.
const daemon = vi.hoisted(() => ({ answers: [] as Array<unknown>, urls: [] as string[] }));

vi.mock("../api", () => {
	class ApiError extends Error {
		constructor(readonly status: number, message: string, readonly code?: string) {
			super(message);
		}
	}
	return {
		ApiError,
		apiRequest: vi.fn(async (_cfg: unknown, url: string) => {
			daemon.urls.push(url);
			const next = daemon.answers.shift();
			if (next instanceof Error) throw next;
			return new Response(JSON.stringify(next));
		}),
	};
});
vi.mock("../config", () => ({ httpBase: () => "http://mac", authHeaders: () => ({}) }));

import { ApiError } from "../api";
import { getScopedWorkspaceFile } from "./api";

const cfg = {} as never;
const diffs = (version: string, added: string) => ({
	workspaceVersion: version,
	groups: [{ includedPaths: ["a.ts"], patch: `diff --git a/a.ts b/a.ts\n--- a/a.ts\n+++ b/a.ts\n@@ -1 +1,2 @@\n a\n+${added}\n` }],
});
const revision = (content: string) => ({ exists: true, content, revision: content });
const stale = () => new (ApiError as unknown as new (status: number, message: string, code: string) => Error)(409, "Workspace changed while the file revision was loading", "WORKSPACE_SNAPSHOT_STALE");

beforeEach(() => {
	daemon.answers = [];
	daemon.urls = [];
});

describe("a worktree scope's file read", () => {
	it("fences the after side to the patch's version, and reads the pair again when the worktree moves", async () => {
		// The agent stages B between the patch (at v1) and the content read.
		daemon.answers = [diffs("v1", "A"), stale(), diffs("v2", "B"), revision("a\nB\n")];
		const detail = await getScopedWorkspaceFile(cfg, "s", "a.ts", "staged");
		expect(daemon.urls).toEqual([
			"/api/v1/sessions/s/workspace/diffs",
			"/api/v1/sessions/s/workspace/file/revision?path=a.ts&scope=staged&side=after&workspaceVersion=v1",
			"/api/v1/sessions/s/workspace/diffs",
			"/api/v1/sessions/s/workspace/file/revision?path=a.ts&scope=staged&side=after&workspaceVersion=v2",
		]);
		// Patch and content come from the same moment, never patch A over content B.
		expect(detail.diff).toContain("+B");
		expect(detail.content).toBe("a\nB\n");
		expect(detail.workspaceVersion).toBe("v2");
	});

	it("gives up rather than mix versions when the worktree keeps moving", async () => {
		daemon.answers = [diffs("v1", "A"), stale(), diffs("v2", "B"), stale(), diffs("v3", "C"), stale()];
		await expect(getScopedWorkspaceFile(cfg, "s", "a.ts", "staged")).rejects.toThrow("Workspace changed");
		expect(daemon.urls.filter((url) => url.endsWith("/diffs"))).toHaveLength(3);
	});

	it("surfaces a repository's git failure instead of reading it as no changes", async () => {
		daemon.answers = [{ workspaceVersion: "v1", groups: [{ patch: "", errors: [{ code: "WORKSPACE_DIFF_FAILED", message: "git diff failed in repo api" }] }] }];
		await expect(getScopedWorkspaceFile(cfg, "s", "a.ts", "unstaged")).rejects.toThrow("git diff failed in repo api");
		expect(daemon.urls).toHaveLength(1);
	});

	it("passes any other failure straight through", async () => {
		daemon.answers = [diffs("v1", "A"), new Error("offline")];
		await expect(getScopedWorkspaceFile(cfg, "s", "a.ts", "staged")).rejects.toThrow("offline");
		expect(daemon.urls).toHaveLength(2);
	});
});
