import { describe, expect, it, vi } from "vitest";
import type { CloudClient } from "../cloud/client";
import { CloudError } from "../cloud/errors";
import { parseUnifiedDiff } from "../../../shared/files/diff-parser";
import { addedFileDiff, cloudFilesBackend, workspaceStatus } from "./cloudBackend";
import { changeGroups } from "./model";

const file = (path: string, status: string, extra: object = {}) => ({ path, status, additions: 2, deletions: 1, size: 10, binary: false, editable: true, fileFingerprint: `fp-${path}`, ...extra });

function backend(client: Partial<Record<keyof CloudClient, unknown>>) {
	return cloudFilesBackend(client as unknown as CloudClient, "org-1");
}

describe("a cloud session's files, in the daemon's shapes", () => {
	it("reads copies and untracked files as new ones", () => {
		expect(workspaceStatus("copied")).toBe("added");
		expect(workspaceStatus("untracked")).toBe("added");
		expect(workspaceStatus("renamed")).toBe("renamed");
		expect(workspaceStatus(undefined)).toBe("unmodified");
	});

	it("reads the review as the changed-files manifest", async () => {
		const getWorkspaceReview = vi.fn(async () => ({
			workspaceVersion: "v7", compareBaseRef: "origin/main", compareMode: "base", truncated: false, ahead: 2, behind: 0,
			files: [file("src/a.ts", "modified"), file("b.ts", "untracked")],
			sections: { committed: [file("src/a.ts", "modified")], untracked: [file("b.ts", "untracked")] },
			summary: { files: 2, additions: 4, deletions: 2 },
			commits: [], repositories: [{ name: "web", present: true }],
		}));
		const manifest = await backend({ getWorkspaceReview }).manifest("s1");
		expect(getWorkspaceReview).toHaveBeenCalledWith("org-1", "s1", undefined);
		expect(manifest).toMatchObject({ sessionId: "s1", workspaceVersion: "v7", compareBaseRef: "origin/main", ahead: 2, degraded: false, summary: { files: 2 } });
		expect(manifest.files.map((item) => item.status)).toEqual(["modified", "added"]);
		expect(manifest.sections.staged).toEqual([]);
		expect(manifest.sections.untracked[0]).toEqual({ path: "b.ts", previousPath: undefined, status: "added", additions: 2, deletions: 1, binary: false, size: 10, fileFingerprint: "fp-b.ts" });
	});

	// Seen on the Pixel: "Changes 2" over a list of every file in the repository
	// at ±0. The review's files are the worktree's whole inventory; the
	// changes are its sections, which is all the desktop draws.
	it("lists only changed files, not the unchanged inventory beside them", async () => {
		const getWorkspaceReview = vi.fn(async () => ({
			workspaceVersion: "v9", compareBaseRef: "refs/ao/diff-base", compareMode: "base", truncated: false,
			files: [file(".gitignore", "unmodified", { additions: 0, deletions: 0 }), file("README.md", "modified"), file("notes/a.md", "added"), file("package.json", "unmodified", { additions: 0, deletions: 0 })],
			sections: { committed: [file("README.md", "modified"), file("notes/a.md", "added")], staged: [], unstaged: [], untracked: [] },
			summary: { files: 2, additions: 3, deletions: 0 },
			commits: [],
		}));
		const manifest = await backend({ getWorkspaceReview }).manifest("s1");
		expect(manifest.files.map((item) => item.path)).toEqual(["README.md", "notes/a.md"]);
		expect(changeGroups(manifest).map((group) => [group.key, group.files.length])).toEqual([["committed", 2]]);
	});

	it("reads one file with its diff, for the section asked", async () => {
		const getWorkspaceReviewFile = vi.fn(async () => ({ ...file("src/a.ts", "modified"), deleted: false, content: "x", contentTruncated: false, diff: "@@", diffTruncated: false, workspaceVersion: "v7" }));
		const detail = await backend({ getWorkspaceReviewFile }).file("s1", "src/a.ts", { section: "staged" });
		expect(getWorkspaceReviewFile).toHaveBeenCalledWith("org-1", "s1", { path: "src/a.ts", scope: "staged" });
		expect(detail).toMatchObject({ path: "src/a.ts", status: "modified", content: "x", diff: "@@", fileFingerprint: "fp-src/a.ts", workspaceVersion: "v7" });
	});

	// The worker's diff leaves out a file git does not track yet; a machine shows every line added.
	it("shows a new file git does not track yet as every line added", async () => {
		const getWorkspaceReviewFile = vi.fn(async () => ({ ...file("notes/a.md", "untracked", { additions: 2 }), deleted: false, content: "# A\n- one\n", contentTruncated: false, diff: "", diffTruncated: false, workspaceVersion: "v8" }));
		const detail = await backend({ getWorkspaceReviewFile }).file("s1", "notes/a.md");
		expect(detail.status).toBe("added");
		expect(parseUnifiedDiff(detail.diff).filter((row) => row.kind === "add").map((row) => row.text)).toEqual(["# A", "- one"]);
		expect(addedFileDiff("x", "")).toBe("");
		expect(addedFileDiff("x", "no newline")).toContain("\\ No newline at end of file");
	});

	it("reads folders and search results with the same statuses", async () => {
		const files = backend({
			getWorkspaceTree: vi.fn(async () => ({ path: "src", truncated: false, entries: [{ name: "a.ts", path: "src/a.ts", type: "file", status: "copied" }, { name: "lib", path: "src/lib", type: "dir" }] })),
			searchWorkspace: vi.fn(async () => ({ query: "a", results: [file("src/a.ts", "untracked")], nextCursor: "c2", truncated: false })),
		});
		const tree = await files.tree("s1", "src");
		expect(tree.entries.map((entry) => entry.status)).toEqual(["added", undefined]);
		const found = await files.search("s1", "a");
		expect(found).toEqual({ results: [{ path: "src/a.ts", status: "added", binary: false, size: 10 }], truncated: true });
	});

	// No image URL on the control plane: the bytes come back as base64.
	it("shows a changed image from its revision, and nothing it cannot draw", async () => {
		const getWorkspaceRevision = vi.fn()
			.mockResolvedValueOnce({ path: "a.png", side: "after", workspaceVersion: "v7", mediaType: "image/png", encoding: "base64", size: 3, exists: true, binary: true, truncated: false, content: "iVBO" })
			.mockResolvedValueOnce({ path: "a.png", side: "before", workspaceVersion: "v7", size: 3, exists: true, binary: true, truncated: true, content: "" })
			.mockRejectedValueOnce(new CloudError("gone", { status: 409, code: "WORKER_UNAVAILABLE" }));
		const files = backend({ getWorkspaceRevision });
		expect(await files.imageSource("s1", "a.png", "after", "v7")).toEqual({ uri: "data:image/png;base64,iVBO" });
		expect(getWorkspaceRevision).toHaveBeenCalledWith("org-1", "s1", { path: "a.png", side: "after", workspaceVersion: "v7" });
		expect(await files.imageSource("s1", "a.png", "before", "v7")).toBeUndefined();
		expect(await files.imageSource("s1", "a.png", "before", "v7")).toBeUndefined();
	});

	it("sends review comments as the person's messages, and says a refusal in Cloud's words", async () => {
		const sendMessage = vi.fn(async (..._args: unknown[]) => ({ event: {} }));
		const target = { path: "src/a.ts", workspaceVersion: "v7", fileFingerprint: "fp", startLine: 1, endLine: 1, side: "new" };
		const sent: number[] = [];
		const count = await backend({ sendMessage }).sendFeedback("s1", [{ target, feedback: "Rename this" }] as never, (n) => sent.push(n));
		expect(count).toBe(1);
		expect(sent).toEqual([1]);
		expect(sendMessage.mock.calls[0]?.[0]).toBe("org-1");
		expect(Object.keys(sendMessage.mock.calls[0]?.[2] as object)).toEqual(["text"]);

		const refused = backend({ getWorkspaceReview: vi.fn(async () => { throw new CloudError("The session's worker is not connected.", { status: 409, code: "WORKER_UNAVAILABLE" }); }) });
		const failure = await refused.manifest("s1").catch((error: unknown) => error);
		expect(failure).not.toHaveProperty("status");
		expect((failure as Error).message).toBeTruthy();
	});
});
