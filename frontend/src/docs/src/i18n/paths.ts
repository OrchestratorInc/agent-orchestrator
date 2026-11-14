import { DEFAULT_LOCALE, isSupportedLocale, type SupportedLocale } from "./locales";

export type Translations = Record<string, SupportedLocale[]>;

export function withTrailingSlash(path: string): string {
  return path.endsWith("/") ? path : `${path}/`;
}

export function localePath(englishPath: string, locale: SupportedLocale): string {
  const path = withTrailingSlash(englishPath);
  return locale === DEFAULT_LOCALE ? path : `/${locale}${path}`;
}

export function splitLocalePath(pathname: string): { locale: SupportedLocale; path: string } {
  const path = withTrailingSlash(pathname);
  const [, first = "", ...rest] = path.split("/");
  if (first !== DEFAULT_LOCALE && isSupportedLocale(first)) {
    return { locale: first, path: `/${rest.join("/")}` };
  }
  return { locale: DEFAULT_LOCALE, path };
}
