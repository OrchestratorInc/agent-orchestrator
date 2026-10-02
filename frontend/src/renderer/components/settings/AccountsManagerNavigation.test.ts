import { describe, expect, it } from "vitest";
import { createAppI18n } from "../../i18n/instance";
import { globalSettingsItem, globalSettingsItemsFor, visibleGlobalSettings } from "./settingsCatalog";

describe("Accounts Manager navigation", () => {
	it.each([false, true])("has one Accounts surface with cloud=%s", (cloudEnabled) => {
		const entries = visibleGlobalSettings({ cloudEnabled });
		expect(entries.filter((entry) => entry.id === "accounts")).toHaveLength(1);
		expect(entries.some((entry) => entry.id === "agents")).toBe(false);
		expect(entries.map((entry) => entry.label(createAppI18n("en").t))).not.toContain("Subscriptions");
	});

	it("resolves legacy subscription callers to Accounts without duplicating all-settings content", () => {
		expect(globalSettingsItem("agents", { cloudEnabled: false }).id).toBe("accounts");
		expect(globalSettingsItemsFor("agents", { cloudEnabled: false }).map((entry) => entry.id)).toEqual(["accounts"]);
		expect(globalSettingsItemsFor("all", { cloudEnabled: false }).filter((entry) => entry.id === "accounts")).toHaveLength(1);
	});

	it.each([
		["en", "Accounts"],
		["es", "Cuentas"],
	] as const)("localizes the Accounts navigation label in %s", (locale, label) => {
		const item = globalSettingsItem("accounts", { cloudEnabled: false });
		expect(item.label(createAppI18n(locale).t)).toBe(label);
	});
});
