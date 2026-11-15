import { COMPANY } from "../lib/constants";

/**
 * Every language the docs interface has a catalog for. Articles exist in
 * only some of them; the languages the docs actually serve are English plus
 * those with translated articles (`translatedLocales()`).
 */
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
 * The first `name` cookie whose value is one of `allowed`, or null.
 *
 * The landing site and older docs builds can leave two `ao_locale` cookies,
 * one for `.orchestrator.inc` and one for the docs host alone, and the
 * browser sends both. Taking the first allowed value in header order keeps
 * the result deterministic and means an unserved value, such as the
 * landing's `fr`, never hides a served one. Values may be quoted; anything
 * else that is not exactly an allowed value is ignored.
 *
 * Self-contained, with no imports, so the root layout can inline it into
 * the head script.
 */
export function findCookieValue(cookie: string, name: string, allowed: readonly string[]): string | null {
  const parts = cookie.split(";");
  for (let i = 0; i < parts.length; i++) {
    const separator = parts[i].indexOf("=");
    if (separator === -1 || parts[i].slice(0, separator).trim() !== name) continue;
    let value = parts[i].slice(separator + 1).trim();
    if (value.length > 1 && value.charAt(0) === '"' && value.charAt(value.length - 1) === '"') value = value.slice(1, -1);
    if (allowed.indexOf(value) > -1) return value;
  }
  return null;
}

/**
 * The locale saved in the cookie when the docs serve it, or English. The
 * landing site shares the cookie and offers languages the docs do not have,
 * so `served` is the docs' own list, not every interface catalog.
 */
export function readLocaleCookie(cookie: string, served: readonly SupportedLocale[]): SupportedLocale {
  const value = findCookieValue(cookie, LOCALE_COOKIE, served);
  return value && isSupportedLocale(value) ? value : DEFAULT_LOCALE;
}

/** The site the landing page and the docs share the language cookie on. */
export const SHARED_COOKIE_DOMAIN = new URL(COMPANY.MARKETING_URL).hostname;

const ONE_YEAR = 60 * 60 * 24 * 365;

/**
 * The `document.cookie` writes that save `locale` as the reader's choice.
 *
 * On orchestrator.inc and its subdomains the choice is saved for the whole
 * domain, the same scope the landing site uses, so it replaces the landing's
 * cookie instead of sitting beside it. The host-only cookie older docs
 * builds wrote is removed first. Anywhere else, such as localhost or a
 * preview host, a domain cookie would be rejected, so it stays host-only.
 */
export function localeCookieWrites(locale: SupportedLocale, location: { hostname: string; protocol: string }): string[] {
  const secure = location.protocol === "https:" ? "; secure" : "";
  const host = location.hostname.toLowerCase();
  const shared = host === SHARED_COOKIE_DOMAIN || host.endsWith(`.${SHARED_COOKIE_DOMAIN}`);
  const save = `${LOCALE_COOKIE}=${locale}; path=/; max-age=${ONE_YEAR}; samesite=lax${secure}`;
  if (!shared) return [save];
  return [`${LOCALE_COOKIE}=; path=/; max-age=0; samesite=lax${secure}`, `${save}; domain=${SHARED_COOKIE_DOMAIN}`];
}
