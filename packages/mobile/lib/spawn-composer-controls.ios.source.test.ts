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
		// The agent and its model are one enclosed capsule, the circles' height,
		// holding the harness's mark and one name.
		expect(source).toContain('frame({ minHeight: ACTION_HEIGHT, maxHeight: ACTION_HEIGHT }), backgroundOverlay({ color: t.bgElevated }), clipShape("capsule")');
		expect(source).toContain("<SelectorText>{agentTitle}</SelectorText>");
		// Beside the project, the approval mode keeps its short name; the
		// project's gives way first.
		expect(source).toContain('indicator="chevron.up.chevron.down" priority={1} />');
		for (const id of ["spawn-host", "spawn-project", "spawn-harness", "spawn-approval"]) {
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

	// Attaching sits beside the primary button, as a solid circle that opens
	// the photo and file pickers; the primary button dictates while the prompt
	// is empty.
	it("attaches beside the primary button, which dictates while the prompt is empty", () => {
		expect(source).toContain('systemName="paperclip"');
		expect(source).toContain('<Button label="Choose Photo" systemImage="photo" onPress={onChoosePhoto} />');
		expect(source).toContain('<Button label="Choose File" systemImage="doc" onPress={onChooseFile} />');
		expect(source).toContain("onPress={dictates ? voice.onToggleLatched : onSpawn}");
		expect(source).not.toContain("MicKey");
	});

	// A solid filled capsule on every iOS version, not Liquid Glass's
	// translucent one, with the attach circle beside it at the same height.
	it("starts from a native prominent button rather than a scaling Pressable", () => {
		expect(source).not.toContain("Pressable");
		expect(source).not.toContain("glassProminent");
		// A fixed disc in every state, so the row never changes height.
		expect(source).toContain('<HStack modifiers={[frame({ width: ACTION_HEIGHT, height: ACTION_HEIGHT }), backgroundOverlay({ color: primaryFill }), clipShape("circle")');
		expect(source).toContain('frame({ width: ACTION_HEIGHT, height: ACTION_HEIGHT }),\n\t\t\t\t\t\t\t\t\tbackgroundOverlay({ color: t.bgElevated }),\n\t\t\t\t\t\t\t\t\tclipShape("circle"),');
		expect(source).toContain("disabledModifier(primaryDisabled)");
	});
});
