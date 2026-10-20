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
  // Starts in English so the first render matches the prerendered HTML.
  const [locale, setLocale] = useState<SupportedLocale>(DEFAULT_LOCALE);

  useEffect(() => {
    setLocale(readLocaleCookie(document.cookie));
  }, []);

  useEffect(() => {
    document.documentElement.lang = locale;
  }, [locale]);

  const i18n = useMemo(
    () => setupI18n({ locale, messages: { [locale]: CATALOGS[locale] } }),
    [locale],
  );

  const changeLocale = useCallback((next: string) => {
    if (!isSupportedLocale(next)) return;
    document.cookie = `${LOCALE_COOKIE}=${encodeURIComponent(next)}; path=/; max-age=${ONE_YEAR}; samesite=lax`;
    setLocale(next);
  }, []);

  return (
    <I18nProvider i18n={i18n}>
      <RootProvider
        theme={{ enabled: false }}
        search={{ SearchDialog: DocsSearchDialog }}
        i18n={{
          locale,
          locales: LOCALE_ITEMS,
          translations: frameworkTranslations(i18n),
          onLocaleChange: changeLocale,
        }}
      >
        {children}
      </RootProvider>
    </I18nProvider>
  );
}
