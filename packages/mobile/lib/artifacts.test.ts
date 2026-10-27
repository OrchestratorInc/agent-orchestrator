import { describe, expect, it } from "vitest";
import {
	artifactExtensionLabel,
	artifactFilePath,
	artifactMenuActions,
	artifactViewKind,
	canCopyArtifact,
	formatArtifactSize,
	looksBinary,
	parseArtifacts,
	sortArtifacts,
} from "./artifacts";

describe("parseArtifacts", () => {
	it("keeps well-formed files and normalizes their fields", () => {
		expect(parseArtifacts([
			{ path: "report.md", name: "report.md", kind: "markdown", size: 120, updatedAt: "2026-10-28T10:00:00Z", rawUrl: "http://ao-preview-artifact.x.localhost/report.md" },
			{ path: "out/data.csv", kind: "weird", size: -4 },
		])).toEqual([
			{ path: "report.md", name: "report.md", kind: "markdown", size: 120, updatedAt: "2026-10-28T10:00:00Z" },
			{ path: "out/data.csv", name: "data.csv", kind: "file", size: 0, updatedAt: "" },
		]);
	});

	it("drops anything that could leave the artifact directory", () => {
		expect(parseArtifacts([
			{ path: "../secret" },
			{ path: "/etc/passwd" },
			{ path: "a\\b" },
			{ path: "a//b" },
			{ path: "" },
			null,
			"report.md",
		])).toEqual([]);
		expect(parseArtifacts(undefined)).toEqual([]);
	});
});

describe("sortArtifacts", () => {
	it("puts the newest first and breaks ties by path", () => {
		const sorted = sortArtifacts([
			{ path: "b.md", name: "b.md", kind: "markdown", size: 1, updatedAt: "2026-10-28T10:00:00Z" },
			{ path: "c.md", name: "c.md", kind: "markdown", size: 1, updatedAt: "2026-10-28T12:00:00Z" },
			{ path: "a.md", name: "a.md", kind: "markdown", size: 1, updatedAt: "2026-10-28T10:00:00Z" },
			{ path: "z.md", name: "z.md", kind: "markdown", size: 1, updatedAt: "" },
		]);
		expect(sorted.map((artifact) => artifact.path)).toEqual(["c.md", "a.md", "b.md", "z.md"]);
	});
});

describe("artifactViewKind", () => {
	it("draws each kind of file with the viewer that can show it", () => {
		expect(artifactViewKind({ path: "index.html", kind: "html" })).toBe("html");
		expect(artifactViewKind({ path: "notes.md", kind: "markdown" })).toBe("markdown");
		expect(artifactViewKind({ path: "README.markdown", kind: "file" })).toBe("markdown");
		expect(artifactViewKind({ path: "chart.PNG", kind: "file" })).toBe("image");
		expect(artifactViewKind({ path: "logo.svg", kind: "file" })).toBe("web-media");
		expect(artifactViewKind({ path: "demo.mp4", kind: "file" })).toBe("web-media");
		expect(artifactViewKind({ path: "data.csv", kind: "file" })).toBe("text");
	});

	it("offers Copy only for files whose contents are text", () => {
		expect(canCopyArtifact({ path: "notes.md", kind: "markdown" })).toBe(true);
		expect(canCopyArtifact({ path: "index.html", kind: "html" })).toBe(true);
		expect(canCopyArtifact({ path: "chart.png", kind: "file" })).toBe(false);
		expect(canCopyArtifact({ path: "demo.mov", kind: "file" })).toBe(false);
	});
});

describe("artifactFilePath", () => {
	it("routes through the artifact prefix so a page's relative links stay in the artifact directory", () => {
		expect(artifactFilePath("ao-1", "site/my page.html")).toBe("/api/v1/sessions/ao-1/preview/files/__ao_artifacts__/site/my%20page.html");
	});

	it("asks for a markdown file's source rather than the page the daemon renders from it", () => {
		expect(artifactFilePath("ao-1", "plan.md", { raw: true })).toBe("/api/v1/sessions/ao-1/preview/files/__ao_artifacts__/plan.md?raw=true");
	});

	it("refuses a path that could escape", () => {
		expect(artifactFilePath("ao-1", "../x")).toBeUndefined();
		expect(artifactFilePath("ao-1", "/x")).toBeUndefined();
	});
});

describe("looksBinary", () => {
	it("tells undecodable bytes from text", () => {
		expect(looksBinary("hello\nworld")).toBe(false);
		expect(looksBinary("PNG\u0000\u0001")).toBe(true);
		expect(looksBinary("���ab")).toBe(true);
		expect(looksBinary("")).toBe(false);
	});
});

describe("labels", () => {
	it("formats sizes and extensions for a card", () => {
		expect(formatArtifactSize(12)).toBe("12 B");
		expect(formatArtifactSize(340 * 1024)).toBe("340 KB");
		expect(formatArtifactSize(1.25 * 1024 * 1024)).toBe("1.3 MB");
		expect(artifactExtensionLabel("data.csv")).toBe("CSV");
		expect(artifactExtensionLabel("Makefile")).toBe("FILE");
	});
});

describe("artifactMenuActions", () => {
	it("offers Open, Copy for text, and Delete last", () => {
		expect(artifactMenuActions({ path: "notes.md", kind: "markdown", size: 10 })).toEqual(["open", "copy", "delete"]);
		expect(artifactMenuActions({ path: "chart.png", kind: "file", size: 10 })).toEqual(["open", "delete"]);
	});

	it("leaves out Open in the viewer and Copy for text too large to load", () => {
		expect(artifactMenuActions({ path: "notes.md", kind: "markdown", size: 10 }, { inViewer: true })).toEqual(["copy", "delete"]);
		expect(artifactMenuActions({ path: "dump.txt", kind: "file", size: 5 << 20 })).toEqual(["open", "delete"]);
	});
});
