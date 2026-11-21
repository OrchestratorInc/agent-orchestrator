import { DEFAULT_LOCALE, isSupportedLocale, type SupportedLocale } from "./locales";

/** The English path of each translated article, and the languages it is translated into. */
export type Translations = Record<string, SupportedLocale[]>;

/** Adds the trailing slash every exported page URL has. */
export function withTrailingSlash(path: string): string {
  return path.endsWith("/") ? path : `${path}/`;
}

/** The URL of an article in a language. English articles have no prefix. */
export function localePath(englishPath: string, locale: SupportedLocale): string {
  const path = withTrailingSlash(englishPath);
  return locale === DEFAULT_LOCALE ? path : `/${locale}${path}`;
}

/** Splits a translated URL such as `/ja/quickstart/` into its language and English path. */
export function splitLocalePath(pathname: string): { locale: SupportedLocale; path: string } {
  const path = withTrailingSlash(pathname);
  const [, first = "", ...rest] = path.split("/");
  if (first !== DEFAULT_LOCALE && isSupportedLocale(first)) {
    return { locale: first, path: `/${rest.join("/")}` };
  }
  return { locale: DEFAULT_LOCALE, path };
}
