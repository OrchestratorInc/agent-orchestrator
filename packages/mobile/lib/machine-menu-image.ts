import { machineSymbolNames, type MachineSymbol } from "./machineAppearance";
import { symbols, type IOSSymbol } from "./symbols";

/** A machine's SF Symbol, for a native menu action's image. */
export function machineMenuImage(symbol: MachineSymbol): IOSSymbol {
	return symbols[machineSymbolNames[symbol]].ios;
}
