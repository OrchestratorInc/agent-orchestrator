import type { I18n } from "@lingui/core";
import { msg } from "@lingui/core/macro";
import type { DocsLayoutProps } from "fumadocs-ui/layouts/notebook";
import { buttonVariants } from "fumadocs-ui/components/ui/button";
import Link from "fumadocs-core/link";
import { Download } from "lucide-react";
import type { ComponentProps } from "react";
import { DiscordIcon, GitHubIcon } from "@/components/brand-icons";
import { LanguageSelect, LanguageSelectText } from "@/components/language-select";
import { COMPANY } from "./constants";

const DOWNLOAD_BUTTON = buttonVariants({ color: "primary", size: "sm", className: "gap-1.5 [&_svg]:size-3.5" });

/** The layout options every page shares, minus the sidebar tree. */
export function baseOptions(i18n: I18n, homeUrl = "/"): Omit<DocsLayoutProps, "tree"> {
  const name = COMPANY.NAME;
  const download = i18n._(msg`Download`);

  // The logo goes to the product site and the "Docs" label to the docs home.
  function NavTitle({ href = homeUrl, className }: ComponentProps<"a">) {
    return (
      <span className={`${className ?? ""} text-sm font-medium text-foreground`}>
        <a href={COMPANY.MARKETING_URL} aria-label={i18n._(msg`${name} home`)} className="inline-flex shrink-0">
          <img src={`${COMPANY.MARKETING_URL}/ao-logo.svg`} alt="" width={20} height={20} className="size-5" />
        </a>
        <Link href={href}>{i18n._(msg`Docs`)}</Link>
      </span>
    );
  }

  return {
    nav: {
      mode: "top",
      url: homeUrl,
      title: NavTitle,
      // Below the desktop breakpoint the navbar links move into the menu, but
      // Download stays in the header.
      children: (
        <a href={COMPANY.DOWNLOAD_URL} target="_blank" rel="noreferrer noopener" className={`${DOWNLOAD_BUTTON} ms-auto lg:hidden`}>
          <Download />
          {download}
        </a>
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
        text: i18n._(msg`Changelog`),
        url: COMPANY.CHANGELOG_URL,
        external: true,
      },
      {
        type: "custom",
        on: "nav",
        children: (
          <a href={COMPANY.DOWNLOAD_URL} target="_blank" rel="noreferrer noopener" className={DOWNLOAD_BUTTON}>
            <Download />
            {download}
          </a>
        ),
      },
      {
        on: "menu",
        text: download,
        url: COMPANY.DOWNLOAD_URL,
        external: true,
      },
      {
        type: "icon",
        label: "Discord",
        text: "Discord",
        icon: <DiscordIcon />,
        url: COMPANY.DISCORD_URL,
        external: true,
      },
      {
        type: "icon",
        label: "GitHub",
        text: "GitHub",
        icon: <GitHubIcon />,
        url: COMPANY.GITHUB_URL,
        external: true,
      },
    ],
  };
}
