import { render } from "@testing-library/react";
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

// Claude's catalog names its default after a model ("Opus 5.5"); while another model
// runs, the picker shows that row instead of "opus". A model last used as "opus" must
// still find that row, or it is not listed first.
it("keeps a model folded into the agent's default row findable under its own id", () => {
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
