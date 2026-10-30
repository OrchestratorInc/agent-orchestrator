import { describe, expect, it } from "vitest";
import { appI18n } from "../i18n";
import { buildSettingsCommands, buildShortcutCommands } from "./palette-registry";
import { filterCommands } from "./command-palette";

const t = appI18n.t.bind(appI18n);

describe("palette registry", () => {
	it("offers shortcut actions by their localized names, search-only", () => {
		const items = buildShortcutCommands(t);
		const sidebar = items.find((item) => item.id === "shortcut:toggle-sidebar");
		expect(sidebar).toMatchObject({ title: "Toggle sidebar", searchOnly: true, action: { kind: "run-shortcut", shortcut: "toggle-sidebar" } });
		// The palette's own openers and actions it already lists are left out.
		for (const id of ["command-palette", "quick-open-file", "jump-to-session", "open-settings", "new-session", "open-orchestrator"]) {
			expect(items.some((item) => item.id === `shortcut:${id}`)).toBe(false);
		}
	});

	it("follows the settings catalog's visibility rules", () => {
		const regular = buildSettingsCommands(t, { developerMode: false, diagnostics: false }).map((item) => item.id);
		const developer = buildSettingsCommands(t, { developerMode: true, diagnostics: true }).map((item) => item.id);
		expect(regular).toContain("setting:accounts");
		expect(regular).toContain("setting:remote-hosts");
		expect(regular).not.toContain("setting:diagnostics");
		expect(developer).not.toContain("setting:accounts");
		expect(developer).toContain("setting:remote-hosts");
		expect(developer).toContain("setting:diagnostics");
	});

	it("names a row's page beside it and lets rows inherit their page's keywords", () => {
		const items = buildSettingsCommands(t, { developerMode: false, diagnostics: false });
		const addAccount = items.find((item) => item.id === "setting:add-account");
		expect(addAccount?.subtitle).toBe(t("settings.agents"));
		expect(addAccount?.action).toEqual({ kind: "open-settings-section", section: "agents", focus: t("settings.codexAccounts.add") });
		expect(filterCommands(items, "add codex account")[0]?.id).toBe("setting:add-account");
		// Any Mobile row is right: they all open the Mobile page.
		expect(filterCommands(items, "pair phone")[0]?.action).toMatchObject({ kind: "open-settings-section", section: "mobile" });
		expect(filterCommands(items, "dark mode")[0]?.id).toBe("setting:theme");
	});

	it("lands a row on itself and a page on its top", () => {
		const items = buildSettingsCommands(t, { developerMode: false, diagnostics: false });
		expect(items.find((item) => item.id === "setting:theme")?.action).toEqual({ kind: "open-settings-section", section: "general", focus: t("settings.theme") });
		expect(items.find((item) => item.id === "setting:updates")?.action).toEqual({ kind: "open-settings-section", section: "updates" });
	});
});
