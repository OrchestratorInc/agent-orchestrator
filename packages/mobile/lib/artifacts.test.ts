import { describe, expect, it } from "vitest";
import { formatFileAnnotationMessage } from "../../shared/files/file-annotations";
import {
	artifactCacheEntry,
	artifactCacheEvictions,
	artifactCommentTarget,
	artifactExtensionLabel,
	artifactFormatLabel,
	contentRangeTotal,
	OPEN_CLOUD_FILE_HINT,
	openOnMachineHint,
	artifactFilePath,
	artifactMenuActions,
	artifactPageAt,
	artifactViewerPages,
	artifactFilesPath,
	artifactViewKind,
	resolveArtifactAssetPath,
	canCopyArtifact,
	canShowArtifactHistory,
	formatArtifactSize,
	isTextArtifact,
	MAX_SHOWN_TEXT_CHARS,
	shownText,
	looksBinary,
	parseArtifacts,
	sortArtifacts,
	stableArtifactOrder,
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

	it("keeps the paths a renamed file had, so a turn that reported one still finds it", () => {
		expect(parseArtifacts([
			{ path: "final.md", previousPaths: ["draft.md", 4, "notes/draft.md"] },
			{ path: "fresh.md" },
		]).map(({ path, previousPaths }) => ({ path, previousPaths }))).toEqual([
			{ path: "final.md", previousPaths: ["draft.md", "notes/draft.md"] },
			{ path: "fresh.md", previousPaths: undefined },
		]);
	});

	it("keeps a published file's http(s) link and drops anything else", () => {
		const link = "https://orchestrator.inc/s/3f2a9c";
		expect(parseArtifacts([
			{ path: "report.html", kind: "html", shareUrl: link },
			{ path: "local.html", kind: "html", shareUrl: "http://localhost:8787/s/abc" },
			{ path: "script.html", kind: "html", shareUrl: "javascript:alert(1)" },
			{ path: "relative.html", kind: "html", shareUrl: "/s/abc" },
			{ path: "long.html", kind: "html", shareUrl: `https://orchestrator.inc/s/${"a".repeat(600)}` },
			{ path: "empty.html", kind: "html", shareUrl: "" },
		]).map(({ path, shareUrl }) => ({ path, shareUrl }))).toEqual([
			{ path: "report.html", shareUrl: link },
			{ path: "local.html", shareUrl: "http://localhost:8787/s/abc" },
			{ path: "script.html", shareUrl: undefined },
			{ path: "relative.html", shareUrl: undefined },
			{ path: "long.html", shareUrl: undefined },
			{ path: "empty.html", shareUrl: undefined },
		]);
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
		expect(artifactViewKind({ path: "logo.svg", kind: "file" })).toBe("svg");
		expect(artifactViewKind({ path: "demo.mp4", kind: "file" })).toBe("video");
		expect(artifactViewKind({ path: "data.csv", kind: "file" })).toBe("table");
		expect(artifactViewKind({ path: "src/app.tsx", kind: "file" })).toBe("text");
	});

	it("previews the formats the desktop previews", () => {
		for (const path of ["a.apng", "a.avif", "a.bmp", "a.gif", "a.ico", "a.jpg", "a.JPEG", "a.png", "a.webp"]) expect(artifactViewKind({ path, kind: "file" })).toBe("image");
		for (const path of ["clip.m4v", "clip.mov", "clip.MP4", "clip.ogv", "clip.webm"]) expect(artifactViewKind({ path, kind: "file" })).toBe("video");
		for (const path of ["a.aac", "a.flac", "a.m4a", "a.mp3", "a.oga", "a.ogg", "a.opus", "a.wav"]) expect(artifactViewKind({ path, kind: "file" })).toBe("audio");
		expect(artifactViewKind({ path: "spec.PDF", kind: "file" })).toBe("pdf");
		expect(artifactViewKind({ path: "rows.tsv", kind: "file" })).toBe("table");
		expect(artifactViewKind({ path: "flow.mmd", kind: "file" })).toBe("diagram");
		expect(artifactViewKind({ path: "flow.mermaid", kind: "file" })).toBe("diagram");
	});

	it("draws an iPhone photo, which the phone decodes and the desktop does not", () => {
		expect(artifactViewKind({ path: "IMG_0001.HEIC", kind: "file" })).toBe("image");
		expect(artifactViewKind({ path: "photo.heif", kind: "file" })).toBe("image");
	});

	it("says a known binary format has no preview instead of reading it", () => {
		for (const path of ["bundle.zip", "report.docx", "sheet.xlsx", "app.sqlite", "font.woff2"]) expect(artifactViewKind({ path, kind: "file" })).toBe("binary");
	});

	it("offers Copy only for files whose contents are text", () => {
		expect(canCopyArtifact({ path: "notes.md", kind: "markdown" })).toBe(true);
		expect(canCopyArtifact({ path: "index.html", kind: "html" })).toBe(true);
		expect(canCopyArtifact({ path: "chart.png", kind: "file" })).toBe(false);
		expect(canCopyArtifact({ path: "demo.mov", kind: "file" })).toBe(false);
	});

	it("takes any file as text unless its extension is a known binary format", () => {
		for (const path of ["data.csv", "src/app.tsx", "LICENSE", "deploy/.env", "analysis.ipynb", "config.jsonc", "api.http", "model.jl", "shader.glsl", ".env.local", "Dockerfile.dev"]) {
			expect(isTextArtifact({ path, kind: "file" })).toBe(true);
		}
		for (const path of ["bundle.zip", "report.docx", "sheet.xlsx", "app.sqlite", "data.parquet", "font.woff2"]) {
			expect(isTextArtifact({ path, kind: "file" })).toBe(false);
			expect(canCopyArtifact({ path, kind: "file" })).toBe(false);
			expect(canShowArtifactHistory({ path, kind: "file" })).toBe(false);
		}
	});

	it("takes an SVG, a table and a diagram as text, and media as not", () => {
		for (const path of ["logo.svg", "rows.csv", "rows.tsv", "flow.mmd"]) {
			expect(isTextArtifact({ path, kind: "file" })).toBe(true);
			expect(canCopyArtifact({ path, kind: "file" })).toBe(true);
		}
		for (const path of ["clip.mp4", "song.mp3", "spec.pdf", "IMG_1.heic"]) expect(isTextArtifact({ path, kind: "file" })).toBe(false);
	});
});

describe("artifactFormatLabel", () => {
	it("names the format and size of a file the viewer cannot draw", () => {
		expect(artifactFormatLabel({ path: "out/bundle.zip", size: 2.4 * 1024 * 1024 })).toBe("ZIP file · 2.4 MB");
		expect(artifactFormatLabel({ path: "LICENSE", size: 300 })).toBe("File · 300 B");
	});

	it("leaves out a size that is not known", () => {
		expect(artifactFormatLabel({ path: "clip.webm", size: 0 })).toBe("WEBM file");
	});
});

describe("contentRangeTotal", () => {
	it("reads the whole size from a range response", () => {
		expect(contentRangeTotal("bytes 0-0/4182")).toBe(4182);
		expect(contentRangeTotal("bytes 0-0/3145728 ")).toBe(3145728);
	});

	it("gives nothing when the size is not stated", () => {
		expect(contentRangeTotal("bytes 0-0/*")).toBeUndefined();
		expect(contentRangeTotal(null)).toBeUndefined();
		expect(contentRangeTotal("")).toBeUndefined();
	});
});

describe("openOnMachineHint", () => {
	it("names the desktop's Show in action for the machine's platform", () => {
		expect(openOnMachineHint("Studio Mac", "darwin")).toBe("Open it on Studio Mac with “Show\u00a0in\u00a0Finder” in the desktop app.");
		expect(openOnMachineHint("Build Box", "win32")).toBe("Open it on Build Box with “Show\u00a0in\u00a0File\u00a0Explorer” in the desktop app.");
		expect(openOnMachineHint("Server", "linux")).toBe("Open it on Server with “Show\u00a0in\u00a0File\u00a0Manager” in the desktop app.");
	});

	it("points to the artifact menu when the platform is not known", () => {
		expect(openOnMachineHint("Studio Mac", "")).toBe("Open it on Studio Mac from the artifact's ⋯ menu in the desktop app.");
	});

	it("names no machine for a Cloud session's file", () => {
		expect(OPEN_CLOUD_FILE_HINT).toBe("Open it from the session's artifacts in the desktop app.");
		expect(OPEN_CLOUD_FILE_HINT).not.toMatch(/machine|Finder/);
	});

	it("says the machine when its name is not known", () => {
		expect(openOnMachineHint(undefined, "darwin")).toBe("Open it on the machine with “Show\u00a0in\u00a0Finder” in the desktop app.");
		expect(openOnMachineHint("  ", undefined)).toBe("Open it on the machine from the artifact's ⋯ menu in the desktop app.");
	});
});

describe("artifactCacheEntry", () => {
	it("names one version of a file, and keeps its extension", () => {
		const entry = artifactCacheEntry("host.a", "ao-1", "docs/spec.pdf", "2026-11-10T10:00:00Z");
		expect(entry).toEqual(artifactCacheEntry("host.a", "ao-1", "docs/spec.pdf", "2026-11-10T10:00:00Z"));
		expect(entry.name.startsWith(`${entry.prefix}-`)).toBe(true);
		expect(entry.name).toMatch(/\.pdf$/);
	});

	it("shares its prefix with the file's other versions only", () => {
		const entry = artifactCacheEntry("host.a", "ao-1", "spec.pdf", "v1");
		const rewritten = artifactCacheEntry("host.a", "ao-1", "spec.pdf", "v2");
		expect(rewritten.prefix).toBe(entry.prefix);
		expect(rewritten.name).not.toBe(entry.name);
		expect(artifactCacheEntry("host.a", "ao-2", "spec.pdf", "v1").prefix).not.toBe(entry.prefix);
		expect(artifactCacheEntry("host.a", "ao-1", "other.pdf", "v1").prefix).not.toBe(entry.prefix);
	});

	it("names a file with 128 bits and a version with 64", () => {
		const entry = artifactCacheEntry("host.a", "ao-1", "docs/spec.pdf", "v1");
		expect(entry.prefix).toMatch(/^[0-9a-f]{32}$/);
		expect(entry.name).toMatch(/^[0-9a-f]{32}-[0-9a-f]{16}\.pdf$/);
	});

	it("gives near-identical paths unrelated names", () => {
		const prefixes = new Set(Array.from({ length: 2000 }, (_, index) => artifactCacheEntry("host.a", "ao-1", `out/report-${index}.pdf`, "v1").prefix));
		expect(prefixes.size).toBe(2000);
	});

	it("keeps the same session and path on two paired machines apart", () => {
		const onA = artifactCacheEntry("host.a", "standalone-1", "report.pdf", "2026-11-10T10:00:00Z");
		const onB = artifactCacheEntry("host.b", "standalone-1", "report.pdf", "2026-11-10T10:00:00Z");
		expect(onB.prefix).not.toBe(onA.prefix);
		expect(onB.name).not.toBe(onA.name);
		// Downloading B's copy never evicts A's.
		expect(artifactCacheEvictions([{ name: onA.name, size: 10, modified: 1 }, { name: onB.name, size: 10, modified: 2 }], onB, () => false)).toEqual([]);
	});
});

describe("artifactCacheEvictions", () => {
	const v1 = artifactCacheEntry("host.a", "ao-1", "spec.pdf", "v1");
	const v2 = artifactCacheEntry("host.a", "ao-1", "spec.pdf", "v2");
	const other = artifactCacheEntry("host.a", "ao-1", "other.pdf", "v1");
	const file = (name: string, size = 10, modified = 1) => ({ name, size, modified });
	const idle = () => false;

	it("drops a file's older copies once a new version is in place", () => {
		expect(artifactCacheEvictions([file(v1.name), file(v2.name), file(other.name)], v2, idle)).toEqual([v1.name]);
	});

	it("drops an interrupted download of an older version", () => {
		expect(artifactCacheEvictions([file(`${v1.name}.part`), file(v2.name)], v2, idle)).toEqual([`${v1.name}.part`]);
	});

	it("keeps a download still in progress", () => {
		expect(artifactCacheEvictions([file(`${v1.name}.part`), file(v2.name)], v2, (name) => name === v1.name)).toEqual([]);
	});

	it("keeps one copy of an unlisted file opened again and again", () => {
		const opens = ["2026-11-10T10:00:00Z", "2026-11-10T10:05:00Z", "2026-11-10T10:09:00Z"].map((openedAt) => artifactCacheEntry("host.a", "ao-1", "report/spec.pdf", openedAt));
		const latest = opens[opens.length - 1];
		expect(artifactCacheEvictions(opens.map((entry) => file(entry.name)), latest, idle).sort()).toEqual(opens.slice(0, -1).map((entry) => entry.name).sort());
	});

	it("drops the least recently written other files while over the cap", () => {
		const a = artifactCacheEntry("host.a", "ao-1", "a.pdf", "v1");
		const b = artifactCacheEntry("host.a", "ao-1", "b.pdf", "v1");
		const files = [file(a.name, 60, 1), file(b.name, 60, 2), file(v2.name, 60, 3)];
		expect(artifactCacheEvictions(files, v2, idle, 130)).toEqual([a.name]);
		expect(artifactCacheEvictions(files, v2, idle, 200)).toEqual([]);
	});

	it("never drops the copy just downloaded, even alone over the cap", () => {
		expect(artifactCacheEvictions([file(v2.name, 500)], v2, idle, 100)).toEqual([]);
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

	it("offers Share link and Copy link for a published file, in the gallery and the viewer", () => {
		const shareUrl = "https://orchestrator.inc/s/3f2a9c";
		expect(artifactMenuActions({ path: "report.html", kind: "html", size: 10, shareUrl })).toEqual(["open", "share-link", "copy-link", "ask", "copy", "history", "delete"]);
		expect(artifactMenuActions({ path: "chart.png", kind: "file", size: 10, shareUrl }, { inViewer: true })).toEqual(["share-link", "copy-link", "ask", "delete"]);
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

describe("artifactPageAt", () => {
	const pages = ["a.png", "b.md", "c.html"].map((path) => ({ path }));

	it("picks the page a swipe settled on", () => {
		expect(artifactPageAt(pages, 0, 390)?.path).toBe("a.png");
		expect(artifactPageAt(pages, 390, 390)?.path).toBe("b.md");
		// A fractional offset (rounding on Android) still lands on the nearest page.
		expect(artifactPageAt(pages, 779.6, 390)?.path).toBe("c.html");
	});

	it("stays inside the list when the scroll overshoots", () => {
		expect(artifactPageAt(pages, -40, 390)?.path).toBe("a.png");
		expect(artifactPageAt(pages, 5_000, 390)?.path).toBe("c.html");
	});

	it("has no page before the pager is measured or when it is empty", () => {
		expect(artifactPageAt(pages, 390, 0)).toBeUndefined();
		expect(artifactPageAt([], 0, 390)).toBeUndefined();
	});
});

describe("stableArtifactOrder", () => {
	const files = (...paths: string[]) => paths.map((path) => ({ path, updatedAt: "" }));

	it("starts in the list's order", () => {
		expect(stableArtifactOrder(undefined, files("c", "b", "a")).map((f) => f.path)).toEqual(["c", "b", "a"]);
	});

	it("keeps a rewritten file's page when it moves to the front of the list", () => {
		const rewritten = { path: "a", updatedAt: "later" };
		const order = stableArtifactOrder(["c", "b", "a"], [rewritten, ...files("c", "b")]);
		expect(order.map((f) => f.path)).toEqual(["c", "b", "a"]);
		// The page carries the file's new version.
		expect(order[2]).toBe(rewritten);
	});

	it("puts a new file at the end and drops a removed one", () => {
		expect(stableArtifactOrder(["c", "b", "a"], files("new", "c", "a")).map((f) => f.path)).toEqual(["c", "a", "new"]);
	});
});

describe("artifactViewerPages", () => {
	const file = (path: string, registered?: boolean) => ({ path, name: path, kind: "file", size: 1, updatedAt: "2026-11-07T00:00:00Z", registered });
	const files = [file("brand.html", true), file("deck.icon/icon.json", true), file("deck.icon/mark.png", true), file("draft.md"), file("assets/a.png")];
	const paths = (pages: readonly { path: string }[]) => pages.map((page) => page.path);

	it("pages through what the list shows, with a bundle as one page", () => {
		expect(paths(artifactViewerPages(undefined, files, "brand.html"))).toEqual(["brand.html", "deck.icon/mark.png"]);
	});

	it("opens a hidden file on its own, with no way to swipe to the others", () => {
		expect(paths(artifactViewerPages(undefined, files, "assets/a.png"))).toEqual(["assets/a.png"]);
	});

	it("keeps the root fallback when nothing is registered", () => {
		const unregistered = [file("report.html"), file("assets/a.png")];
		expect(paths(artifactViewerPages(undefined, unregistered, "report.html"))).toEqual(["report.html"]);
	});

	it("keeps the order the pages had on open", () => {
		const reordered = [file("second.md", true), file("first.md", true)];
		expect(paths(artifactViewerPages(["first.md", "second.md"], reordered, "first.md"))).toEqual(["first.md", "second.md"]);
	});
});
