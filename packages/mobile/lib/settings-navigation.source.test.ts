import { existsSync, readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";

function read(relativePath: string): string {
	const path = fileURLToPath(new URL(relativePath, import.meta.url));
	return existsSync(path) ? readFileSync(path, "utf8") : "";
}

describe("Settings environment navigation", () => {
	// Settings is the list of machines; there is no second list page.
	it("lists the machines in Settings itself, with no list page of their own", () => {
		const settingsLayout = read("../app/settings/_layout.tsx");
		const settings = read("../app/settings/index.tsx");

		expect(settingsLayout).not.toBe("");
		expect(settingsLayout).not.toContain('name="environments"');
		expect(read("../app/settings/environments.tsx")).toBe("");
		expect(settings).not.toContain('label="Paired machines"');
		expect(settings).not.toContain("/settings/environments");
	});

	// A cold link to /settings/theme lands with the Settings list beneath it,
	// so the back button goes back to the list instead of leaving Settings.
	// Settings is pushed from the board's header on both platforms.
	it("opens every Settings page above the Settings list", () => {
		const settingsLayout = read("../app/settings/_layout.tsx");

		expect(settingsLayout).toContain('export const unstable_settings = { initialRouteName: "index" };');
	});

	// Each machine's row in Settings opens its page, pushed inside Settings'
	// stack with its editor above it.
	it("pushes a machine's page and its editor inside Settings' own stack", () => {
		const settings = read("../app/settings/index.tsx");
		const settingsLayout = read("../app/settings/_layout.tsx");

		expect(settings).toContain("<MachineRow");
		expect(settings).toContain('router.push({ pathname: "/settings/machine/[id]", params: { id: host.id } })');
		expect(settingsLayout).toContain('<Stack.Screen name="machine/[id]/index" options={{ title: "Machine" }} />');
		expect(settingsLayout).toContain('<Stack.Screen name="machine/[id]/edit" options={{ title: "Edit machine" }} />');
	});
});
