import type { SFSymbol } from "sf-symbols-typescript";
import type { MachineSymbol } from "./machineAppearance";

const symbols: Record<MachineSymbol, SFSymbol> = {
	server: "server.rack",
	cloud: "cloud",
	linux: "terminal",
	desktop: "desktopcomputer",
	laptop: "laptopcomputer",
	"mac-mini": "macmini",
	"mac-studio": "macstudio",
};

export function machineMenuImage(symbol: MachineSymbol): SFSymbol {
	return symbols[symbol];
}
