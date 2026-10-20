export const SUPPORTED_LOCALES = [
  "en",
  "ja",
  "zh-CN",
  "zh-TW",
  "ko",
  "fr",
  "es",
  "de",
  "pt-BR",
  "it",
  "ru",
  "tr",
  "pl",
  "nl",
  "id",
  "cs",
  "vi",
] as const;

export type SupportedLocale = (typeof SUPPORTED_LOCALES)[number];

export const DEFAULT_LOCALE: SupportedLocale = "en";

/** Locales served under a path prefix. English keeps the bare URLs. */
export const PREFIXED_LOCALES = SUPPORTED_LOCALES.filter(
  (locale): locale is Exclude<SupportedLocale, "en"> => locale !== DEFAULT_LOCALE,
);

// Each name is in its own language, so a reader who cannot read the current
// page can still find their language. Never translate these.
export const LOCALE_LABELS: Record<SupportedLocale, string> = {
  en: "English",
  ja: "日本語",
  "zh-CN": "简体中文",
  "zh-TW": "繁體中文",
  ko: "한국어",
  fr: "Français",
  es: "Español",
  de: "Deutsch",
  "pt-BR": "Português (Brasil)",
  it: "Italiano",
  ru: "Русский",
  tr: "Türkçe",
  pl: "Polski",
  nl: "Nederlands",
  id: "Bahasa Indonesia",
  cs: "Čeština",
  vi: "Tiếng Việt",
};

export function isSupportedLocale(value: string): value is SupportedLocale {
  return (SUPPORTED_LOCALES as readonly string[]).includes(value);
}

/** Splits a pathname into its locale and the path without the prefix. */
export function splitLocalePath(pathname: string): {
  locale: SupportedLocale;
  path: string;
} {
  const [, first = "", ...rest] = pathname.split("/");
  if (isSupportedLocale(first) && first !== DEFAULT_LOCALE) {
    return { locale: first, path: `/${rest.join("/")}` };
  }
  return { locale: DEFAULT_LOCALE, path: pathname || "/" };
}

/** Same-site path for a locale: bare for English, prefixed otherwise. */
export function localizePath(locale: SupportedLocale, path: string): string {
  const normalized = path.startsWith("/") ? path : `/${path}`;
  if (locale === DEFAULT_LOCALE) return normalized;
  return normalized === "/" ? `/${locale}/` : `/${locale}${normalized}`;
}
