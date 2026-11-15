import { findCookieValue, LOCALE_COOKIE, type SupportedLocale } from "./locales";
import type { Translations } from "./paths";

// Runs before first paint on English pages. When the saved language has a
// translation of the page, goes there, keeping the query and section;
// otherwise sets <html lang> from the
// saved language, ahead of hydration, so assistive tech and the browser see
// the right language. Only languages in `served` count, so a language the
// landing site saved but the docs lack leaves the page in English. It
// inlines the same parser the interface uses to read the cookie.
export function savedLocaleScript(translated: Translations, served: readonly SupportedLocale[]): string {
  return `try{var l=(${findCookieValue.toString()})(document.cookie,${JSON.stringify(LOCALE_COOKIE)},${JSON.stringify(served)});if(l){var p=location.pathname.replace(/\\/?$/,"/");var t=${JSON.stringify(translated)}[p];if(t&&t.indexOf(l)>-1)location.replace("/"+l+p+location.search+location.hash);else document.documentElement.lang=l}}catch(e){}`;
}
