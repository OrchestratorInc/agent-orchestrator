import { describe, expect, it } from "vitest";
import { clampPreviewText, isMarkdownPreviewPath, parseMarkdownFrontmatter } from "./markdown-frontmatter";

const file = (body: string) => `---\n${body}\n---\n# Heading\n`;

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
		expect(isMarkdownPreviewPath("docs/notes.txt")).toBe(false);
		expect(isMarkdownPreviewPath("docs/page.html")).toBe(false);
	});
});
