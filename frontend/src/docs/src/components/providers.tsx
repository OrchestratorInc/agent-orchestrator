"use client";

import { setupI18n } from "@lingui/core";
import { I18nProvider } from "@lingui/react";
import { RootProvider } from "fumadocs-ui/provider/next";
import { type ReactNode, useCallback, useEffect, useMemo, useState } from "react";
import { CATALOGS } from "@/i18n/catalogs";
import {
  DEFAULT_LOCALE,
  isSupportedLocale,
  LOCALE_COOKIE,
  LOCALE_LABELS,
  readLocaleCookie,
  SUPPORTED_LOCALES,
  type SupportedLocale,
} from "@/i18n/locales";
import { frameworkTranslations } from "@/i18n/ui-messages";
import { DocsSearchDialog } from "./search-dialog";

const LOCALE_ITEMS = SUPPORTED_LOCALES.map((locale) => ({ locale, name: LOCALE_LABELS[locale] }));
const ONE_YEAR = 60 * 60 * 24 * 365;

/**
 * Every page has one URL and is prerendered in English. The reader's saved
 * language is applied after hydration, and the switcher changes it in place.
 */
export function Providers({ children }: { children: ReactNode }) {
  // Null until the cookie is read. Renders in English meanwhile, so the
  // first render matches the prerendered HTML.
  const [saved, setSaved] = useState<SupportedLocale | null>(null);
  const locale = saved ?? DEFAULT_LOCALE;

  useEffect(() => {
    setSaved(readLocaleCookie(document.cookie));
  }, []);

  // The inline head script already set <html lang> from the cookie, so
  // only write it once the cookie has been read.
  useEffect(() => {
    if (saved) document.documentElement.lang = saved;
  }, [saved]);

  const i18n = useMemo(
    () => setupI18n({ locale, messages: { [locale]: CATALOGS[locale] } }),
    [locale],
  );
  const translations = useMemo(() => frameworkTranslations(i18n), [i18n]);

  const changeLocale = useCallback((next: string) => {
    if (!isSupportedLocale(next)) return;
    document.cookie = `${LOCALE_COOKIE}=${next}; path=/; max-age=${ONE_YEAR}; samesite=lax`;
    setSaved(next);
  }, []);

  return (
    <I18nProvider i18n={i18n}>
      <RootProvider
        theme={{ enabled: false }}
        search={{ SearchDialog: DocsSearchDialog }}
        i18n={{
          locale,
          locales: LOCALE_ITEMS,
          translations,
          onLocaleChange: changeLocale,
        }}
      >
        {children}
      </RootProvider>
    </I18nProvider>
  );
}
