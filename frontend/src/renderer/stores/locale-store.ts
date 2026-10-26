import { create } from "zustand";
import { aoBridge } from "../lib/bridge";
import { appI18n, coerceLocale, DEFAULT_LOCALE, documentLang, loadLocaleCatalog, type AppLocale } from "../i18n";

type LocaleState = {
	locale: AppLocale;
	loaded: boolean;
	saving: boolean;
	saveError: boolean;
	load: () => Promise<void>;
	setLocale: (locale: AppLocale) => Promise<void>;
};

async function applyLocale(locale: AppLocale): Promise<void> {
	// Non-English catalogs are lazy chunks; load before switching so the first
	// render in the new language is complete.
	await loadLocaleCatalog(locale);
	const changingLanguage = appI18n.changeLanguage(locale);
	if (typeof document !== "undefined") {
		document.documentElement.lang = documentLang(locale);
		document.documentElement.dir = appI18n.dir(locale);
	}
	await changingLanguage;
}

// Apply default lang early so the document attribute is correct before hydrate.
void applyLocale(DEFAULT_LOCALE);

let localeRevision = 0;
let pendingLoad: Promise<void> | undefined;

export const useLocaleStore = create<LocaleState>((set, get) => ({
	locale: DEFAULT_LOCALE,
	loaded: false,
	saving: false,
	saveError: false,
	load: async () => {
		if (get().loaded) return;
		if (pendingLoad) return pendingLoad;
		const revisionAtStart = localeRevision;
		pendingLoad = (async () => {
			let locale = DEFAULT_LOCALE;
			try {
				const settings = await aoBridge.uiSettings.get();
				locale = coerceLocale(settings.locale);
			} catch {
				// A missing bridge or unreadable setting must not prevent the UI from starting.
			}
			if (revisionAtStart !== localeRevision) return;
			await applyLocale(locale);
			if (revisionAtStart === localeRevision) set({ locale, loaded: true });
		})();
		try {
			await pendingLoad;
		} finally {
			pendingLoad = undefined;
		}
	},
	setLocale: async (candidate) => {
		const locale = coerceLocale(candidate);
		const revision = ++localeRevision;
		set({ saving: true, saveError: false });
		try {
			await aoBridge.uiSettings.set({ locale });
			await applyLocale(locale);
			if (revision === localeRevision) set({ locale, loaded: true, saving: false });
		} catch {
			if (revision === localeRevision) set({ saving: false, saveError: true });
		}
	},
}));

export function useLocale(): AppLocale {
	return useLocaleStore((state) => state.locale);
}
