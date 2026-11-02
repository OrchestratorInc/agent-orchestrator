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

/** Records an explicit choice from the language switcher. */
export const LOCALE_COOKIE = "ao_locale";

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

/**
 * Finds a cookie value that is one of `allowed`. Self-contained, with no
 * imports, so the root layout can inline it into the head script.
 */
export function findCookieValue(cookie: string, name: string, allowed: readonly string[]): string | null {
  const parts = cookie.split(";");
  for (let i = 0; i < parts.length; i++) {
    const separator = parts[i].indexOf("=");
    if (separator === -1 || parts[i].slice(0, separator).trim() !== name) continue;
    const value = parts[i].slice(separator + 1).trim();
    return allowed.indexOf(value) > -1 ? value : null;
  }
  return null;
}

/** The locale saved by the language switcher, or English when none is saved. */
export function readLocaleCookie(cookie: string): SupportedLocale {
  const value = findCookieValue(cookie, LOCALE_COOKIE, SUPPORTED_LOCALES);
  return value && isSupportedLocale(value) ? value : DEFAULT_LOCALE;
}
