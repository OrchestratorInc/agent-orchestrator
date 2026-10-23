import type { I18n } from "@lingui/core";
import { msg } from "@lingui/core/macro";
import type { BaseLayoutProps } from "fumadocs-ui/layouts/shared";
import { LanguageSelect, LanguageSelectText } from "@/components/language-select";
import { COMPANY } from "./constants";

export function baseOptions(i18n: I18n): BaseLayoutProps {
  const name = COMPANY.NAME;
  return {
    nav: {
      url: "/",
      title: (
        <span className="inline-flex items-center gap-2 text-sm font-medium text-foreground">
          <img
            src={`${COMPANY.MARKETING_URL}/ao-logo.svg`}
            alt=""
            width={20}
            height={20}
            aria-hidden="true"
            className="size-5 shrink-0"
          />
          <span>{i18n._(msg`${name} Docs`)}</span>
        </span>
      ),
    },
    themeSwitch: {
      enabled: false,
    },
    slots: {
      languageSelect: { root: LanguageSelect, text: LanguageSelectText },
    },
    links: [
      {
        text: i18n._(msg`Main site`),
        url: COMPANY.MARKETING_URL,
      },
      {
        text: "GitHub",
        url: COMPANY.GITHUB_URL,
      },
    ],
  };
}
