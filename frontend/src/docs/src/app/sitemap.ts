import type { MetadataRoute } from "next";
import { DEFAULT_LOCALE } from "@/i18n/locales";
import { localePath } from "@/i18n/paths";
import { COMPANY } from "@/lib/constants";
import { source } from "@/lib/source";
import { articleLocales, articlePath } from "@/lib/translations";

export const dynamic = "force-static";

/**
 * One entry per article URL. Untranslated articles have one URL; a translated
 * article has one per language, each listing the others as alternates.
 */
export default function sitemap(): MetadataRoute.Sitemap {
  return source.getPages(DEFAULT_LOCALE).flatMap((page) => {
    const englishPath = articlePath(page.slugs);
    const locales = articleLocales(page.slugs);
    if (locales.length === 1) return [{ url: `${COMPANY.DOCS_URL}${englishPath}` }];

    const languages = {
      ...Object.fromEntries(locales.map((locale) => [locale, `${COMPANY.DOCS_URL}${localePath(englishPath, locale)}`])),
      "x-default": `${COMPANY.DOCS_URL}${englishPath}`,
    };
    return locales.map((locale) => ({
      url: `${COMPANY.DOCS_URL}${localePath(englishPath, locale)}`,
      alternates: { languages },
    }));
  });
}
