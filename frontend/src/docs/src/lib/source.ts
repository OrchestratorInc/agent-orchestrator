import { docs } from "collections/server";
import { loader } from "fumadocs-core/source";
import { DEFAULT_LOCALE, SUPPORTED_LOCALES } from "@/i18n/locales";

// An English article is `page.mdx`, and a translation sits beside it as
// `page.ja.mdx`. There is no fallback, so a language only has the articles
// that are actually translated into it.
export const source = loader({
  baseUrl: "/",
  source: docs.toFumadocsSource(),
  i18n: {
    languages: [...SUPPORTED_LOCALES],
    defaultLanguage: DEFAULT_LOCALE,
    hideLocale: "default-locale",
    fallbackLanguage: null,
    parser: "dot",
  },
});
