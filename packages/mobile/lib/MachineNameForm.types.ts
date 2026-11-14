import type { MachineSymbol } from "./machineAppearance";

export type MachineNameFormProps = {
	name: string;
	onChangeName: (name: string) => void;
	symbol: MachineSymbol;
	onSelectSymbol: (symbol: MachineSymbol) => void;
	/** Said under the icon when other machines already use it. */
	iconHint?: string;
	canSave: boolean;
	onSave: () => void;
};

export const MACHINE_NAME_HINT = "Choose a name you'll recognize in Orchestrator.";
