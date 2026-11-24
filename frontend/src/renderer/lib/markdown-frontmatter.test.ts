import { describe, expect, it } from "vitest";
import { clampPreviewText, isMarkdownFilePath, isMarkdownPreviewPath, parseMarkdownFrontmatter, stripMarkdownFrontmatter, stripMarkdownPreviewAnchor } from "./markdown-frontmatter";

const file = (body: string) => `---\n${body}\n---\n# Heading\n`;

describe("stripMarkdownFrontmatter", () => {
	it("removes complete metadata with LF, CRLF, or a BOM", () => {
		expect(stripMarkdownFrontmatter(file("name: Guide"))).toBe("# Heading\n");
		expect(stripMarkdownFrontmatter("\uFEFF---\r\nname: Guide\r\n---\r\n# Heading")).toBe("# Heading");
		expect(stripMarkdownFrontmatter("---\n---\n# Heading")).toBe("# Heading");
	});

	it("preserves incomplete frontmatter and later horizontal rules", () => {
		expect(stripMarkdownFrontmatter("---\nname: Guide")).toBe("---\nname: Guide");
		expect(stripMarkdownFrontmatter("# Heading\n\n---\n\nBody")).toBe("# Heading\n\n---\n\nBody");
	});
});

describe("parseMarkdownFrontmatter", () => {
	it("reads name and description as plain text", () => {
		const meta = parseMarkdownFrontmatter(file("name: Deployment checklist\ndescription: Steps to verify a build before deployment."));
		expect(meta).toEqual({
			status: "ok",
			name: "Deployment checklist",
			description: "Steps to verify a build before deployment.",
		});
	});

	it("keeps markup characters as text", () => {
		const meta = parseMarkdownFrontmatter(file("name: \"<img src=x onerror=alert(1)>\"\ndescription: '**bold** <script>alert(1)</script>'"));
		expect(meta.name).toBe("<img src=x onerror=alert(1)>");
		expect(meta.description).toBe("**bold** <script>alert(1)</script>");
	});

	it("keeps a field when the other is missing", () => {
		expect(parseMarkdownFrontmatter(file("name: Only name")).description).toBeUndefined();
		expect(parseMarkdownFrontmatter(file("description: Only description")).name).toBeUndefined();
		expect(parseMarkdownFrontmatter(file("title: Ignored")).name).toBeUndefined();
	});

	it("reports a missing frontmatter block", () => {
		expect(parseMarkdownFrontmatter("# No frontmatter\n")).toEqual({ status: "absent" });
	});

	it("reports malformed frontmatter", () => {
		expect(parseMarkdownFrontmatter("---\nname: \"unterminated\n---\n").status).toBe("malformed");
		expect(parseMarkdownFrontmatter("---\nname: hi\n").status).toBe("malformed");
		expect(parseMarkdownFrontmatter("---\nname: [not, scalar]\n---\n").status).toBe("malformed");
	});

	it("reads a folded block description", () => {
		const meta = parseMarkdownFrontmatter(file("name: Checklist\ndescription: |\n  First line\n  Second line"));
		expect(meta.description).toBe("First line\nSecond line");
	});

	it("clamps long values", () => {
		const long = "x".repeat(400);
		expect(clampPreviewText(long)).toHaveLength(280);
		expect(clampPreviewText(long).endsWith("...")).toBe(true);
		expect(clampPreviewText(long).includes("y")).toBe(false);
	});

	it("accepts markdown extensions only", () => {
		expect(isMarkdownPreviewPath("docs/README.md")).toBe(true);
		expect(isMarkdownPreviewPath("docs/Guide.markdown")).toBe(true);
		expect(isMarkdownPreviewPath("docs/README.md?raw=true")).toBe(true);
		expect(isMarkdownPreviewPath("https://example.com/README.md")).toBe(true);
		expect(isMarkdownPreviewPath("https://example.com/README.md#heading")).toBe(true);
		expect(isMarkdownPreviewPath("docs/a#b.md")).toBe(true);
		expect(isMarkdownPreviewPath("docs/a#b.markdown")).toBe(true);
		expect(isMarkdownPreviewPath("docs/check?draft.md")).toBe(true);
		expect(isMarkdownPreviewPath("docs/a?b#c.md")).toBe(true);
		expect(isMarkdownPreviewPath("docs/notes.txt")).toBe(false);
		expect(isMarkdownPreviewPath("docs/page.html")).toBe(false);
	});

	it("strips anchors from markdown preview paths while preserving resolved filenames", () => {
		expect(stripMarkdownPreviewAnchor("docs/check.md")).toBe("docs/check.md");
		expect(stripMarkdownPreviewAnchor("docs/a#b.md")).toBe("docs/a#b.md");
		expect(stripMarkdownPreviewAnchor("docs/check?draft.md")).toBe("docs/check?draft.md");
		expect(stripMarkdownPreviewAnchor("docs/check.md#usage")).toBe("docs/check.md");
		expect(stripMarkdownPreviewAnchor("docs/check.md#L10-L20")).toBe("docs/check.md");
		expect(stripMarkdownPreviewAnchor("docs/a#b.md#usage")).toBe("docs/a#b.md");
		expect(stripMarkdownPreviewAnchor("docs/check?draft.md#usage")).toBe("docs/check?draft.md");
		expect(stripMarkdownPreviewAnchor("docs/guide.md?draft")).toBe("docs/guide.md?draft");
	});

	it("validates markdown file paths by extension", () => {
		expect(isMarkdownFilePath("docs/check.md")).toBe(true);
		expect(isMarkdownFilePath("docs/a#b.md")).toBe(true);
		expect(isMarkdownFilePath("docs/check?draft.md")).toBe(true);
		expect(isMarkdownFilePath("docs/guide.md?draft")).toBe(false);
		expect(isMarkdownFilePath("docs/notes.txt")).toBe(false);
	});
});
