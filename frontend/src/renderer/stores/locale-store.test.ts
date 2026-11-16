import { beforeEach, describe, expect, it, vi } from "vitest";
import { appI18n } from "../i18n";

const getUiSettings = vi.fn();
const setUiSettings = vi.fn();

vi.mock("../lib/bridge", () => ({
	aoBridge: {
		uiSettings: {
			get: (...args: unknown[]) => getUiSettings(...args),
			set: (...args: unknown[]) => setUiSettings(...args),
		},
	},
}));

import { useLocaleStore } from "./locale-store";

describe("locale-store", () => {
	beforeEach(async () => {
		getUiSettings.mockReset();
		setUiSettings.mockReset();
		getUiSettings.mockResolvedValue({ locale: "en" });
		setUiSettings.mockImplementation(async (settings: { locale: string }) => settings);
		await appI18n.changeLanguage("en");
		useLocaleStore.setState({ locale: "en", loaded: false, saving: false, saveError: false });
		document.documentElement.lang = "en";
		document.documentElement.dir = "";
	});

	it("defaults to English before load", () => {
		expect(useLocaleStore.getState().locale).toBe("en");
		expect(appI18n.t("settings.general")).toBe("General");
		expect(document.documentElement.lang).toBe("en");
	});

	it("loads persisted locale from the main process", async () => {
		getUiSettings.mockResolvedValue({ locale: "zh-CN" });
		await useLocaleStore.getState().load();
		expect(useLocaleStore.getState().locale).toBe("zh-CN");
		expect(appI18n.t("settings.general")).toBe("通用");
		expect(document.documentElement.lang).toBe("zh-CN");
		expect(document.documentElement.dir).toBe("ltr");
		expect(useLocaleStore.getState().loaded).toBe(true);
	});

	it("persists locale changes and updates document lang", async () => {
		await useLocaleStore.getState().setLocale("zh-CN");
		expect(setUiSettings).toHaveBeenCalledWith({ locale: "zh-CN" });
		expect(useLocaleStore.getState().locale).toBe("zh-CN");
		expect(document.documentElement.lang).toBe("zh-CN");
		expect(document.documentElement.dir).toBe("ltr");
		expect(appI18n.t("settings.language")).toBe("语言");
	});

	it("does not reload after the first successful load", async () => {
		await useLocaleStore.getState().load();
		await useLocaleStore.getState().load();
		expect(getUiSettings).toHaveBeenCalledTimes(1);
	});

	it("loads persisted Turkish and can switch back to it without restarting", async () => {
		getUiSettings.mockResolvedValue({ locale: "tr" });
		await useLocaleStore.getState().load();
		expect(document.documentElement.lang).toBe("tr");
		expect(document.documentElement.dir).toBe("ltr");
		expect(appI18n.t("settings.general")).toBe("Genel");
		await useLocaleStore.getState().setLocale("en");
		await useLocaleStore.getState().setLocale("tr");
		expect(setUiSettings).toHaveBeenLastCalledWith({ locale: "tr" });
		expect(useLocaleStore.getState().locale).toBe("tr");
		expect(appI18n.t("settings.title")).toBe("Ayarlar");
	});

	it("shares one persisted read across concurrent startup callers", async () => {
		let resolveGet: ((settings: { locale: "zh-CN" }) => void) | undefined;
		getUiSettings.mockReturnValue(
			new Promise<{ locale: "zh-CN" }>((resolve) => {
				resolveGet = resolve;
			}),
		);

		const first = useLocaleStore.getState().load();
		const second = useLocaleStore.getState().load();
		expect(getUiSettings).toHaveBeenCalledTimes(1);
		resolveGet?.({ locale: "zh-CN" });
		await Promise.all([first, second]);

		expect(useLocaleStore.getState()).toMatchObject({ locale: "zh-CN", loaded: true });
	});

	it("does not let a late persisted read overwrite a newer user selection", async () => {
		let resolveGet: ((settings: { locale: "en" }) => void) | undefined;
		getUiSettings.mockReturnValue(
			new Promise<{ locale: "en" }>((resolve) => {
				resolveGet = resolve;
			}),
		);

		const loading = useLocaleStore.getState().load();
		await useLocaleStore.getState().setLocale("zh-CN");
		resolveGet?.({ locale: "en" });
		await loading;

		expect(useLocaleStore.getState().locale).toBe("zh-CN");
		expect(appI18n.language).toBe("zh-CN");
	});

	it("keeps the English fallback usable when persisted settings cannot be read", async () => {
		getUiSettings.mockRejectedValue(new Error("IPC unavailable"));
		await expect(useLocaleStore.getState().load()).resolves.toBeUndefined();
		expect(useLocaleStore.getState().locale).toBe("en");
		expect(useLocaleStore.getState().loaded).toBe(true);
		expect(appI18n.t("settings.general")).toBe("General");
	});

	it("keeps the current locale and exposes an error when persistence fails", async () => {
		setUiSettings.mockRejectedValue(new Error("disk full"));
		await expect(useLocaleStore.getState().setLocale("zh-CN")).resolves.toBeUndefined();
		expect(useLocaleStore.getState()).toMatchObject({
			locale: "en",
			saving: false,
			saveError: true,
		});
		expect(appI18n.language).toBe("en");
	});
});
