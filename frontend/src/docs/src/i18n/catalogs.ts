import type { Messages } from "@lingui/core";
import { messages as enMessages } from "../../locales/en/messages";
import type { SupportedLocale } from "./locales";

// Bundlers only follow import() calls they can resolve statically, so the map
// is spelled out.
const CATALOGS: Record<SupportedLocale, () => Promise<{ messages: Messages }>> = {
  en: async () => ({ messages: enMessages }),
  ja: () => import("../../locales/ja/messages"),
  "zh-CN": () => import("../../locales/zh-CN/messages"),
  "zh-TW": () => import("../../locales/zh-TW/messages"),
  ko: () => import("../../locales/ko/messages"),
  fr: () => import("../../locales/fr/messages"),
  es: () => import("../../locales/es/messages"),
  de: () => import("../../locales/de/messages"),
  "pt-BR": () => import("../../locales/pt-BR/messages"),
  it: () => import("../../locales/it/messages"),
  ru: () => import("../../locales/ru/messages"),
  tr: () => import("../../locales/tr/messages"),
  pl: () => import("../../locales/pl/messages"),
  nl: () => import("../../locales/nl/messages"),
  id: () => import("../../locales/id/messages"),
  cs: () => import("../../locales/cs/messages"),
  vi: () => import("../../locales/vi/messages"),
};

export async function getLocaleMessages(locale: SupportedLocale): Promise<Messages> {
  return (await CATALOGS[locale]()).messages;
}
