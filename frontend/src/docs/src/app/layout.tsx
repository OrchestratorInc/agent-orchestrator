import { GeistSans } from "geist/font/sans";
import type { Metadata } from "next";
import type { ReactNode } from "react";
import { Providers } from "@/components/providers";
import { findCookieValue, LOCALE_COOKIE, SUPPORTED_LOCALES } from "@/i18n/locales";
import { COMPANY } from "../lib/constants";
import "./global.css";

export const metadata: Metadata = {
  metadataBase: new URL(COMPANY.DOCS_URL),
  icons: {
    icon: [{ url: `${COMPANY.MARKETING_URL}/favicon.svg`, type: "image/svg+xml" }],
  },
};

// Sets <html lang> from the saved language before first paint, ahead of
// hydration, so assistive tech and the browser see the right language. It
// inlines the same parser the interface uses to read the cookie.
const SET_LANG = `try{var l=(${findCookieValue.toString()})(document.cookie,${JSON.stringify(LOCALE_COOKIE)},${JSON.stringify(SUPPORTED_LOCALES)});if(l)document.documentElement.lang=l}catch(e){}`;

export default function Layout({ children }: { children: ReactNode }) {
  return (
    <html
      lang="en"
      className={`dark overscroll-none ${GeistSans.variable}`}
      suppressHydrationWarning
    >
      <head>
        <script dangerouslySetInnerHTML={{ __html: SET_LANG }} />
      </head>
      <body className="min-h-screen bg-background text-foreground antialiased">
        <Providers>{children}</Providers>
      </body>
    </html>
  );
}
