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

function identityHash(value: string): number {
	let hash = 2166136261;
	for (let index = 0; index < value.length; index++) {
		hash ^= value.charCodeAt(index);
		hash = Math.imul(hash, 16777619);
	}
	return hash >>> 0;
}

export function machineAppearance(machine: MachineIdentity): MachineAppearance {
	if (machine.appearance) {
		const symbol = machineAppearancePalette.symbols.find((candidate) => candidate === machine.appearance?.symbol) ?? legacySymbols[machine.appearance.symbol] ?? "server";
		return { symbol };
	}
	const symbols = machineAppearancePalette.symbols;
	return { symbol: symbols[identityHash(machine.id || machine.name) % symbols.length] };
}
