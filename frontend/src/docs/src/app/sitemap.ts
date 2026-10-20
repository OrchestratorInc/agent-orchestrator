import type { MetadataRoute } from "next";
import { source } from "@/lib/source";
import { translatedLocales } from "@/lib/translated";
import { DEFAULT_LOCALE } from "@/i18n/locales";
import { localeUrl, localizedAlternates } from "@/i18n/routing";

export const dynamic = "force-static";

/**
 * Each page once per locale that has its body translated, with hreflang
 * alternates. Pages whose body is English only are listed at their English URL.
 */
export default function sitemap(): MetadataRoute.Sitemap {
  return source.getPages(DEFAULT_LOCALE).flatMap((page) => {
    const path = `/${page.slugs.join("/")}`;
    const locales = translatedLocales(page.slugs);
    return locales.map((locale) => {
      const { languages } = localizedAlternates(locale, path, locales);
      return languages ? { url: localeUrl(locale, path), alternates: { languages } } : { url: localeUrl(locale, path) };
    });
  });
}
