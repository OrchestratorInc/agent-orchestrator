"use client";

import { type I18n, setupI18n } from "@lingui/core";
import { I18nProvider } from "@lingui/react";
import { Trans } from "@lingui/react/macro";
import { useEffect, useState } from "react";
import { getLocaleMessages } from "@/i18n/catalogs";
import { DEFAULT_LOCALE, localizePath, splitLocalePath, type SupportedLocale } from "@/i18n/locales";
import { messages as enMessages } from "../../locales/en/messages";

const ENGLISH = setupI18n({ locale: DEFAULT_LOCALE, messages: { [DEFAULT_LOCALE]: enMessages } });

/**
 * The host serves one 404 page for every missing URL, so the page reads the
 * locale prefix from the address and switches language after it loads.
 */
export function NotFound() {
  const [state, setState] = useState<{ locale: SupportedLocale; i18n: I18n }>({
    locale: DEFAULT_LOCALE,
    i18n: ENGLISH,
  });

  useEffect(() => {
    const { locale } = splitLocalePath(window.location.pathname);
    if (locale === DEFAULT_LOCALE) return;
    let cancelled = false;
    getLocaleMessages(locale).then((messages) => {
      if (cancelled) return;
      document.documentElement.lang = locale;
      setState({ locale, i18n: setupI18n({ locale, messages: { [locale]: messages } }) });
    });
    return () => {
      cancelled = true;
    };
  }, []);

  return (
    <I18nProvider i18n={state.i18n}>
      <main className="mx-auto flex min-h-screen max-w-xl flex-col items-start justify-center gap-4 px-4">
        <h1 className="text-2xl font-semibold text-foreground">
          <Trans>Page not found</Trans>
        </h1>
        <p className="text-sm text-muted-foreground">
          <Trans>The page you are looking for might have been removed, had its name changed, or is temporarily unavailable.</Trans>
        </p>
        <a
          href={localizePath(state.locale, "/")}
          className="rounded-lg border border-border bg-muted/30 px-3 py-1.5 text-sm text-foreground no-underline transition-colors hover:bg-muted/45"
        >
          <Trans>Back to the docs home</Trans>
        </a>
      </main>
    </I18nProvider>
  );
}
