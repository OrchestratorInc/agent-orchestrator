import { docs } from "collections/server";
import { loader } from "fumadocs-core/source";
import { DEFAULT_LOCALE, SUPPORTED_LOCALES } from "@/i18n/locales";

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
