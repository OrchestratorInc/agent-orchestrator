import { describe, expect, it } from "vitest";
import { machineAppearance, machineAppearancePalette, unusedMachineSymbol } from "./machineAppearance";

describe("machineAppearance", () => {
	it("keeps a saved machine symbol and drops the legacy tone earlier builds stored", () => {
		expect(machineAppearance({ id: "host-a", name: "Studio Mac", appearance: { symbol: "server", tone: "purple" } as never })).toEqual({
			symbol: "server",
		});
	});

	it("draws a machine with no saved appearance as a laptop", () => {
		expect(machineAppearance({ id: "host-a", name: "Studio Mac" })).toEqual({ symbol: "laptop" });
		expect(machineAppearance({ id: "host-b", name: "Build Linux" })).toEqual({ symbol: "laptop" });
	});

	it("normalizes legacy saved symbols into the curated machine shapes", () => {
		expect(machineAppearance({ id: "old-desktop", name: "Desktop", appearance: { symbol: "monitor" } as never }).symbol).toBe("desktop");
		expect(machineAppearance({ id: "old-linux", name: "Linux", appearance: { symbol: "terminal" } as never }).symbol).toBe("linux");
		expect(machineAppearance({ id: "old-workstation", name: "Workstation", appearance: { symbol: "cpu" } as never }).symbol).toBe("mac-studio");
	});

	it("keeps the preferred icon when free and otherwise takes the first unused one", () => {
		expect(unusedMachineSymbol("laptop", new Set(["desktop"]))).toBe("laptop");
		expect(unusedMachineSymbol("laptop", new Set(["laptop", "server"]))).toBe("cloud");
		expect(unusedMachineSymbol("laptop", new Set(machineAppearancePalette.symbols))).toBe("laptop");
	});
});
