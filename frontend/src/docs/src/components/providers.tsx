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

/**
 * The URL of `path` with the current query and section, so a reader moved to
 * another language keeps both. Each language has its own root layout, so the
 * move is a full page load, and the browser then scrolls to the section.
 */
function withLocationSuffix(path: string): string {
  return `${path}${window.location.search}${window.location.hash}`;
}

const LOCALE_ITEMS = SUPPORTED_LOCALES.map((locale) => ({ locale, name: LOCALE_LABELS[locale] }));
const ONE_YEAR = 60 * 60 * 24 * 365;

/**
 * A translated article has its own URL, and `urlLocale` is its language. The
 * interface follows the URL there, whatever language is saved.
 *
 * Every other page has one URL and is prerendered in English. The saved
 * language is applied after hydration, and the switcher changes it in place.
 * When the saved language has a translation of the page, the reader is sent
 * to it.
 */
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
  // Null until the cookie is read. Renders in English meanwhile, so the
  // first render matches the prerendered HTML.
  const [saved, setSaved] = useState<SupportedLocale | null>(null);
  const locale = urlLocale ?? saved ?? DEFAULT_LOCALE;

  useEffect(() => {
    if (!urlLocale) setSaved(readLocaleCookie(document.cookie));
  }, [urlLocale]);

  // The inline head script already set <html lang> from the cookie, so
  // only write it once the cookie has been read.
  useEffect(() => {
    if (saved) document.documentElement.lang = saved;
  }, [saved]);

  // The head script handles the first page load; this handles navigation
  // to a translated article inside the site.
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
        // No translation in that language, so go to the English article.
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
