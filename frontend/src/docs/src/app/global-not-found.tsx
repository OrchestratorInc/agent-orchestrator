import { GeistSans } from "geist/font/sans";
import type { Metadata } from "next";
import { NotFound } from "@/components/not-found";
import { COMPANY } from "@/lib/constants";
import "./global.css";

export const metadata: Metadata = {
  metadataBase: new URL(COMPANY.DOCS_URL),
  title: "Page Not Found | Agent Orchestrator Docs",
  robots: { index: false },
  icons: {
    icon: [{ url: `${COMPANY.MARKETING_URL}/favicon.svg`, type: "image/svg+xml" }],
  },
};

export default function GlobalNotFound() {
  return (
    <html lang="en" className={`dark overscroll-none ${GeistSans.variable}`}>
      <body className="min-h-screen bg-background text-foreground antialiased">
        <NotFound />
      </body>
    </html>
  );
}
