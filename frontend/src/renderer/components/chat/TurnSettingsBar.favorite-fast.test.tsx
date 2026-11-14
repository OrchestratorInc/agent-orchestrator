import { render, act } from "@testing-library/react";
import { expect, it, vi } from "vitest";
import type { PickerSource } from "./AgentSwitchPicker";
const { source } = vi.hoisted(() => ({ source: { current: undefined as PickerSource | undefined } }));
vi.mock("./AgentSwitchPicker", async original => ({
	...await original<typeof import("./AgentSwitchPicker")>(),
	AgentModelPicker: ({current}: {current:PickerSource}) => { source.current = current; return null; },
}));
import { TurnSettingsBar } from "./TurnSettingsBar";

it.each([true, false])("restores a saved ACP favorite's fast=%s setting along with its model and effort", async savedFast => {
	const change = vi.fn().mockResolvedValue(undefined);
	render(<TurnSettingsBar harness="claude-code" models={[]} settings={{approvalMode:"accept-edits"}}
		onChangeConfigOption={change} agentSwitch={{projectId:"p",onPendingChange:vi.fn()}}
		configOptions={[
			{id:"model",name:"Model",category:"model",type:"select",currentValue:"opus",choices:[{value:"opus",name:"Opus"}]},
			{id:"effort",name:"Effort",category:"thought_level",type:"select",currentValue:"high",choices:[{value:"high",name:"High"}]},
			{id:"fast",name:"Fast mode",type:"boolean",currentBoolean:!savedFast,choices:[]},
		]} />);
	expect(source.current?.fast).toBe(!savedFast);
	await act(async () => {
		source.current?.onPickSetup?.({harness:"claude-code",row:"opus",model:"opus",label:"Opus",effort:"high",fast:savedFast || undefined});
		await Promise.resolve();
	});
	expect(change).toHaveBeenCalledWith("fast", {enabled:savedFast});
});
