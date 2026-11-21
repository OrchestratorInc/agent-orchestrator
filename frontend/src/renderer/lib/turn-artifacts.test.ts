import { describe, expect, it } from "vitest";
import type { TurnDiff } from "../types/conversation";
import type { SessionArtifact } from "../types/workspace";
import { artifactRelativePath, splitTurnArtifacts } from "./turn-artifacts";

const SESSION = "agent-orchestrator-93";
const ROOT = `/Users/me/.ao/data/artifacts/${SESSION}`;

function artifact(path: string, overrides: Partial<SessionArtifact> = {}): SessionArtifact {
	return {
		kind: path.endsWith(".html") ? "html" : path.endsWith(".md") ? "markdown" : "file",
		name: path.split("/").pop() ?? path,
		path,
		size: 10,
		updatedAt: "2026-11-10T10:00:00Z",
		...overrides,
	};
}

describe("artifactRelativePath", () => {
	it("finds the path inside this session's artifact directory", () => {
		expect(artifactRelativePath(`${ROOT}/report.md`, SESSION)).toBe("report.md");
		expect(artifactRelativePath(`~/.ao/data/artifacts/${SESSION}/a/b.png`, SESSION)).toBe("a/b.png");
		expect(artifactRelativePath(`C:\\Users\\me\\.ao\\data\\artifacts\\${SESSION}\\x.html`, SESSION)).toBe("x.html");
	});

	it("rejects worktree files, other sessions, and relative paths", () => {
		expect(artifactRelativePath("/Users/me/.ao/data/worktrees/ao/x/report.md", SESSION)).toBeUndefined();
		expect(artifactRelativePath("/Users/me/.ao/data/artifacts/other-session/report.md", SESSION)).toBeUndefined();
		expect(artifactRelativePath(`artifacts/${SESSION}/report.md`, SESSION)).toBeUndefined();
		expect(artifactRelativePath(`${ROOT}/`, SESSION)).toBeUndefined();
	});
});

describe("splitTurnArtifacts", () => {
	const diff: TurnDiff = {
		files: [
			{ path: `${ROOT}/report.md`, additions: 26, deletions: 0, status: "added" },
			{ path: `${ROOT}/clock.html`, additions: 3, deletions: 1, status: "modified" },
			{ path: "src/app.ts", additions: 1, deletions: 1, status: "modified" },
		],
	};

	it("moves listed deliverables out of the changed files", () => {
		const split = splitTurnArtifacts(diff, SESSION, [artifact("report.md"), artifact("clock.html")]);
		expect(split.artifacts.map((entry) => [entry.artifact.path, entry.status])).toEqual([
			["report.md", "added"],
			["clock.html", "modified"],
		]);
		expect(split.files.map((file) => file.path)).toEqual(["src/app.ts"]);
	});

	it("keeps a file the session does not list, or lists only as a supporting file", () => {
		const split = splitTurnArtifacts(diff, SESSION, [
			artifact("report.md", { registered: true }),
			artifact("clock.html"),
		]);
		expect(split.artifacts.map((entry) => entry.artifact.path)).toEqual(["report.md"]);
		expect(split.files.map((file) => file.path)).toEqual([`${ROOT}/clock.html`, "src/app.ts"]);
	});

	it("keeps a deleted artifact as a changed file", () => {
		const split = splitTurnArtifacts(
			{ files: [{ path: `${ROOT}/report.md`, additions: 0, deletions: 5, status: "deleted" }] },
			SESSION,
			[artifact("report.md")],
		);
		expect(split.artifacts).toEqual([]);
		expect(split.files).toHaveLength(1);
	});

	it("resolves bare names through the turn's absolute paths", () => {
		const split = splitTurnArtifacts(
			{ files: [{ path: "report.md", additions: 1, deletions: 0, status: "added" }] },
			SESSION,
			[artifact("report.md")],
			(path) => `${ROOT}/${path}`,
		);
		expect(split.artifacts[0]?.artifact.path).toBe("report.md");
		expect(split.files).toEqual([]);
	});

	it("finds an artifact under the path it had before a rename", () => {
		const split = splitTurnArtifacts(
			{ files: [{ path: `${ROOT}/draft.md`, additions: 1, deletions: 0, status: "added" }] },
			SESSION,
			[artifact("final.md", { previousPaths: ["draft.md"] })],
		);
		expect(split.artifacts[0]?.artifact.path).toBe("final.md");
	});

	it("labels a bundle Created only when the turn added every file in it, in any order", () => {
		const bundle = [artifact("demo.app/index.html", { registered: true }), artifact("demo.app/extra.css", { registered: true })];
		const added = { path: `${ROOT}/demo.app/extra.css`, additions: 3, deletions: 0, status: "added" as const };
		const modified = { path: `${ROOT}/demo.app/index.html`, additions: 1, deletions: 1, status: "modified" as const };
		for (const files of [[added, modified], [modified, added]]) {
			const split = splitTurnArtifacts({ files }, SESSION, bundle);
			expect(split.artifacts).toHaveLength(1);
			expect(split.artifacts[0]?.status).toBe("modified");
		}
		const created = splitTurnArtifacts(
			{ files: [added, { ...modified, status: "added" }] },
			SESSION,
			bundle,
		);
		expect(created.artifacts[0]?.status).toBe("added");
	});

	it("shows artifacts the daemon saw without any provider diff", () => {
		const split = splitTurnArtifacts(undefined, SESSION, [artifact("chart.svg"), artifact("notes.md")], undefined, [
			{ path: "chart.svg", status: "added" },
			{ path: "notes.md", status: "modified" },
			{ path: "scratch/tmp.txt", status: "added" },
		]);
		expect(split.artifacts.map((entry) => [entry.artifact.path, entry.status])).toEqual([
			["chart.svg", "added"],
			["notes.md", "modified"],
		]);
		expect(split.files).toEqual([]);
	});

	it("makes one card when the diff and the daemon both report a file", () => {
		const split = splitTurnArtifacts(
			{ files: [{ path: `${ROOT}/report.md`, additions: 26, deletions: 0, status: "added" }] },
			SESSION,
			[artifact("report.md")],
			undefined,
			[{ path: "report.md", status: "added" }],
		);
		expect(split.artifacts).toHaveLength(1);
		expect(split.artifacts[0]?.status).toBe("added");
		expect(split.files).toEqual([]);
	});

	it("leaves the diff untouched without a session or artifacts", () => {
		expect(splitTurnArtifacts(diff, undefined, [artifact("report.md")]).files).toBe(diff.files);
		expect(splitTurnArtifacts(diff, SESSION, []).files).toBe(diff.files);
	});
});
