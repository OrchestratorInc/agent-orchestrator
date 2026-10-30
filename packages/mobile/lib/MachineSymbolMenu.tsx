import { Label, Picker } from "@expo/ui/swift-ui";
import { disabled as disabledModifier, pickerStyle, tag } from "@expo/ui/swift-ui/modifiers";
import { machineMenuImage } from "./machine-menu-image";
import { machineAppearancePalette, machineSymbolLabels, type MachineSymbol } from "./machineAppearance";

function isMachineSymbol(value: unknown): value is MachineSymbol {
	return machineAppearancePalette.symbols.some((symbol) => symbol === value);
}

/**
 * The machine's icon as an iOS form row: a menu picker whose choices show each
 * shape's SF Symbol beside its name, with the system's checkmark on the chosen
 * one. Only for a SwiftUI `Form`; Android picks from MachineSymbolPicker's grid.
 */
export function MachineSymbolMenu({ selected, onSelect, disabled = false, testID }: {
	selected: MachineSymbol;
	onSelect: (symbol: MachineSymbol) => void;
	disabled?: boolean;
	testID?: string;
}) {
	return (
		<Picker
			label="Icon"
			selection={selected}
			onSelectionChange={(value) => { if (isMachineSymbol(value) && value !== selected) onSelect(value); }}
			testID={testID}
			modifiers={[pickerStyle("menu"), disabledModifier(disabled)]}
		>
			{machineAppearancePalette.symbols.map((symbol) => (
				<Label key={symbol} title={machineSymbolLabels[symbol]} systemImage={machineMenuImage(symbol)} modifiers={[tag(symbol)]} />
			))}
		</Picker>
	);
}
