import type { MetadataRoute } from "next";
import { source } from "@/lib/source";
import { DEFAULT_LOCALE, SUPPORTED_LOCALES } from "@/i18n/locales";
import { localeUrl, localizedAlternates } from "@/i18n/routing";

export const dynamic = "force-static";

/** Every page in every locale, each listing its other language versions. */
export default function sitemap(): MetadataRoute.Sitemap {
  return SUPPORTED_LOCALES.flatMap((locale) =>
    source.getPages(locale).map((page) => {
      const path = `/${page.slugs.join("/")}`;
      return {
        url: localeUrl(locale, path),
        alternates: { languages: localizedAlternates(DEFAULT_LOCALE, path).languages },
      };
    }),
  );
}
