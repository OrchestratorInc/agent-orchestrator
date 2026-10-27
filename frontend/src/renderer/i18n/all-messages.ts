// Every locale catalog, eagerly. For tests and build tooling only: importing
// this from runtime code puts all 8 catalogs (~1.5 MB) back on the startup path.
import zhCN from "./zh-CN.json";
import ja from "./ja.json";
import ko from "./ko.json";
import es from "./es.json";
import fr from "./fr.json";
import de from "./de.json";
import ptBR from "./pt-BR.json";
import type { i18n } from "i18next";
import type { AppLocale } from "./locales";
import { DEFAULT_LOCALE } from "./locales";
import { createAppI18n, type TranslationCatalogs } from "./instance";
import { enMessages, type MessageCatalog } from "./messages";

export { enMessages };
export const zhCNMessages = zhCN;
export const jaMessages = ja;
export const koMessages = ko;
export const esMessages = es;
export const frMessages = fr;
export const deMessages = de;
export const ptBRMessages = ptBR;

export const appCatalogs: TranslationCatalogs = {
	en: enMessages,
	"zh-CN": zhCNMessages,
	ja: jaMessages,
	ko: koMessages,
	es: esMessages,
	fr: frMessages,
	de: deMessages,
	"pt-BR": ptBRMessages,
};

export function catalogFor(locale: AppLocale): MessageCatalog {
	return (appCatalogs[locale] ?? appCatalogs.en) as unknown as MessageCatalog;
}

/** A synchronously initialized instance with every catalog loaded. */
export function createFullAppI18n(locale: AppLocale = DEFAULT_LOCALE, catalogs: TranslationCatalogs = appCatalogs): i18n {
	return createAppI18n(locale, catalogs);
}
