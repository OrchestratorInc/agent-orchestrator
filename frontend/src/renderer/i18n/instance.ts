import { createInstance, type BackendModule, type i18n } from "i18next";
import { initReactI18next } from "react-i18next";
import { APP_LOCALES, DEFAULT_LOCALE, type AppLocale } from "./locales";
import { enMessages } from "./messages";

export type TranslationCatalogs = Record<AppLocale, Record<string, string | string[]>>;

type Catalog = Record<string, string | string[]>;

// One lazy chunk per locale. English ships in the startup bundle as the
// fallback; the rest load only when the user's saved locale asks for them.
const localeLoaders: Record<Exclude<AppLocale, "en">, () => Promise<{ default: Catalog }>> = {
	"zh-CN": () => import("./zh-CN.json"),
	ja: () => import("./ja.json"),
	ko: () => import("./ko.json"),
	es: () => import("./es.json"),
	fr: () => import("./fr.json"),
	de: () => import("./de.json"),
	"pt-BR": () => import("./pt-BR.json"),
};

/**
 * i18next backend that imports a locale's catalog on demand. Because it is a
 * backend, every changeLanguage() waits for the catalog before switching, so
 * no caller can render a non-English language with an empty bundle. A failed
 * import reports an empty bundle and the fallback (English) is used.
 */
const lazyLocaleBackend: BackendModule = {
	type: "backend",
	init() {},
	read(language, _namespace, callback) {
		const load = localeLoaders[language as Exclude<AppLocale, "en">];
		if (!load) {
			callback(null, {});
			return;
		}
		load().then(
			(module) => callback(null, module.default),
			() => callback(null, {}),
		);
	},
};

/** Create an isolated, synchronously initialized instance for unit tests. */
export function createAppI18n(
	locale: AppLocale = DEFAULT_LOCALE,
	catalogs: Partial<TranslationCatalogs> = { en: enMessages },
): i18n {
	return initializeI18n(createInstance(), locale, catalogs);
}

function initializeI18n(instance: i18n, locale: AppLocale, catalogs: Partial<TranslationCatalogs>, lazy = false): i18n {
	// Only locales actually supplied are registered: an empty placeholder bundle
	// would count as loaded and stop the lazy backend from fetching the real one.
	const resources = Object.fromEntries(
		APP_LOCALES.filter((lng) => catalogs[lng] !== undefined || !lazy).map((lng) => [lng, { translation: catalogs[lng] ?? {} }]),
	);
	void instance.init({
		lng: locale,
		fallbackLng: DEFAULT_LOCALE,
		supportedLngs: [...APP_LOCALES],
		load: "currentOnly",
		resources,
		partialBundledLanguages: lazy,
		defaultNS: "translation",
		keySeparator: false,
		nsSeparator: false,
		returnNull: false,
		initAsync: false,
		interpolation: { escapeValue: false },
	});
	return instance;
}

/** An English-only instance that loads other locales on changeLanguage(), as the app uses. */
export function createLazyAppI18n(): i18n {
	return initializeI18n(createInstance().use(lazyLocaleBackend), DEFAULT_LOCALE, { en: enMessages }, true);
}

export const appI18n = initializeI18n(
	createInstance().use(lazyLocaleBackend).use(initReactI18next),
	DEFAULT_LOCALE,
	{ en: enMessages },
	true,
);

function hasCatalog(instance: i18n, locale: AppLocale): boolean {
	const bundle = instance.getResourceBundle(locale, "translation") as Catalog | undefined;
	return bundle !== undefined && Object.keys(bundle).length > 0;
}

/**
 * Load a locale's catalog ahead of switching to it (e.g. to warm it). Optional:
 * changeLanguage() on an instance with the lazy backend loads it by itself.
 */
export function loadLocaleCatalog(locale: AppLocale, instance: i18n = appI18n): Promise<void> {
	if (locale === "en" || hasCatalog(instance, locale)) return Promise.resolve();
	if (instance.services.backendConnector?.backend) return instance.loadLanguages(locale).then(() => undefined);
	const load = localeLoaders[locale];
	return load()
		.then((module) => {
			instance.addResourceBundle(locale, "translation", module.default, true, true);
		})
		.catch(() => undefined);
}
