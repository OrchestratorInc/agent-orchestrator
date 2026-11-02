import { describe, expect, it } from "vitest";
import { parseUnifiedDiff } from "../../../shared/files/diff-parser";
import type { WorkspaceFileSummary, WorkspaceManifest } from "./api";
import {
	annotationTarget,
	changeAccessibilityLabel,
	changeGroups,
	commentAnchor,
	compactCount,
	contentLines,
	diffDisplayRows,
	fileDisplayRows,
	languageForPath,
	orderedChanges,
	selectedLines,
	selectionLabel,
	splitPath,
	withInlineComments,
	type DisplayRow,
} from "./model";

const file = (path: string, extra: Partial<WorkspaceFileSummary> = {}): WorkspaceFileSummary => ({
	path, status: "modified", additions: 1, deletions: 1, binary: false, size: 10, ...extra,
});

const manifest = (sections: Partial<WorkspaceManifest["sections"]>, files: WorkspaceFileSummary[] = []): Pick<WorkspaceManifest, "files" | "sections"> => ({
	files,
	sections: { committed: [], staged: [], unstaged: [], untracked: [], ...sections },
});

describe("changeGroups", () => {
	it("splits uncommitted work from what is already on the branch, each path once", () => {
		const groups = changeGroups(manifest({
			committed: [file("src/a.ts"), file("src/b.ts")],
			unstaged: [file("src/b.ts")],
			untracked: [file("notes.md", { status: "added" })],
		}));
		expect(groups.map((group) => [group.key, group.files.map((entry) => entry.path)])).toEqual([
			["uncommitted", ["notes.md", "src/b.ts"]],
			["committed", ["src/a.ts"]],
		]);
		expect(groups[0].files[1].sections).toEqual(["unstaged", "committed"]);
	});

	it("takes counts from the combined list, which is the diff the viewer opens", () => {
		const groups = changeGroups(manifest({ staged: [file("a.ts", { additions: 2 })] }, [file("a.ts", { additions: 9, deletions: 4 })]));
		expect(groups[0].files[0]).toMatchObject({ additions: 9, deletions: 4, sections: ["staged"] });
	});

	it("keeps files a degraded manifest lists without sections", () => {
		const groups = changeGroups(manifest({}, [file("loose.go")]));
		expect(orderedChanges(groups).map((entry) => entry.path)).toEqual(["loose.go"]);
	});

	it("omits empty groups", () => {
		expect(changeGroups(manifest({}))).toEqual([]);
	});
});

describe("paths and labels", () => {
	it("splits a path into a name and its directory", () => {
		expect(splitPath("src/auth/pkce.ts")).toEqual({ name: "pkce.ts", dir: "src/auth" });
		expect(splitPath("README.md")).toEqual({ name: "README.md", dir: "" });
	});

	it("reads a row the way a screen reader should announce it", () => {
		expect(changeAccessibilityLabel({ path: "src/a.ts", status: "renamed", previousPath: "lib/a.ts", additions: 1, deletions: 0, binary: false }))
			.toBe("a.ts in src, renamed, renamed from lib/a.ts, 1 line added, 0 lines removed");
	});

	it("compacts large line counts for the composer pill", () => {
		expect([0, 999, 1000, 2395, 9960, 12400].map(compactCount)).toEqual(["0", "999", "1k", "2.4k", "10k", "12k"]);
	});

	it("maps paths to highlighter languages", () => {
		expect(languageForPath("a/b.tsx")).toBe("tsx");
		expect(languageForPath("Dockerfile")).toBe("shell");
		expect(languageForPath("LICENSE")).toBeUndefined();
	});
});

const twoHunks = [
	"diff --git a/f.ts b/f.ts",
	"--- a/f.ts",
	"+++ b/f.ts",
	"@@ -3,3 +3,3 @@ function top",
	" three",
	"-four",
	"+FOUR",
	" five",
	"@@ -20,2 +20,3 @@ function low",
	" twenty",
	"+inserted",
	" twentyone",
].join("\n");

const fileLines = Array.from({ length: 30 }, (_, index) => `line ${index + 1}`);

const summarize = (rows: DisplayRow[]) => rows.map((row) => row.type === "fold"
	? `fold ${row.id} ${row.hidden}${row.expandable ? "" : " locked"}`
	: row.type === "comment"
		? `comment ${row.commentId}`
		: `${row.row.kind} ${row.row.oldNo ?? "-"}/${row.row.newNo ?? "-"}`);

describe("diffDisplayRows", () => {
	it("folds the unchanged code before, between and after hunks", () => {
		const rows = summarize(diffDisplayRows(parseUnifiedDiff(twoHunks), fileLines));
		expect(rows[0]).toBe("fold before:0 2");
		expect(rows).toContain("fold before:1 14");
		expect(rows[rows.length - 1]).toBe("fold after 8");
	});

	it("reveals hidden lines next to the hunk, with matching old and new numbers", () => {
		const rows = diffDisplayRows(parseUnifiedDiff(twoHunks), fileLines, { "before:1": 3, after: 2 });
		const text = summarize(rows);
		expect(text).toContain("fold before:1 11");
		const revealed = rows.filter((row) => row.type === "line" && row.key.startsWith("x:"));
		expect(revealed.map((row) => row.type === "line" && [row.row.oldNo, row.row.newNo, row.row.text])).toEqual([
			[17, 17, "line 17"], [18, 18, "line 18"], [19, 19, "line 19"],
			// After an insertion, old numbers trail new ones by one.
			[22, 23, "line 23"], [23, 24, "line 24"],
		]);
		expect(text[text.length - 1]).toBe("fold after 6");
	});

	it("removes a fold once everything in it is shown", () => {
		const rows = summarize(diffDisplayRows(parseUnifiedDiff(twoHunks), fileLines, { "before:0": 99 }));
		expect(rows.slice(0, 2)).toEqual(["context 1/1", "context 2/2"]);
	});

	it("keeps folds closed without the full file, and drops the unknown tail", () => {
		const rows = summarize(diffDisplayRows(parseUnifiedDiff(twoHunks), null, { "before:1": 5 }));
		expect(rows).toContain("fold before:1 14 locked");
		expect(rows.some((row) => row.startsWith("fold after"))).toBe(false);
	});

	it("shows an added file as one run of additions with nothing folded", () => {
		const diff = "@@ -0,0 +1,2 @@\n+a\n+b";
		expect(summarize(diffDisplayRows(parseUnifiedDiff(diff), ["a", "b"]))).toEqual(["add -/1", "add -/2"]);
	});
});

describe("selection → feedback", () => {
	const rows = diffDisplayRows(parseUnifiedDiff(twoHunks), fileLines);
	const firstAdd = rows.findIndex((row) => row.type === "line" && row.row.kind === "add");

	it("labels single lines, ranges and removed-only selections", () => {
		expect(selectionLabel(selectedLines(rows, firstAdd, firstAdd))).toBe("Line 4");
		expect(selectionLabel(selectedLines(rows, firstAdd + 1, firstAdd - 1))).toBe("Lines 4–5");
		expect(selectionLabel(selectedLines(rows, firstAdd - 1, firstAdd - 1))).toBe("Removed line 4");
		// A replacement pair followed by context spans the new side, not the first and last rows.
		expect(selectionLabel(selectedLines(rows, firstAdd - 1, firstAdd + 1))).toBe("Lines 4–5");
	});

	it("skips fold rows inside a range", () => {
		expect(selectedLines(rows, 0, 2).every((row) => typeof row.text === "string")).toBe(true);
	});

	it("builds the same annotation desktop sends for one line", () => {
		expect(annotationTarget({ path: "f.ts", workspaceVersion: "v1", fileFingerprint: "" }, selectedLines(rows, firstAdd, firstAdd))).toEqual({
			path: "f.ts", previousPath: undefined, side: "new", workspaceVersion: "v1", fileFingerprint: undefined,
			line: 4, oldLine: undefined, newLine: 4, lineKind: "add", lineText: "FOUR",
		});
	});

	it("quotes a range and points at its first line", () => {
		const target = annotationTarget({ path: "f.ts" }, selectedLines(rows, firstAdd - 1, firstAdd));
		expect(target).toMatchObject({ side: "new", line: 4, oldLine: 4 });
		expect(target.quote).toBe("Line 4 (before and after):\n- four\n+ FOUR");
	});

	it("comments on removed lines against the old side", () => {
		expect(annotationTarget({ path: "f.ts" }, selectedLines(rows, firstAdd - 1, firstAdd - 1))).toMatchObject({ side: "old", line: 4, lineKind: "del" });
	});
});

describe("file rows", () => {
	it("numbers every line and drops the final newline", () => {
		const lines = contentLines("a\r\nb\n", false);
		expect(lines).toEqual(["a", "b"]);
		expect(summarize(fileDisplayRows(lines ?? []))).toEqual(["context 1/1", "context 2/2"]);
		expect(contentLines("a", true)).toBeNull();
	});
});

describe("inline comments", () => {
	const rows = diffDisplayRows(parseUnifiedDiff(twoHunks), fileLines);
	const firstAdd = rows.findIndex((row) => row.type === "line" && row.row.kind === "add");

	it("anchors a comment under the last selected line, on the side it shows", () => {
		expect(commentAnchor(selectedLines(rows, firstAdd - 1, firstAdd + 1))).toEqual({ side: "new", line: 5 });
		expect(commentAnchor(selectedLines(rows, firstAdd - 1, firstAdd - 1))).toEqual({ side: "old", line: 4 });
		expect(commentAnchor([])).toBeUndefined();
	});

	it("draws comments under their lines, in order, and skips ones whose line is hidden", () => {
		const out = summarize(withInlineComments(rows, [
			{ id: "a", anchor: { side: "new", line: 4 } },
			{ id: "b", anchor: { side: "old", line: 4 } },
			{ id: "c", anchor: { side: "new", line: 4 } },
			{ id: "folded", anchor: { side: "new", line: 12 } },
		]));
		const at = (label: string) => out.indexOf(label);
		expect(at("comment b")).toBe(at("del 4/-") + 1);
		expect(at("comment a")).toBe(at("add -/4") + 1);
		expect(at("comment c")).toBe(at("comment a") + 1);
		expect(out).not.toContain("comment folded");
	});

	it("draws a context line's comment once, though it has both numbers", () => {
		const out = summarize(withInlineComments(rows, [{ id: "ctx", anchor: { side: "new", line: 5 } }]));
		expect(out.filter((row) => row === "comment ctx")).toHaveLength(1);
	});
});
