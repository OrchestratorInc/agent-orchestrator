import { GeistSans } from "geist/font/sans";
import type { Metadata } from "next";
import { NotFound } from "@/components/not-found";
import { Providers } from "@/components/providers";
import { siteMetadata } from "@/components/site-root";
import "./global.css";

export const metadata: Metadata = {
  ...siteMetadata,
  title: "Page not found | Orchestrator.inc Docs",
  robots: { index: false },
};

// Each language has its own root layout, so the 404 page brings its own.
export default function NotFoundPage() {
  return (
    <html lang="en" className={`dark overscroll-none ${GeistSans.variable}`} suppressHydrationWarning>
      <body className="min-h-screen bg-background text-foreground antialiased">
        <Providers>
          <NotFound />
        </Providers>
      </body>
    </html>
  );
}
