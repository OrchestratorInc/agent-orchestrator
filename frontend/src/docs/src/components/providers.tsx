"use client";

import { RootProvider } from "fumadocs-ui/provider/next";
import { usePathname, useRouter } from "next/navigation";
import type { ReactNode } from "react";
import { LocalizedSearchDialog } from "./search-dialog";
import {
  LOCALE_LABELS,
  localizePath,
  splitLocalePath,
  SUPPORTED_LOCALES,
  type SupportedLocale,
} from "@/i18n/locales";

const LOCALE_ITEMS = SUPPORTED_LOCALES.map((locale) => ({ locale, name: LOCALE_LABELS[locale] }));

export function Providers({
  locale,
  translations,
  children,
}: {
  locale: SupportedLocale;
  translations: Record<string, string>;
  children: ReactNode;
}) {
  const router = useRouter();
  const pathname = usePathname();

  return (
    <RootProvider
      theme={{ enabled: false }}
      search={{ SearchDialog: LocalizedSearchDialog }}
      i18n={{
        locale,
        locales: LOCALE_ITEMS,
        translations,
        // The same page in the chosen language. English drops the prefix.
        onLocaleChange: (next) => {
          const { path } = splitLocalePath(pathname);
          router.push(localizePath(next as SupportedLocale, path));
        },
      }}
    >
      {children}
    </RootProvider>
  );
}
