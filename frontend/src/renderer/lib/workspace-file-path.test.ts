import { describe, expect, it } from "vitest";
import {
	explicitWorkspaceFilePath,
	createWorkspaceFileLookup,
	findWorkspaceFilePath,
	isAbsoluteFileReference,
	matchWorkspaceFilePath,
	workspaceFileOpenTarget,
	normalizeWorkspaceFileReference,
	workspaceFileReferenceLine,
} from "./workspace-file-path";

describe("workspace file lookup for a catalog", () => {
	it.each([
		["./src/a.ts", "src/a.ts"],
		["file:///repo/src/a.ts%3A42", "src/a.ts"],
		["C:\\repo\\src\\a.ts:9", "src/a.ts"],
		["a.ts#L12-L18", "src/a.ts"],
		["index.ts", undefined],
		["missing.ts", undefined],
		["https://example.com/src/a.ts", undefined],
	])("preserves repeated resolution of %s", (reference, expected) => {
		const find = createWorkspaceFileLookup(["src/a.ts", "frontend/index.ts", "backend/index.ts"]);
		expect(find(reference)).toBe(expected);
		expect(find(reference)).toBe(expected);
	});

	it("keeps catalogs independent when files appear, disappear, or become ambiguous", () => {
		const before = createWorkspaceFileLookup(["src/a.ts"]);
		expect(before("new.ts")).toBeUndefined();
		expect(before("a.ts")).toBe("src/a.ts");
		const after = createWorkspaceFileLookup(["src/new.ts", "src/a.ts", "other/a.ts"]);
		expect(after("new.ts")).toBe("src/new.ts");
		expect(after("a.ts")).toBeUndefined();
		expect(createWorkspaceFileLookup([])("a.ts")).toBeUndefined();
	});

	it("continues resolving after a long stream of distinct references", () => {
		const find = createWorkspaceFileLookup(["src/a.ts"]);
		expect(find("a.ts")).toBe("src/a.ts");
		for (let i = 0; i < 1100; i++) expect(find(`missing${i}.ts`)).toBeUndefined();
		expect(find("a.ts")).toBe("src/a.ts");
	});

	it("preserves exact precedence, suffix disambiguation, and duplicate ambiguity", () => {
		const find = createWorkspaceFileLookup([
			" ./src/index.ts ", "src/index.ts", "lib/index.ts", "src/only.ts", "copies/only.ts",
			"src/duplicate.ts", "src/duplicate.ts", "windows\\unique.ts", "notes/",
		]);
		expect(find("src/index.ts")).toBe(" ./src/index.ts ");
		expect(find("index.ts")).toBeUndefined();
		expect(find("/repo/src/only.ts:12")).toBe("src/only.ts");
		expect(find("elsewhere/only.ts")).toBeUndefined();
		expect(find("duplicate.ts")).toBeUndefined();
		expect(find("src/duplicate.ts")).toBe("src/duplicate.ts");
		expect(find("unique.ts")).toBe("windows\\unique.ts");
		expect(find("/repo/notes/")).toBe("notes/");
	});

	it("matches the linear resolver across path forms and catalog collisions", () => {
		const catalogs = [
			[], [""], ["/", ""], ["src/a.ts", "b.ts", "dir/", " spaced/file.ts "],
			["a.ts", "lib/a.ts", "./src/a.ts", "other\\a.ts", "./src/a.ts"],
			["C:\\repo\\src\\a.ts", "src/b.ts", "./percent%20name.ts", "docs/hello world.md"],
		];
		for (const paths of catalogs) {
			const find = createWorkspaceFileLookup(paths);
			const references = ["", "./", "/", "missing.ts", "https://host/a.ts", "//host/a.ts", "file://[", ...paths];
			for (const path of paths) {
				const normalized = normalizeWorkspaceFileReference(path);
				const base = normalized.split("/").at(-1) ?? "";
				references.push(normalized, base, `./${normalized}`, `/repo/${normalized}`, `${normalized}:12:3`,
					`${normalized}#L2-L9`, `file:///repo/${encodeURI(normalized)}`, `C:\\repo\\${normalized.replaceAll("/", "\\")}`);
			}
			for (const reference of references) {
				expect(find(reference), `${JSON.stringify(paths)}: ${reference}`).toBe(findWorkspaceFilePath(reference, paths));
			}
		}
	});
});

describe("matchWorkspaceFilePath", () => {
	const files = [
		{ path: "src/a.ts", status: "modified" as const, additions: 1, deletions: 0, binary: false, size: 12 },
		{ path: "docs/report.md", status: "added" as const, additions: 10, deletions: 0, binary: false, size: 40 },
	];

	it("matches an exact workspace path", () => {
		expect(matchWorkspaceFilePath("src/a.ts", files)).toBe("src/a.ts");
	});

	it("matches a basename from a turn diff", () => {
		expect(matchWorkspaceFilePath("report.md", files)).toBe("docs/report.md");
	});

	it("matches a suffix path", () => {
		expect(matchWorkspaceFilePath("a.ts", files)).toBe("src/a.ts");
	});

	it("normalizes leading ./", () => {
		expect(matchWorkspaceFilePath("./src/a.ts", files)).toBe("src/a.ts");
	});

	it("strips editor line locations before matching", () => {
		expect(matchWorkspaceFilePath("src/a.ts:42:7", files)).toBe("src/a.ts");
		expect(matchWorkspaceFilePath("docs/report.md#L12-L18", files)).toBe("docs/report.md");
	});

	it("resolves encoded absolute file urls against workspace files", () => {
		expect(
			matchWorkspaceFilePath("file:///Users/me/project/docs/report.md%3A42", files),
		).toBe("docs/report.md");
	});

	it("keeps an absolute reference as the open target instead of its file-list match", () => {
		expect(workspaceFileOpenTarget("/other-repo/src/a.ts:12", "src/a.ts")).toBe("/other-repo/src/a.ts");
		expect(workspaceFileOpenTarget("file:///other-repo/src/a.ts", "src/a.ts")).toBe("/other-repo/src/a.ts");
		expect(workspaceFileOpenTarget("C:\\repo\\src\\a.ts", "src/a.ts")).toBe("C:/repo/src/a.ts");
		expect(workspaceFileOpenTarget("a.ts:12", "src/a.ts")).toBe("src/a.ts");
		expect(workspaceFileOpenTarget("./src/a.ts", "src/a.ts")).toBe("src/a.ts");
	});

	it("recognizes absolute file references", () => {
		expect(isAbsoluteFileReference("/Users/me/repo/src/app.ts")).toBe(true);
		expect(isAbsoluteFileReference("~/notes/plan.md")).toBe(true);
		expect(isAbsoluteFileReference("C:/repo/src/app.ts")).toBe(true);
		expect(isAbsoluteFileReference("src/app.ts")).toBe(false);
		expect(isAbsoluteFileReference("./src/app.ts")).toBe(false);
	});

	it("falls back to the normalized request when nothing matches", () => {
		expect(matchWorkspaceFilePath("missing.txt", files)).toBe("missing.txt");
	});

	it("disambiguates duplicate basenames with a path suffix", () => {
		const duplicateFiles = [
			...files,
			{ path: "frontend/index.ts", status: "modified" as const, additions: 1, deletions: 0, binary: false, size: 12 },
			{ path: "backend/index.ts", status: "modified" as const, additions: 1, deletions: 0, binary: false, size: 12 },
		];
		expect(matchWorkspaceFilePath("frontend/index.ts", duplicateFiles)).toBe("frontend/index.ts");
		expect(matchWorkspaceFilePath("backend/index.ts", duplicateFiles)).toBe("backend/index.ts");
	});

	it("only resolves a basename when it is unambiguous", () => {
		expect(findWorkspaceFilePath("index.ts:10", ["frontend/index.ts", "backend/index.ts"])).toBeUndefined();
	});

	it("recognizes explicit local links without mistaking urls for files", () => {
		expect(explicitWorkspaceFilePath("/repo/src/new.ts:12")).toBe("/repo/src/new.ts");
		expect(explicitWorkspaceFilePath("docs/new.ts#L4")).toBe("docs/new.ts");
		expect(explicitWorkspaceFilePath("https://example.com/docs/new.ts")).toBeUndefined();
		expect(explicitWorkspaceFilePath("//example.com/docs/new.ts")).toBeUndefined();
		expect(explicitWorkspaceFilePath("mailto:dev@example.com")).toBeUndefined();
	});

	it("does not match a web url by its path suffix", () => {
		expect(findWorkspaceFilePath("https://example.com/src/a.ts", ["src/a.ts"])).toBeUndefined();
	});

	it("normalizes Windows file references", () => {
		expect(normalizeWorkspaceFileReference("C:\\repo\\src\\app.ts:9")).toBe("C:/repo/src/app.ts");
	});

	it("extracts editor line locations without confusing columns or ranges", () => {
		expect(workspaceFileReferenceLine("src/app.ts:42")).toBe(42);
		expect(workspaceFileReferenceLine("src/app.ts:42:7")).toBe(42);
		expect(workspaceFileReferenceLine("src/app.ts#L12-L18")).toBe(12);
		expect(workspaceFileReferenceLine("file:///repo/src/app.ts%3A27")).toBe(27);
		expect(workspaceFileReferenceLine("src/app.ts")).toBeUndefined();
	});
});
