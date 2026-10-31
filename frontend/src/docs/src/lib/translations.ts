import { DEFAULT_LOCALE, SUPPORTED_LOCALES, type SupportedLocale } from "@/i18n/locales";
import { localePath, splitLocalePath, type Translations, withTrailingSlash } from "@/i18n/paths";
import { source } from "./source";

/** The English path of an article, e.g. `/quickstart/`. */
export function articlePath(slugs: string[]): string {
  return slugs.length ? `/${slugs.join("/")}/` : "/";
}

/** The languages an article exists in, English first. */
export function articleLocales(slugs: string[]): SupportedLocale[] {
  return SUPPORTED_LOCALES.filter((locale) => source.getPage(slugs, locale));
}

/** Every translated article, keyed by its English path. Untranslated articles are left out. */
export function translations(): Translations {
  const result: Translations = {};
  for (const page of source.getPages(DEFAULT_LOCALE)) {
    const locales = articleLocales(page.slugs).filter((locale) => locale !== DEFAULT_LOCALE);
    if (locales.length) result[articlePath(page.slugs)] = locales;
  }
  return result;
}

/** The languages that have at least one translated article. */
export function translatedLocales(): SupportedLocale[] {
  return SUPPORTED_LOCALES.filter((locale) => locale !== DEFAULT_LOCALE && source.getPages(locale).length > 0);
}

/**
 * Points a link in a translated article at the translation of its target
 * when there is one. Links to untranslated articles keep their English URL.
 */
export function localizeHref(href: string | undefined, locale: SupportedLocale): string | undefined {
  if (!href || locale === DEFAULT_LOCALE || !href.startsWith("/") || href.startsWith("//")) return href;
  const [pathname = "", hash] = href.split("#", 2);
  const { path } = splitLocalePath(pathname);
  const slugs = withTrailingSlash(path).split("/").filter(Boolean);
  if (!source.getPage(slugs, locale)) return href;
  return hash === undefined ? localePath(path, locale) : `${localePath(path, locale)}#${hash}`;
}
