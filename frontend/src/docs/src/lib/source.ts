import { docs } from "collections/server";
import { defineI18n } from "fumadocs-core/i18n";
import { loader } from "fumadocs-core/source";
import { DEFAULT_LOCALE, SUPPORTED_LOCALES } from "@/i18n/locales";

export const i18n = defineI18n({
  languages: [...SUPPORTED_LOCALES],
  defaultLanguage: DEFAULT_LOCALE,
  hideLocale: "default-locale",
  // Articles are English only for now. Each locale falls back to them under
  // its own prefix; a translation would live next to it as `page.<locale>.mdx`.
});

export const source = loader({
  baseUrl: "/",
  i18n,
  source: docs.toFumadocsSource(),
});
