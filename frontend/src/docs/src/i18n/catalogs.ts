import type { Messages } from "@lingui/core";
import { messages as cs } from "../../locales/cs/messages";
import { messages as de } from "../../locales/de/messages";
import { messages as en } from "../../locales/en/messages";
import { messages as es } from "../../locales/es/messages";
import { messages as fr } from "../../locales/fr/messages";
import { messages as id } from "../../locales/id/messages";
import { messages as it } from "../../locales/it/messages";
import { messages as ja } from "../../locales/ja/messages";
import { messages as ko } from "../../locales/ko/messages";
import { messages as nl } from "../../locales/nl/messages";
import { messages as pl } from "../../locales/pl/messages";
import { messages as ptBR } from "../../locales/pt-BR/messages";
import { messages as ru } from "../../locales/ru/messages";
import { messages as tr } from "../../locales/tr/messages";
import { messages as vi } from "../../locales/vi/messages";
import { messages as zhCN } from "../../locales/zh-CN/messages";
import { messages as zhTW } from "../../locales/zh-TW/messages";
import type { SupportedLocale } from "./locales";

// The interface catalogs are a few dozen strings each, so every locale ships
// with the page and switching languages never waits on a request.
export const CATALOGS: Record<SupportedLocale, Messages> = {
  en,
  ja,
  "zh-CN": zhCN,
  "zh-TW": zhTW,
  ko,
  fr,
  es,
  de,
  "pt-BR": ptBR,
  it,
  ru,
  tr,
  pl,
  nl,
  id,
  cs,
  vi,
};
