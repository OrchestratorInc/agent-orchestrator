import type { AppLocale } from "../../shared/ui-locale";

export { APP_LOCALES, DEFAULT_LOCALE, coerceLocale } from "../../shared/ui-locale";
export type { AppLocale } from "../../shared/ui-locale";

export function documentLang(locale: AppLocale): string {
	return locale;
}
