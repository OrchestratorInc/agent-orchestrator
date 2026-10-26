import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";

const source = readFileSync(fileURLToPath(new URL("./spawn-composer-controls.ios.tsx", import.meta.url)), "utf8");

describe("iOS spawn menu layout", () => {
	// Fixed widths truncated longer names and fixed heights clipped the rail at
	// large text sizes; the host now takes the height its content asks for.
	it("lets the selectors size to their content", () => {
		expect(source).not.toMatch(/MENU_WIDTH/);
		expect(source).not.toMatch(/frame\(\{ height:/);
		expect(source).not.toMatch(/height: \d+/);
		expect(source).toContain("<Host matchContents={{ vertical: true }}");
		expect(source).toContain("<Spacer minLength={0} />");
		expect(source).toContain('frame({ maxWidth: FILL_WIDTH, alignment: "leading" })');
		// The agent's name keeps its natural width; only the model fills.
		expect(source).toContain('layoutPriority(1), tint(t.textPrimary), accessibilityIdentifier("spawn-harness")');
		expect(source.match(/layoutPriority\(/g)).toHaveLength(1);
		for (const id of ["spawn-host", "spawn-project", "spawn-attachment", "spawn-harness", "spawn-model"]) {
			expect(source).toContain(`accessibilityIdentifier("${id}")`);
		}
	});

	it("scales every label with Dynamic Type", () => {
		expect(source).not.toMatch(/font\(\{ size/);
		expect(source).toContain('font({ textStyle: "subheadline", weight: "medium" })');
	});

	// Checkmark images on plain buttons drew a selection VoiceOver never heard.
	it("checks the current choice with the system's checkable menu item", () => {
		expect(source).not.toContain('"checkmark"');
		expect(source).toContain("<Toggle isOn={selected} onIsOnChange={onIsOnChange}");
	});

	it("separates standalone from the project rows", () => {
		expect(source).toContain('menuOrder("fixed")');
		expect(source).toContain("project.sectionBreakBefore ? <Divider");
	});

	it("uses the standalone agent mark in the trigger and project rows", () => {
		expect(source).toContain('project?.icon === "message-square-plus" ? "plus.bubble" : "folder"');
		expect(source).toContain("name={projectSystemImage(selectedProject)}");
		expect(source).toContain("systemImage={projectSystemImage(project)}");
	});

	it("lets the attach button choose a photo or a file", () => {
		expect(source).toContain('systemImage="paperclip"');
		expect(source).toContain('<Button label="Choose Photo" systemImage="photo"');
		expect(source).toContain('<Button label="Choose File" systemImage="doc"');
	});

	// Liquid Glass's prominent style falls back to a plain text button before
	// iOS 26, so the filled style stands in there.
	it("starts from a native prominent button rather than a scaling Pressable", () => {
		expect(source).not.toContain("Pressable");
		expect(source).toContain('parseInt(String(Platform.Version), 10) >= 26 ? "glassProminent" : "borderedProminent"');
		expect(source).toContain("buttonStyle(startStyle)");
		expect(source).toContain("disabledModifier(disabled)");
	});
});
