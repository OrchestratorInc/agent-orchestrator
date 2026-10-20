import { COMPANY } from "@/lib/constants";
import {
  DEFAULT_LOCALE,
  isSupportedLocale,
  localizePath,
  type SupportedLocale,
} from "./locales";

/**
 * Every page shares one optional catch-all route, so the locale is the first
 * segment when it names a prefixed locale. English has no prefix.
 */
export function splitSlug(slug: string[] = []): { locale: SupportedLocale; slug: string[] } {
  const [first, ...rest] = slug;
  if (first && first !== DEFAULT_LOCALE && isSupportedLocale(first)) {
    return { locale: first, slug: rest };
  }
  return { locale: DEFAULT_LOCALE, slug };
}

/** Catch-all params for a page in a locale. */
export function joinSlug(locale: SupportedLocale, slug: string[]): string[] {
  return locale === DEFAULT_LOCALE ? slug : [locale, ...slug];
}

/** Absolute URL of a same-site path in a locale, with the site's trailing slash. */
export function localeUrl(locale: SupportedLocale, path: string): string {
  const withSlash = path.endsWith("/") ? path : `${path}/`;
  return `${COMPANY.DOCS_URL}${localizePath(locale, withSlash)}`;
}

/**
 * Canonical and hreflang links. `translatedLocales` are the locales whose copy
 * of the page has its body in that language. A page whose body exists only in
 * English keeps the English canonical and names no alternates.
 */
export function localizedAlternates(
  locale: SupportedLocale,
  path: string,
  translatedLocales: readonly SupportedLocale[],
) {
  if (!translatedLocales.includes(locale)) return { canonical: localeUrl(DEFAULT_LOCALE, path) };
  if (translatedLocales.length < 2) return { canonical: localeUrl(locale, path) };
  const languages: Record<string, string> = {
    "x-default": localeUrl(DEFAULT_LOCALE, path),
  };
  for (const option of translatedLocales) {
    languages[option] = localeUrl(option, path);
  }
  return { canonical: localeUrl(locale, path), languages };
}
