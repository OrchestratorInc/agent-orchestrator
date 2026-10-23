import { GeistSans } from "geist/font/sans";
import type { Metadata } from "next";
import type { ReactNode } from "react";
import { DEFAULT_LOCALE, type SupportedLocale } from "@/i18n/locales";
import { localePath } from "@/i18n/paths";
import { savedLocaleScript } from "@/i18n/saved-locale-script";
import { COMPANY } from "@/lib/constants";
import { docsTree } from "@/lib/docs-tree";
import { source } from "@/lib/source";
import { translations } from "@/lib/translations";
import { LocalizedDocsLayout } from "./docs-layout";
import { Providers } from "./providers";
import "../app/global.css";

export const siteMetadata: Metadata = {
  metadataBase: new URL(COMPANY.DOCS_URL),
  icons: {
    icon: [{ url: `${COMPANY.MARKETING_URL}/favicon.svg`, type: "image/svg+xml" }],
  },
};

/**
 * The page shell. English pages are one tree whose interface language comes
 * from the saved choice. Each language with translated articles has its own
 * tree under `/<locale>/`, prerendered in that language.
 */
export function siteLayout(locale: SupportedLocale) {
  return function Layout({ children }: { children: ReactNode }) {
    const translated = translations();
    const isEnglish = locale === DEFAULT_LOCALE;
    return (
      <html
        lang={locale}
        className={`dark overscroll-none ${GeistSans.variable}`}
        suppressHydrationWarning
      >
        <head>
          {isEnglish && <script dangerouslySetInnerHTML={{ __html: savedLocaleScript(translated) }} />}
        </head>
        <body className="min-h-screen bg-background text-foreground antialiased">
          <Providers urlLocale={isEnglish ? undefined : locale} translations={translated}>
            <LocalizedDocsLayout tree={docsTree(locale)} homeUrl={source.getPage([], locale) ? localePath("/", locale) : "/"}>
              {children}
            </LocalizedDocsLayout>
          </Providers>
        </body>
      </html>
    );
  };
}
