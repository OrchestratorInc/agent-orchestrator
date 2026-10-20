import { DEFAULT_LOCALE, SUPPORTED_LOCALES, type SupportedLocale } from "@/i18n/locales";
import { source } from "./source";

/** True when the locale has its own copy of the article, not the English fallback. */
export function hasTranslatedBody(locale: SupportedLocale, slug: string[]): boolean {
  if (locale === DEFAULT_LOCALE) return true;
  const page = source.getPage(slug, locale);
  return page !== undefined && page.data !== source.getPage(slug, DEFAULT_LOCALE)?.data;
}

/** Locales whose copy of the page has its body in that language, English first. */
export function translatedLocales(slug: string[]): SupportedLocale[] {
  return SUPPORTED_LOCALES.filter((locale) => hasTranslatedBody(locale, slug));
}
