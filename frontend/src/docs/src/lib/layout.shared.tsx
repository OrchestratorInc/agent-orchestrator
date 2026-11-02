import type { I18n } from "@lingui/core";
import { msg } from "@lingui/core/macro";
import type { BaseLayoutProps } from "fumadocs-ui/layouts/shared";
import { LanguageSelect, LanguageSelectText } from "@/components/language-select";
import { COMPANY } from "./constants";

export function baseOptions(i18n: I18n, homeUrl = "/"): BaseLayoutProps {
  return {
    nav: {
      url: homeUrl,
      title: (
        <span className="inline-flex items-center gap-2 text-sm font-medium text-foreground">
          {/* The logo carries the company name, so the label only says "Docs". */}
          <img
            src={`${COMPANY.MARKETING_URL}/ao-logo.svg`}
            alt={COMPANY.NAME}
            width={20}
            height={20}
            className="size-5 shrink-0"
          />
          <span>{i18n._(msg`Docs`)}</span>
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
