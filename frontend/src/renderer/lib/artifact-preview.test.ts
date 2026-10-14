import { describe, expect, it } from "vitest";
import { isArtifactPreviewUrl, withArtifactPreviewTheme } from "./artifact-preview";
import { renderThemeFragment } from "./render-frame";

const theme = { appearance: "light" as const, variables: { "--background": "#fff" } };

describe("artifact preview URLs", () => {
	it("recognizes only the artifact preview origin", () => {
		expect(isArtifactPreviewUrl("http://ao-preview-artifact.abc.localhost:3001/report.html")).toBe(true);
		expect(isArtifactPreviewUrl("http://ao-preview.abc.localhost:3001/index.html")).toBe(false);
		expect(isArtifactPreviewUrl("not a url")).toBe(false);
		expect(isArtifactPreviewUrl(undefined)).toBe(false);
	});

	it("hands an artifact page the theme in its fragment, replacing any fragment it had", () => {
		const page = "http://ao-preview-artifact.abc.localhost:3001/q3/report.html";
		const fragment = renderThemeFragment(theme, "fullscreen");
		expect(withArtifactPreviewTheme(page, theme)).toBe(`${page}${fragment}`);
		expect(withArtifactPreviewTheme(`${page}#ao-theme=stale`, theme)).toBe(`${page}${fragment}`);
	});

	it("leaves workspace previews and other pages as they are", () => {
		for (const url of ["http://ao-preview.abc.localhost:3001/index.html#top", "http://localhost:5173/", "file:///tmp/a.html"]) {
			expect(withArtifactPreviewTheme(url, theme)).toBe(url);
		}
	});
});
