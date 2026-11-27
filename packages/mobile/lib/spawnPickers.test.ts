import { describe, expect, it, vi } from "vitest";

vi.mock("react-native", () => ({ Platform: { OS: "android" }, StyleSheet: { create: (styles: object) => styles } }));
vi.mock("./useEffortSlider", () => ({ useEffortSliderSwitch: () => ({}) }));

import type { ChatConfigOption } from "./chat/types";
import { spawnPermissionChoices } from "./spawnPickers";

// Claude Code's own modes, as its chat reports them (real payload).
const claude: ChatConfigOption[] = [{
	id: "mode", name: "Mode", category: "mode", type: "select", currentValue: "auto",
	choices: [
		{ permissionMode: "auto", value: "auto", name: "Auto", description: "Use a model classifier to approve/deny permission prompts" },
		{ permissionMode: "default", value: "default", name: "Manual", description: "Standard behavior, prompts for dangerous operations" },
		{ permissionMode: "accept-edits", value: "acceptEdits", name: "Accept Edits", description: "Auto-accept file edit operations" },
		{ value: "plan", name: "Plan Mode", description: "Planning mode, no actual tool execution" },
		{ value: "dontAsk", name: "Don't Ask", description: "Don't prompt for permissions, deny if not pre-approved" },
		{ permissionMode: "bypass-permissions", value: "bypassPermissions", name: "Bypass Permissions", description: "Bypass all permission checks" },
	],
} as ChatConfigOption];

const modes = [
	{ id: "default", label: "Use Claude permissions" },
	{ id: "auto", label: "Auto" },
	{ id: "accept-edits", label: "Accept edits" },
	{ id: "bypass-permissions", label: "Bypass permissions" },
];

describe("New task's permission choices", () => {
	// The chat says "Manual"; New task said "Use agent permissions" for the same mode.
	it("names the agent's modes as its chat does, sending AO's mode for each", () => {
		const choices = spawnPermissionChoices("claude-code", modes, "default", claude);
		expect(choices.map((choice) => [choice.value, choice.label, choice.selected])).toEqual([
			["auto", "Auto", false],
			["default", "Manual", true],
			["accept-edits", "Accept Edits", false],
			["bypass-permissions", "Bypass Permissions", false],
		]);
		expect(choices.map((choice) => choice.label)).not.toContain("Plan Mode");
	});

	it("keeps AO's words before the agent has reported its modes in a chat", () => {
		expect(spawnPermissionChoices("claude-code", modes, "auto").map((choice) => choice.label)).toEqual(modes.map((mode) => mode.label));
	});
});
