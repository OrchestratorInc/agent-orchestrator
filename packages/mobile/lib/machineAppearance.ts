import type { AppSymbolName } from "./symbols";

export const machineAppearancePalette = {
	symbols: ["server", "cloud", "linux", "desktop", "laptop", "mac-mini", "mac-studio"] as const,
};

export type MachineSymbol = (typeof machineAppearancePalette.symbols)[number];

/** Phone-local visual identity. Earlier builds also stored a `tone`, which
 * nothing renders; it is ignored when read back. */
export type MachineAppearance = {
	symbol: MachineSymbol;
};

export const machineSymbolLabels: Record<MachineSymbol, string> = {
	server: "Server",
	cloud: "Cloud VM",
	linux: "Linux/WSL",
	desktop: "Desktop",
	laptop: "Laptop",
	"mac-mini": "Mini PC",
	"mac-studio": "Workstation",
};

/** The system glyph each machine shape draws as: an SF Symbol on iOS, a Material Symbol on Android. */
export const machineSymbolNames = {
	server: "server",
	cloud: "cloud",
	linux: "terminal",
	desktop: "desktop",
	laptop: "laptop",
	"mac-mini": "miniPc",
	"mac-studio": "workstation",
} as const satisfies Record<MachineSymbol, AppSymbolName>;

const legacySymbols: Record<string, MachineSymbol> = {
	monitor: "desktop",
	terminal: "linux",
	command: "linux",
	cpu: "mac-studio",
	globe: "cloud",
	layers: "server",
};

type MachineIdentity = {
	id: string;
	name: string;
	appearance?: { symbol: string };
};

/** What a machine with no saved icon draws as: most paired machines are someone's laptop. */
const DEFAULT_SYMBOL: MachineSymbol = "laptop";

export function machineAppearance(machine: MachineIdentity): MachineAppearance {
	if (machine.appearance) {
		const symbol = machineAppearancePalette.symbols.find((candidate) => candidate === machine.appearance?.symbol) ?? legacySymbols[machine.appearance.symbol] ?? "server";
		return { symbol };
	}
	return { symbol: DEFAULT_SYMBOL };
}

/** The preferred icon unless another machine already has it; then the first free one. */
export function unusedMachineSymbol(preferred: MachineSymbol, taken: ReadonlySet<MachineSymbol>): MachineSymbol {
	if (!taken.has(preferred)) return preferred;
	return machineAppearancePalette.symbols.find((symbol) => !taken.has(symbol)) ?? preferred;
}
