import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";

const source = readFileSync(fileURLToPath(new URL("./spawn-composer-controls.ios.tsx", import.meta.url)), "utf8");
const shared = readFileSync(fileURLToPath(new URL("./model-controls.ios.tsx", import.meta.url)), "utf8");

describe("iOS spawn menu layout", () => {
	// Fixed widths truncated longer names and fixed heights clipped the rail at
	// large text sizes; the host now takes the height its content asks for.
	it("lets the selectors size to their content", () => {
		expect(source).not.toMatch(/MENU_WIDTH/);
		expect(source).not.toMatch(/frame\(\{ height:/);
		expect(source).not.toMatch(/height: \d+/);
		expect(source).toContain("<Host matchContents={{ vertical: true }}");
		expect(source).toContain("<Spacer minLength={0} />");
		// The agent and its model are one enclosed capsule, the chat composer's
		// discs' height, holding the harness's mark and one name: the chat's own.
		expect(shared).toContain('frame({ minHeight: COMPOSER_DISC, maxHeight: COMPOSER_DISC }), backgroundOverlay({ color: t.bgElevated }), clipShape("capsule")');
		expect(source).toContain("label={<ModelCapsuleLabel mark={<HarnessImage harness={harness} />} title={agentTitle} />}");
		for (const id of ["spawn-host", "spawn-project"]) expect(source).toContain(`accessibilityIdentifier("${id}")`);
		for (const id of ["spawn-harness", "spawn-effort", "spawn-approval"]) expect(source).toContain(`"${id}"`);
	});

	// The composer and its controls row are the chat's, from the same pieces:
	// who runs the task and its effort at the start, the approval mode's symbol
	// at the end. Only the machine and the project are rows above it.
	it("fills the chat composer's controls row with the agent, its effort, then the approval mode", () => {
		const settings = source.indexOf("settings={");
		expect(source).toContain("<SpawnComposer");
		expect(source).toContain('import { ApprovalMenu, Choice, ModelCapsuleLabel, useTriggerModifiers } from "./model-controls.ios";');
		expect(source).toContain('import { EffortGaugeButton } from "./effort-gauge";');
		expect(source).toContain('import { EffortSliderOverlay } from "./effort-slider";');
		expect(source.indexOf('accessibilityIdentifier("spawn-project")')).toBeLessThan(settings);
		expect(source.indexOf('id: "spawn-harness"')).toBeGreaterThan(settings);
		expect(source.indexOf('testID="spawn-effort"')).toBeGreaterThan(source.indexOf('id: "spawn-harness"'));
		expect(source.indexOf('id="spawn-approval"')).toBeGreaterThan(source.indexOf('testID="spawn-effort"'));
		expect(source).toContain('settingsHost: { alignSelf: "stretch", height: touchTarget }');
	});

	it("scales every label with Dynamic Type", () => {
		expect(source).not.toMatch(/font\(\{ size/);
		expect(shared).not.toMatch(/font\(\{ size/);
		expect(shared).toContain('font({ textStyle: "subheadline", weight: "medium" })');
		expect(source).toContain('font({ textStyle: "body" })');
	});

	// Checkmark images on plain buttons drew a selection VoiceOver never heard.
	it("checks the current choice with the system's checkable menu item", () => {
		expect(source).not.toContain('"checkmark"');
		expect(shared).toContain("<Toggle isOn={selected} onIsOnChange={onIsOnChange}");
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
});
