"use client";

import { Trans, useLingui } from "@lingui/react/macro";
import Link from "next/link";
import { LocalizedTitle } from "./page-locale";

export function NotFound() {
  const { t } = useLingui();
  return (
    <main className="mx-auto flex min-h-screen max-w-xl flex-col items-start justify-center gap-4 px-4">
      <LocalizedTitle source="Page not found" title={t`Page not found`} />
      <h1 className="text-2xl font-semibold text-foreground">
        <Trans>Page not found</Trans>
      </h1>
      <p className="text-sm text-muted-foreground">
        <Trans>The page you are looking for might have been removed, had its name changed, or is temporarily unavailable.</Trans>
      </p>
      <Link
        href="/"
        className="rounded-lg border border-border bg-muted/30 px-3 py-1.5 text-sm text-foreground no-underline transition-colors hover:bg-muted/45"
      >
        <Trans>Back to the docs home</Trans>
      </Link>
    </main>
  );
}
