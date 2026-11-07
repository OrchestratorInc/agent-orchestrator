"use client";

import { msg } from "@lingui/core/macro";
import { useLingui } from "@lingui/react";
import Link from "fumadocs-core/link";
import { COMPANY } from "@/lib/constants";

type FooterLink = { label: string; href: string; external?: boolean };

/**
 * The footer under every page, with the same Product, Community and Legal
 * links as the landing site. `homeUrl` is the docs home in the page's language.
 */
export function SiteFooter({ homeUrl = "/" }: { homeUrl?: string }) {
  const { i18n } = useLingui();
  const name = COMPANY.NAME;
  const columns: { title: string; links: FooterLink[] }[] = [
    {
      title: i18n._(msg`Product`),
      links: [
        { label: i18n._(msg`Download`), href: COMPANY.DOWNLOAD_URL, external: true },
        { label: i18n._(msg`Changelog`), href: COMPANY.CHANGELOG_URL, external: true },
        { label: i18n._(msg`Docs`), href: homeUrl },
      ],
    },
    {
      title: i18n._(msg`Community`),
      links: [
        { label: "GitHub", href: COMPANY.GITHUB_URL, external: true },
        { label: "Discord", href: COMPANY.DISCORD_URL, external: true },
        { label: "X", href: COMPANY.X_URL, external: true },
      ],
    },
    {
      title: i18n._(msg`Legal`),
      links: [
        { label: i18n._(msg`Privacy`), href: COMPANY.PRIVACY_URL, external: true },
        { label: i18n._(msg`Refunds & Cancellations`), href: COMPANY.REFUNDS_URL, external: true },
        { label: i18n._(msg`Support & Contact`), href: COMPANY.SUPPORT_URL, external: true },
        { label: i18n._(msg`Apache-2.0 License`), href: COMPANY.LICENSE_URL, external: true },
      ],
    },
  ];

  return (
    // Styled in global.css: the docs only ship the utility classes the docs
    // framework uses, and this needs a grid that the framework has no class for.
    <footer className="docs-footer">
      <div className="docs-footer-inner">
        <div className="docs-footer-brand">
          <a href={COMPANY.MARKETING_URL} aria-label={i18n._(msg`${name} home`)}>
            <img src={`${COMPANY.MARKETING_URL}/ao-logo.svg`} alt="" width={20} height={20} />
            <span>{name}</span>
          </a>
          <p suppressHydrationWarning>
            © {new Date().getFullYear()} {COMPANY.LEGAL_NAME}
          </p>
        </div>
        {columns.map((column) => (
          <nav key={column.title} aria-label={column.title}>
            <h2>{column.title}</h2>
            <ul>
              {column.links.map((link) => (
                <li key={link.href}>
                  {link.external ? (
                    <a href={link.href} target="_blank" rel="noreferrer noopener">
                      {link.label}
                    </a>
                  ) : (
                    <Link href={link.href}>{link.label}</Link>
                  )}
                </li>
              ))}
            </ul>
          </nav>
        ))}
      </div>
    </footer>
  );
}
