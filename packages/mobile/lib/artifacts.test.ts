import { describe, expect, it } from "vitest";
import { formatFileAnnotationMessage } from "../../shared/files/file-annotations";
import {
	artifactCommentTarget,
	artifactExtensionLabel,
	artifactFilePath,
	artifactMenuActions,
	artifactFilesPath,
	artifactViewKind,
	resolveArtifactAssetPath,
	canCopyArtifact,
	canShowArtifactHistory,
	formatArtifactSize,
	isTextArtifact,
	MAX_SHOWN_TEXT_CHARS,
	shownText,
	showsInWebView,
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

	it("takes a file as text by its extension, and an unknown extension as binary", () => {
		expect(isTextArtifact({ path: "data.csv", kind: "file" })).toBe(true);
		expect(isTextArtifact({ path: "src/app.tsx", kind: "file" })).toBe(true);
		expect(isTextArtifact({ path: "LICENSE", kind: "file" })).toBe(true);
		expect(isTextArtifact({ path: "deploy/.env", kind: "file" })).toBe(true);
		for (const path of ["bundle.zip", "report.docx", "sheet.xlsx", "app.sqlite"]) {
			expect(isTextArtifact({ path, kind: "file" })).toBe(false);
			expect(canCopyArtifact({ path, kind: "file" })).toBe(false);
			expect(canShowArtifactHistory({ path, kind: "file" })).toBe(false);
		}
	});

	it("does not draw a PDF in an Android WebView, which has no PDF renderer", () => {
		expect(showsInWebView("spec.pdf", "android")).toBe(false);
		expect(showsInWebView("spec.PDF", "ios")).toBe(true);
		expect(showsInWebView("demo.mp4", "android")).toBe(true);
	});
});

describe("shownText", () => {
	it("keeps a short text whole", () => {
		expect(shownText("a\nb")).toEqual({ text: "a\nb", truncated: false });
	});

	it("cuts a long text at the last line break before the cap", () => {
		const line = `${"x".repeat(99)}\n`;
		const long = line.repeat(Math.ceil(MAX_SHOWN_TEXT_CHARS / line.length) + 10);
		const shown = shownText(long);
		expect(shown.truncated).toBe(true);
		expect(shown.text.length).toBeLessThanOrEqual(MAX_SHOWN_TEXT_CHARS);
		expect(shown.text.endsWith("x")).toBe(true);
	});

	it("cuts a long line at the cap", () => {
		expect(shownText("x".repeat(20), 10)).toEqual({ text: "x".repeat(10), truncated: true });
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
	it("offers Open, Ask, Copy and History for text, and Delete last", () => {
		expect(artifactMenuActions({ path: "notes.md", kind: "markdown", size: 10 })).toEqual(["open", "ask", "copy", "history", "delete"]);
		expect(artifactMenuActions({ path: "chart.png", kind: "file", size: 10 })).toEqual(["open", "ask", "delete"]);
		expect(artifactMenuActions({ path: "bundle.zip", kind: "file", size: 10 })).toEqual(["open", "ask", "delete"]);
	});

	it("leaves out Open in the viewer and Copy for text too large to load, but keeps Ask and History", () => {
		expect(artifactMenuActions({ path: "notes.md", kind: "markdown", size: 10 }, { inViewer: true })).toEqual(["ask", "copy", "history", "delete"]);
		expect(artifactMenuActions({ path: "dump.txt", kind: "file", size: 5 << 20 })).toEqual(["open", "ask", "history", "delete"]);
	});
});

describe("artifactCommentTarget", () => {
	it("tells the agent which artifact, and quotes the selected text", () => {
		const target = artifactCommentTarget({ path: "plans/plan.md", name: "plan.md" }, "  Leases are kept in memory  ");
		expect(target).toEqual({ source: "plan.md", path: "plans/plan.md", side: "file", quote: "Leases are kept in memory" });
		const message = formatFileAnnotationMessage(target, "Persist them instead.");
		expect(message).toContain("- Source: plan.md");
		expect(message).toContain("- Location: Selected text");
		expect(message).toContain("- Selected text: Leases are kept in memory");
	});

	it("is about the whole file without a selection", () => {
		const target = artifactCommentTarget({ path: "report.html", name: "report.html" });
		expect(target).toEqual({ source: "report.html", path: "report.html", side: "file" });
		expect(formatFileAnnotationMessage(target, "Add a summary.")).toContain("- Location: Entire file");
	});
});

describe("resolveArtifactAssetPath", () => {
	it("resolves a document's relative links inside the artifact folder", () => {
		expect(resolveArtifactAssetPath("plan.md", "shots/01.png")).toBe("shots/01.png");
		expect(resolveArtifactAssetPath("docs/plan.md", "./img/a%20b.png?raw=1#top")).toBe("docs/img/a b.png");
		expect(resolveArtifactAssetPath("docs/plan.md", "../shots/a.png")).toBe("shots/a.png");
	});

	it("leaves URLs and rooted paths alone, and refuses to leave the folder", () => {
		expect(resolveArtifactAssetPath("plan.md", "https://example.com/a.png")).toBeUndefined();
		expect(resolveArtifactAssetPath("plan.md", "data:image/png;base64,AAAA")).toBeUndefined();
		expect(resolveArtifactAssetPath("plan.md", "/etc/a.png")).toBeUndefined();
		expect(resolveArtifactAssetPath("plan.md", "../a.png")).toBeUndefined();
		expect(resolveArtifactAssetPath("plan.md", "#section")).toBeUndefined();
	});
});

describe("artifactFilesPath", () => {
	it("names the route that serves an HTML artifact with AO's theme written in", () => {
		expect(artifactFilesPath("ao-1", "deck/my page.html")).toBe("/api/v1/sessions/ao-1/artifact-files/deck/my%20page.html");
		expect(artifactFilesPath("ao-1", "../x.html")).toBeUndefined();
	});
});
