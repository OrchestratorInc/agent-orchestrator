import { DEFAULT_LOCALE, SUPPORTED_LOCALES, type SupportedLocale } from "@/i18n/locales";
import { localePath, splitLocalePath, type Translations, withTrailingSlash } from "@/i18n/paths";
import { source } from "./source";

export function articlePath(slugs: string[]): string {
  return slugs.length ? `/${slugs.join("/")}/` : "/";
}

export function articleLocales(slugs: string[]): SupportedLocale[] {
  return SUPPORTED_LOCALES.filter((locale) => source.getPage(slugs, locale));
}

export function translations(): Translations {
  const result: Translations = {};
  for (const page of source.getPages(DEFAULT_LOCALE)) {
    const locales = articleLocales(page.slugs).filter((locale) => locale !== DEFAULT_LOCALE);
    if (locales.length) result[articlePath(page.slugs)] = locales;
  }
  return result;
}

export function translatedLocales(): SupportedLocale[] {
  return SUPPORTED_LOCALES.filter((locale) => locale !== DEFAULT_LOCALE && source.getPages(locale).length > 0);
}

export function localizeHref(href: string | undefined, locale: SupportedLocale): string | undefined {
  if (!href || locale === DEFAULT_LOCALE || !href.startsWith("/") || href.startsWith("//")) return href;
  const [pathname = "", hash] = href.split("#", 2);
  const { path } = splitLocalePath(pathname);
  const slugs = withTrailingSlash(path).split("/").filter(Boolean);
  if (!source.getPage(slugs, locale)) return href;
  return hash === undefined ? localePath(path, locale) : `${localePath(path, locale)}#${hash}`;
}
