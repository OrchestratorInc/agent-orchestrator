import { GeistSans } from "geist/font/sans";
import type { Metadata } from "next";
import type { ReactNode } from "react";
import { Providers } from "@/components/providers";
import { LOCALE_COOKIE, SUPPORTED_LOCALES } from "@/i18n/locales";
import { COMPANY } from "../lib/constants";
import "./global.css";

export const metadata: Metadata = {
  metadataBase: new URL(COMPANY.DOCS_URL),
  icons: {
    icon: [{ url: `${COMPANY.MARKETING_URL}/favicon.svg`, type: "image/svg+xml" }],
  },
};

// Sets <html lang> from the saved language before first paint, ahead of
// hydration, so assistive tech and the browser see the right language.
const SET_LANG = `try{var m=document.cookie.match(/(?:^|; )${LOCALE_COOKIE}=([^;]*)/);var l=m&&m[1];if(${JSON.stringify(SUPPORTED_LOCALES)}.indexOf(l)>-1)document.documentElement.lang=l}catch(e){}`;

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
