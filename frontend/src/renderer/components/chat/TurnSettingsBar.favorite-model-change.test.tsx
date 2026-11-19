import { act, render } from "@testing-library/react";
import { expect, it, vi } from "vitest";
import type { PickerSource } from "./AgentSwitchPicker";
import type { ChatConfigOption } from "../../types/conversation";

const picker = vi.hoisted(() => ({ current: undefined as PickerSource | undefined }));
vi.mock("./AgentSwitchPicker", async (original) => ({
	...await original<typeof import("./AgentSwitchPicker")>(),
	AgentModelPicker: ({ current }: { current: PickerSource }) => {
		picker.current = current;
		return null;
	},
}));
import { TurnSettingsBar } from "./TurnSettingsBar";

const model: ChatConfigOption = { id: "model", name: "Model", category: "model", type: "select", currentValue: "haiku", choices: [{ value: "haiku", name: "Haiku" }, { value: "opus", name: "Opus" }] };
const opusOptions: ChatConfigOption[] = [
	{ ...model, currentValue: "opus" },
	{ id: "effort", name: "Effort", category: "thought_level", type: "select", currentValue: "max", choices: [{ value: "high", name: "High" }, { value: "max", name: "Max" }] },
	{ id: "fast", name: "Fast mode", type: "boolean", currentBoolean: false, choices: [] },
];

async function pickSetup(
	harness: string,
	options: ChatConfigOption[],
	afterModel: ChatConfigOption[],
	setup: Parameters<NonNullable<PickerSource["onPickSetup"]>>[0],
) {
	const change = vi.fn().mockResolvedValue(afterModel);
	render(<TurnSettingsBar harness={harness} models={[]} settings={{ model: options[0]?.currentValue }}
		configOptions={options} onChangeConfigOption={change} agentSwitch={{ projectId: "p", onPendingChange: vi.fn() }} />);
	await act(async () => {
		picker.current?.onPickSetup?.(setup);
		for (let tick = 0; tick < 6; tick++) await Promise.resolve();
	});
	return change;
}

// The options a favorite restores after its model are the ones that model offers.
it("sets Fast that only the favorite's model offers", async () => {
	const change = await pickSetup("claude-code", [model], opusOptions, { harness: "claude-code", row: "opus", model: "opus", label: "Opus", effort: "high", fast: true });
	expect(change).toHaveBeenCalledWith("fast", { enabled: true });
});

it("sets the effort that only the favorite's model offers", async () => {
	const change = await pickSetup("claude-code", [model], opusOptions, { harness: "claude-code", row: "opus", model: "opus", label: "Opus", effort: "high" });
	expect(change).toHaveBeenCalledWith("effort", { value: "high" });
});

it("uses the Fast choices of the favorite's model", async () => {
	const oldFast: ChatConfigOption = { id: "fast", name: "Fast mode", type: "select", currentValue: "normal", choices: [{ value: "normal", name: "Off" }, { value: "priority", name: "On" }] };
	const newFast: ChatConfigOption = { ...oldFast, currentValue: "default", choices: [{ value: "default", name: "Off" }, { value: "fast", name: "On" }] };
	const change = await pickSetup("claude-code", [model, oldFast], [opusOptions[0], newFast], { harness: "claude-code", row: "opus", model: "opus", label: "Opus", fast: true });
	expect(change).toHaveBeenCalledWith("fast", { value: "fast" });
	expect(change).not.toHaveBeenCalledWith("fast", { value: "priority" });
});

it("writes no Fast when the favorite's model has none", async () => {
	const oldFast: ChatConfigOption = { id: "fast", name: "Fast mode", type: "boolean", currentBoolean: true, choices: [] };
	const change = await pickSetup("claude-code", [{ ...model, currentValue: "opus" }, oldFast], [model], { harness: "claude-code", row: "haiku", model: "haiku", label: "Haiku" });
	expect(change.mock.calls.filter(([id]) => id === "fast")).toEqual([]);
});

it.each([true, false])("restores a Cursor favorite's saved Thinking %s", async (thinking) => {
	const options: ChatConfigOption[] = [
		{ id: "model", name: "Model", category: "model", type: "select", currentValue: "model-1", choices: [{ value: "model-1", name: "Model 1" }] },
		{ id: "effort", name: "Effort", category: "thought_level", type: "select", currentValue: "high", choices: [{ value: "high", name: "High" }] },
		{ id: "thinking", name: "Thinking", category: "thought_level", type: "select", currentValue: thinking ? "off" : "on", choices: [{ value: "off", name: "Off" }, { value: "on", name: "On" }] },
	];
	const change = await pickSetup("cursor", options, options, { harness: "cursor", row: "model-1", model: "model-1", label: "Model 1", effort: "high", thinking });
	expect(change).toHaveBeenCalledWith("thinking", { value: thinking ? "on" : "off" });
});

// Claude's catalog names its default after a model ("Opus 5.5"); while another model
// runs, the picker shows that row instead of "opus". A favourite saved on Opus must
// still find its model, or it is greyed out.
it("keeps a model folded into the agent's default row offered to its favourites", () => {
	const claude: ChatConfigOption = {
		id: "model", name: "Model", category: "model", type: "select", currentValue: "haiku",
		choices: [
			{ value: "default", name: "Default (recommended)", description: "Opus 5.5" },
			{ value: "opus", name: "Opus 5.5" },
			{ value: "haiku", name: "Haiku 4.5" },
		],
	};
	render(<TurnSettingsBar harness="claude-code" models={[]} settings={{ model: "haiku" }}
		configOptions={[claude]} onChangeConfigOption={vi.fn()} agentSwitch={{ projectId: "p", onPendingChange: vi.fn() }} />);
	const rows = picker.current?.models ?? [];
	expect(rows.some((model) => model.id === "opus" || model.aliases?.includes("opus"))).toBe(true);
});
