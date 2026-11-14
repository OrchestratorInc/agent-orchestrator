import { describe, expect, it } from "vitest";
import { appI18n } from "../i18n";
import { buildChoiceCommands, buildSettingsCommands, buildShortcutCommands, choiceValue, type PalettePreferences } from "./palette-registry";
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

	it("offers session commands only while a session is on screen", () => {
		const offSession = buildShortcutCommands(t).map((item) => item.id);
		const local = buildShortcutCommands(t, { session: "local" }).map((item) => item.id);
		const elsewhere = buildShortcutCommands(t, { session: "elsewhere" }).map((item) => item.id);
		for (const id of ["toggle-diff", "archive-session", "open-in-editor", "new-shell-terminal", "close-shell-terminal", "previous-tab"]) {
			expect(offSession).not.toContain(`shortcut:${id}`);
			expect(local).toContain(`shortcut:${id}`);
		}
		// Only a session on this machine has a worktree path to copy.
		expect(local).toContain("shortcut:copy-session-path");
		expect(elsewhere).not.toContain("shortcut:copy-session-path");
		expect(elsewhere).toContain("shortcut:toggle-diff");
		// App-wide commands are always there.
		expect(offSession).toContain("shortcut:toggle-sidebar");
		expect(offSession).toContain("shortcut:previous-session");
	});

	it("leaves out Toggle browser DevTools, which only the main process can run", () => {
		expect(buildShortcutCommands(t, { session: "local" }).some((item) => item.id === "shortcut:toggle-browser-devtools")).toBe(false);
	});

	it("offers the split shortcuts only while split panes are on", () => {
		const off = buildShortcutCommands(t, { session: "local" }).map((item) => item.id);
		const on = buildShortcutCommands(t, { splitPanes: true, session: "local" }).map((item) => item.id);
		expect(off).not.toContain("shortcut:split-shell-terminal");
		expect(off).not.toContain("shortcut:split-shell-terminal-down");
		expect(on).toContain("shortcut:split-shell-terminal");
		expect(on).toContain("shortcut:split-shell-terminal-down");
	});

	it("follows the settings catalog's visibility rules", () => {
		const regular = buildSettingsCommands(t, { developerMode: false, diagnostics: false }).map((item) => item.id);
		const developer = buildSettingsCommands(t, { developerMode: true, diagnostics: true }).map((item) => item.id);
		expect(regular).toContain("setting:harness");
		expect(regular).toContain("setting:remote-hosts");
		expect(regular).not.toContain("setting:diagnostics");
		expect(developer).toContain("setting:harness");
		expect(developer).toContain("setting:remote-hosts");
		expect(developer).toContain("setting:diagnostics");
	});

	it("offers Developer Mode rows only in Developer Mode and Window translucency only on macOS", () => {
		const regular = buildSettingsCommands(t, { developerMode: false, diagnostics: false }).map((item) => item.id);
		const developerMac = buildSettingsCommands(t, { developerMode: true, diagnostics: false, mac: true }).map((item) => item.id);
		for (const id of ["side-chats", "split-panes", "cli-link", "transparency"]) {
			expect(regular).not.toContain(`setting:${id}`);
			expect(developerMac).toContain(`setting:${id}`);
		}
	});

	it("lists only rows the settings pages still show", () => {
		const ids = buildSettingsCommands(t, { developerMode: true, diagnostics: true, mac: true }).map((item) => item.id);
		// Closed-lid keep awake is part of Keep this Mac awake.
		expect(ids).not.toContain("setting:keep-awake-lid");
		expect(ids).toContain("setting:color-theme");
		expect(ids).toContain("setting:computer-use");
	});

	it("names a row's page beside it and keeps page keywords on the page row", () => {
		const items = buildSettingsCommands(t, { developerMode: false, diagnostics: false });
		const logins = items.find((item) => item.id === "setting:harness-logins");
		expect(logins?.subtitle).toBe(t("settings.harness"));
		expect(logins?.action).toEqual({ kind: "open-settings-section", section: "harness", focus: t("settings.harness.viewLabel") });
		// Accounts live on the Harness page, so an accounts search lands there.
		expect(filterCommands(items, "codex accounts")[0]?.action).toMatchObject({ kind: "open-settings-section", section: "harness" });
		// API keys are set from a harness's Account, so a key search lands there too.
		expect(filterCommands(items, "api key")[0]?.action).toMatchObject({ kind: "open-settings-section", section: "harness" });
		expect(filterCommands(items, "pair phone")[0]?.id).toBe("setting:mobile");
		expect(filterCommands(items, "color theme")[0]?.id).toBe("setting:color-theme");
		expect(filterCommands(items, "dark mode")[0]?.id).toBe("setting:theme");
	});

	it("lands a row on itself and a page on its top", () => {
		const items = buildSettingsCommands(t, { developerMode: false, diagnostics: false });
		expect(items.find((item) => item.id === "setting:theme")?.action).toEqual({ kind: "open-settings-section", section: "general", focus: t("settings.theme") });
		expect(items.find((item) => item.id === "setting:updates")?.action).toEqual({ kind: "open-settings-section", section: "updates" });
	});

	const prefs: PalettePreferences = {
		themePreference: "dark",
		themeStyle: "nord",
		locale: "en",
		terminalClickMode: "app",
		chatSendKeyMode: "enter",
		toggles: { "render-math": true, sound: false },
	};

	it("flips a switch in place and shows it as a switch", () => {
		const items = buildSettingsCommands(t, { developerMode: false, diagnostics: false, prefs });
		const math = items.find((item) => item.id === "setting:render-math");
		const sound = items.find((item) => item.id === "setting:sound");
		expect(math).toMatchObject({ toggled: true, keepOpen: true, action: { kind: "run" } });
		expect(sound).toMatchObject({ toggled: false, keepOpen: true, action: { kind: "run" } });
	});

	it("opens a choice list from its setting, naming the current choice", () => {
		const items = buildSettingsCommands(t, { developerMode: false, diagnostics: false, prefs });
		expect(items.find((item) => item.id === "setting:theme")).toMatchObject({
			state: t("settings.theme.dark"),
			action: { kind: "open-view", view: { mode: "choices", menu: "theme" } },
		});
		expect(items.find((item) => item.id === "setting:color-theme")?.state).toBe("Nord");
	});

	it("checks the current choice and keeps the list open after a pick", () => {
		const rows = buildChoiceCommands("theme", t, prefs);
		expect(rows.map((row) => row.id)).toEqual(["choice:theme:light", "choice:theme:dark", "choice:theme:system"]);
		expect(rows.filter((row) => row.checked).map((row) => row.id)).toEqual(["choice:theme:dark"]);
		expect(rows.every((row) => row.keepOpen)).toBe(true);
		expect(choiceValue("choice:color-theme:nord")).toBe("nord");
		expect(choiceValue("setting:theme")).toBeUndefined();
	});

	it("finds a list by one of its options rather than listing the option", () => {
		const items = buildSettingsCommands(t, { developerMode: false, diagnostics: false, prefs });
		expect(filterCommands(items, "nord")[0]?.id).toBe("setting:color-theme");
		expect(items.some((item) => item.id.startsWith("choice:"))).toBe(false);
	});
});
