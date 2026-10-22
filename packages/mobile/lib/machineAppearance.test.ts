import { describe, expect, it } from "vitest";
import { machineAppearance, machineAppearancePalette } from "./machineAppearance";

describe("machineAppearance", () => {
	it("keeps a saved machine symbol and drops the legacy tone earlier builds stored", () => {
		expect(machineAppearance({ id: "host-a", name: "Studio Mac", appearance: { symbol: "server", tone: "purple" } as never })).toEqual({
			symbol: "server",
		});
	});

	it("gives existing machines a stable identity when no appearance was saved", () => {
		const first = machineAppearance({ id: "host-a", name: "Studio Mac" });
		const repeated = machineAppearance({ id: "host-a", name: "Studio Mac" });
		const second = machineAppearance({ id: "host-b", name: "Build Linux" });

		expect(repeated).toEqual(first);
		expect(second).not.toEqual(first);
		expect(machineAppearancePalette.symbols).toContain(first.symbol);
	});

	it("normalizes legacy saved symbols into the curated machine shapes", () => {
		expect(machineAppearance({ id: "old-desktop", name: "Desktop", appearance: { symbol: "monitor" } as never }).symbol).toBe("desktop");
		expect(machineAppearance({ id: "old-linux", name: "Linux", appearance: { symbol: "terminal" } as never }).symbol).toBe("linux");
		expect(machineAppearance({ id: "old-workstation", name: "Workstation", appearance: { symbol: "cpu" } as never }).symbol).toBe("mac-studio");
	});
});
