import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";

const source = readFileSync(
	fileURLToPath(new URL("../app/settings/index.tsx", import.meta.url)),
	"utf8",
);

// The Desktop row used to say "Paired" whenever an address was saved, right
// above a Test connection row reporting the desktop unreachable.
describe("settings desktop section", () => {
	it("reports live connection state rather than a saved-address flag", () => {
		expect(source).not.toMatch(/"Paired"/);
		expect(source).not.toMatch(/label="Selected machine"/);
		expect(source).toMatch(/<MachineSelector/);
		// One row per machine says its connection once; the default machine's
		// is the board poll's live status.
		expect(source).toMatch(/<MachineConnectionRow/);
		expect(source).toContain("return { label: machineStatus.label, tone: machineStatusTone(machineStatus.tone) };");
	});

	it("leaves destructive management in Environments", () => {
		expect(source).not.toMatch(/DisconnectRow|forgetServer|forgetPairedHost/);
	});
});
