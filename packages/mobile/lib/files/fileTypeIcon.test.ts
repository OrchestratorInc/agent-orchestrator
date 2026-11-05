import { describe, expect, it } from "vitest";
import { fileIconSvg, folderIconSvg } from "./fileTypeIcon";
import { FILE_ICON_FALLBACK, FILE_ICON_SVGS, FOLDER_ICON_FALLBACK } from "./fileTypeIcons.generated";

const fallback = FILE_ICON_SVGS[FILE_ICON_FALLBACK];

describe("file type icons", () => {
	it("gives common languages their own logo, not the plain page", () => {
		for (const path of ["src/a.ts", "x/b.tsx", "main.py", "cmd/main.go", "lib.rs", "App.swift", "style.css", "README.md", "data.json"]) {
			expect(fileIconSvg(path), path).not.toBe(fallback);
		}
	});

	it("tells TypeScript, Python and Go apart", () => {
		const icons = new Set(["a.ts", "a.py", "a.go"].map(fileIconSvg));
		expect(icons.size).toBe(3);
	});

	it("prefers an exact file name, then a two-part extension, then the extension", () => {
		expect(fileIconSvg("web/package.json")).not.toBe(fileIconSvg("web/other.json"));
		expect(fileIconSvg("a.test.ts")).not.toBe(fileIconSvg("a.ts"));
		expect(fileIconSvg("Dockerfile")).toBe(fileIconSvg("docker/dockerfile"));
	});

	it("falls back to a plain page or folder", () => {
		expect(fileIconSvg("notes.unknownext")).toBe(fallback);
		expect(fileIconSvg("LICENSE.weird.unknownext")).toBe(fallback);
		expect(folderIconSvg("some-folder")).toBe(FILE_ICON_SVGS[FOLDER_ICON_FALLBACK]);
		expect(folderIconSvg("src")).not.toBe(FILE_ICON_SVGS[FOLDER_ICON_FALLBACK]);
	});

	it("only ships SVG markup", () => {
		for (const svg of FILE_ICON_SVGS) expect(svg.startsWith("<svg")).toBe(true);
	});
});
