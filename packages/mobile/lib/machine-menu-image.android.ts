import type { ImageSourcePropType } from "react-native";
import { machineSymbolNames, type MachineSymbol } from "./machineAppearance";
import { symbols } from "./symbols";
import { materialDrawable } from "./toolbarIcon.android";

/** A machine's Material Symbol drawable, for a MenuView action's image or a Compose `Icon`. */
export function machineMenuImage(symbol: MachineSymbol): ImageSourcePropType {
	return materialDrawable(symbols[machineSymbolNames[symbol]].android);
}
