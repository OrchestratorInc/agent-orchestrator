"use client";

import { Trans, useLingui } from "@lingui/react/macro";
import Link from "next/link";
import { useEffect, useState } from "react";
import { COMPANY } from "@/lib/constants";
import { docsIssueUrl } from "@/lib/docs-issue";
import { LocalizedTitle } from "./page-locale";

const LINK =
  "rounded-lg border border-border bg-muted/30 px-3 py-1.5 text-sm text-foreground no-underline transition-colors hover:bg-muted/45";

export function NotFound() {
  const { t } = useLingui();
  // The missing URL is only known in the browser; the export has one 404 page.
  const [missing, setMissing] = useState<{ url: string; path: string }>({ url: COMPANY.DOCS_URL, path: "/" });
  useEffect(() => setMissing({ url: window.location.href, path: window.location.pathname }), []);
  const name = COMPANY.NAME;

  return (
    // The docs only ship the utility classes the docs framework uses, so this
    // page sticks to those (min-h-dvh, max-w-md) to stay centered.
    <main className="mx-auto flex min-h-dvh max-w-md flex-col items-start justify-center gap-4 px-4 py-12">
      <LocalizedTitle source="Page not found" title={t`Page not found`} />
      <a href={COMPANY.MARKETING_URL} aria-label={t`${name} home`} className="mb-2 inline-flex">
        <img src={`${COMPANY.MARKETING_URL}/ao-logo.svg`} alt="" width={28} height={28} />
      </a>
      <h1 className="text-2xl font-semibold text-foreground">
        <Trans>Page not found</Trans>
      </h1>
      <p className="text-sm text-muted-foreground">
        <Trans>The page you are looking for might have been removed, had its name changed, or is temporarily unavailable.</Trans>
      </p>
      <nav aria-label={t`Popular pages`} className="flex flex-wrap gap-2">
        <Link href="/" className={LINK}>
          <Trans>Back to the docs home</Trans>
        </Link>
        <Link href="/installation/" className={LINK}>
          <Trans>Installation</Trans>
        </Link>
        <Link href="/troubleshooting/" className={LINK}>
          <Trans>Troubleshooting</Trans>
        </Link>
      </nav>
      <p className="text-sm text-muted-foreground">
        <Trans>
          Followed a link here?{" "}
          <a
            href={docsIssueUrl({ title: `Broken link ${missing.path}`, pageUrl: missing.url })}
            target="_blank"
            rel="noreferrer noopener"
            className="text-fd-foreground underline"
          >
            Report a broken link
          </a>
        </Trans>
      </p>
    </main>
  );
}
