import { readFileSync } from "node:fs";
import { describe, expect, it } from "vitest";

const screen = readFileSync(new URL("../app/pair.tsx", import.meta.url), "utf8");

describe("legacy reconnect route", () => {
	it("distinguishes a selected empty id from an absent repair parameter", () => {
		expect(screen).toContain("() => repairHostId !== undefined ? undefined : null");
		expect(screen).toContain("if (repairHostId === undefined)");
		expect(screen).toContain("if (repairHostId !== undefined && !repairHost)");
		expect(screen).not.toContain("if (!repairHostId)");
	});
});
