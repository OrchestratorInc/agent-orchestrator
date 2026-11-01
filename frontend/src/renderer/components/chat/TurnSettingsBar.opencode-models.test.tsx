import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import type { ChatConfigOption } from "../../types/conversation";
import type { PickerSource } from "./AgentSwitchPicker";

const picker = vi.hoisted(() => ({ current: undefined as PickerSource | undefined }));
vi.mock("./AgentSwitchPicker", async (original) => ({
	...await original<typeof import("./AgentSwitchPicker")>(),
	AgentModelPicker: ({ current }: { current: PickerSource }) => {
		picker.current = current;
		return null;
	},
}));
import { TurnSettingsBar } from "./TurnSettingsBar";

const direct = "opencode/deepseek-v4-flash";
const go = "opencode-go/deepseek-v4-flash";

function modelOption(currentValue: string): ChatConfigOption {
	return {
		id: "model", name: "Model", category: "model", type: "select", currentValue,
		choices: [direct, go].map((value) => ({ value, name: `${value.split("/")[0]}/DeepSeek V4 Flash` })),
	};
}

describe.each(["opencode", "opencode-v2"])("%s provider model choices", (harness) => {
	it.each([direct, go])("keeps both routes selectable when %s is current", async (current) => {
		const user = userEvent.setup();
		const change = vi.fn();
		render(<TurnSettingsBar harness={harness} models={[]} settings={{ model: current }}
			configOptions={[modelOption(current)]} onChangeConfigOption={change} />);

		await user.click(screen.getByRole("button", { name: "Model" }));
		expect(screen.getAllByRole("menuitemradio", { name: "DeepSeek V4 Flash" })).toHaveLength(2);
		expect(screen.getByText("OpenCode", { exact: true })).toBeInTheDocument();
		expect(screen.getByText("OpenCode Go", { exact: true })).toBeInTheDocument();
		await user.click(screen.getByRole("menuitemradio", { name: "DeepSeek V4 Flash", checked: false }));
		expect(change).toHaveBeenCalledWith("model", { value: current === direct ? go : direct });
	});

	it("keeps OpenCode Go distinct in the unified chat picker and sends its exact ID", () => {
		const change = vi.fn();
		render(<TurnSettingsBar harness={harness} models={[]} settings={{ model: direct }}
			configOptions={[modelOption(direct)]} onChangeConfigOption={change}
			agentSwitch={{ projectId: "p", onPendingChange: vi.fn() }} />);

		expect(picker.current?.models).toEqual([
			{ id: direct, label: "DeepSeek V4 Flash", group: "OpenCode", aliases: undefined },
			{ id: go, label: "DeepSeek V4 Flash", group: "OpenCode Go", aliases: undefined },
		]);
		picker.current?.onPickModel?.(go);
		expect(change).toHaveBeenCalledWith("model", { value: go });
	});
});
