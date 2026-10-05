import { describe, expect, it } from "vitest";
import { readFileSync } from "node:fs";

describe("combined sidebar", () => {
	it("does not offer a global environment switch in either drawer", () => {
		for (const file of ["sidebar-navigation-shell.tsx", "sidebar-navigation-shell.android.tsx"]) {
			const source = readFileSync(new URL(`./${file}`, import.meta.url), "utf8");
			expect(source).not.toContain("SidebarEnvironmentPicker");
		}
	});
});
