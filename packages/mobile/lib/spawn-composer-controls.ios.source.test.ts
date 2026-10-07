import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";

const source = readFileSync(fileURLToPath(new URL("./spawn-composer-controls.ios.tsx", import.meta.url)), "utf8");

describe("iOS spawn menu layout", () => {
	it("keeps the project trigger compact and gives the harness enough label room", () => {
		expect(source).toContain("const HARNESS_MENU_WIDTH = 124");
		expect(source).toContain('frame({ width: HARNESS_MENU_WIDTH })');
		expect(source).toContain('const PROJECT_MENU_WIDTH = 224');
		expect(source).toContain('{projectLabel}</Text>\n\t\t\t\t\t\t\t<Image systemName="chevron.up.chevron.down"');
		expect(source).toContain('padding({ horizontal: 4 }), frame({ width: PROJECT_MENU_WIDTH, alignment: "leading" })');
		expect(source).toContain('layoutPriority(1)');
		expect(source).toContain('accessibilityIdentifier("spawn-project")');
		expect(source).toContain('accessibilityIdentifier("spawn-model")');
	});

	it("separates standalone from the project rows", () => {
		expect(source).toContain("Button, Divider, Group");
		expect(source).toContain('menuOrder("fixed")');
		expect(source).toContain("project.sectionBreakBefore ? <Divider /> : null");
		expect(source).toContain("<Divider />");
	});

	it("uses the standalone agent logo in the trigger and project rows", () => {
		expect(source).toContain('project?.icon === "message-square-plus" ? "plus.bubble" : "folder"');
		expect(source).toContain("systemName={projectSystemImage(selectedProject)}");
		expect(source).toContain("systemName={projectSystemImage(project)}");
	});
});
