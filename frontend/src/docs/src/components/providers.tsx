"use client";

import { setupI18n } from "@lingui/core";
import { I18nProvider } from "@lingui/react";
import { RootProvider } from "fumadocs-ui/provider/next";
import { usePathname } from "next/navigation";
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
import { localePath, splitLocalePath, type Translations, withTrailingSlash } from "@/i18n/paths";
import { frameworkTranslations } from "@/i18n/ui-messages";
import { DocsSearchDialog } from "./search-dialog";

function withLocationSuffix(path: string): string {
  return `${path}${window.location.search}${window.location.hash}`;
}

const LOCALE_ITEMS = SUPPORTED_LOCALES.map((locale) => ({ locale, name: LOCALE_LABELS[locale] }));
const ONE_YEAR = 60 * 60 * 24 * 365;

export function Providers({
  urlLocale,
  translations = {},
  children,
}: {
  urlLocale?: SupportedLocale;
  translations?: Translations;
  children: ReactNode;
}) {
  const pathname = usePathname();
  const [saved, setSaved] = useState<SupportedLocale | null>(null);
  const locale = urlLocale ?? saved ?? DEFAULT_LOCALE;

  useEffect(() => {
    if (!urlLocale) setSaved(readLocaleCookie(document.cookie));
  }, [urlLocale]);

  useEffect(() => {
    if (saved) document.documentElement.lang = saved;
  }, [saved]);

  useEffect(() => {
    if (urlLocale || !saved || saved === DEFAULT_LOCALE) return;
    const path = withTrailingSlash(pathname);
    if (translations[path]?.includes(saved)) window.location.replace(withLocationSuffix(localePath(path, saved)));
  }, [urlLocale, saved, pathname, translations]);

  const i18n = useMemo(
    () => setupI18n({ locale, messages: { [locale]: CATALOGS[locale] } }),
    [locale],
  );
  const frameworkStrings = useMemo(() => frameworkTranslations(i18n), [i18n]);

  const changeLocale = useCallback(
    (next: string) => {
      if (!isSupportedLocale(next)) return;
      document.cookie = `${LOCALE_COOKIE}=${next}; path=/; max-age=${ONE_YEAR}; samesite=lax`;
      const { path } = splitLocalePath(pathname);
      if (next !== DEFAULT_LOCALE && translations[path]?.includes(next)) {
        window.location.assign(withLocationSuffix(localePath(path, next)));
      } else if (urlLocale) {
        window.location.assign(withLocationSuffix(path));
      } else {
        setSaved(next);
      }
    },
    [pathname, translations, urlLocale],
  );

  return (
    <I18nProvider i18n={i18n}>
      <RootProvider
        theme={{ enabled: false }}
        search={{ SearchDialog: DocsSearchDialog }}
        i18n={{
          locale,
          locales: LOCALE_ITEMS,
          translations: frameworkStrings,
          onLocaleChange: changeLocale,
        }}
      >
        {children}
      </RootProvider>
    </I18nProvider>
  );
}
