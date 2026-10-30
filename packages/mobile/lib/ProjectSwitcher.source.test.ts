import { existsSync, readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";

const path = (relative: string) => fileURLToPath(new URL(relative, import.meta.url));
const source = readFileSync(path("./ProjectSwitcher.tsx"), "utf8");
const layout = readFileSync(path("../app/_layout.tsx"), "utf8");

describe("project picker", () => {
	// Picking one value from a short list is a pull-down menu on both platforms,
	// not a half-height sheet of rows.
	it("is the system pull-down menu, not a sheet", () => {
		expect(source).toContain("<MenuView");
		expect(source).not.toContain("projectSheetRoute");
		expect(source).not.toContain("router.push");
		expect(existsSync(path("../app/sheets/project.tsx"))).toBe(false);
		expect(existsSync(path("./ProjectPickerSheet.tsx"))).toBe(false);
		expect(layout).not.toContain('"sheets/project"');
	});

	it("labels the row in sentence case", () => {
		expect(source).toMatch(/>\s*Project\s*</);
		expect(source).not.toMatch(/>\s*PROJECTS\s*</);
		expect(source).not.toContain("letterSpacing");
	});
});
