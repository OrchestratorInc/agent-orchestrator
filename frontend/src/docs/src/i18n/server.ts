import { type I18n, setupI18n } from "@lingui/core";
import { setI18n } from "@lingui/react/server";
import { getLocaleMessages } from "./catalogs";
import type { SupportedLocale } from "./locales";

// One instance per locale. Never activate another locale on a cached
// instance: setI18n binds the instance to the current render only.
const instances = new Map<SupportedLocale, Promise<I18n>>();

export function getI18nInstance(locale: SupportedLocale): Promise<I18n> {
  let instance = instances.get(locale);
  if (!instance) {
    instance = getLocaleMessages(locale).then((messages) =>
      setupI18n({ locale, messages: { [locale]: messages } }),
    );
    instances.set(locale, instance);
  }
  return instance;
}

/**
 * Binds the locale to this server render. Await it in every layout and page:
 * layouts do not re-render on client navigation.
 */
export async function initServerI18n(locale: SupportedLocale): Promise<I18n> {
  const i18n = await getI18nInstance(locale);
  setI18n(i18n);
  return i18n;
}
