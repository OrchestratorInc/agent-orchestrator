"use client";

import { useLingui } from "@lingui/react";
import { msg } from "@lingui/core/macro";
import { useEffect, useRef } from "react";
import { DEFAULT_LOCALE } from "@/i18n/locales";

/** Points out that the article is English when the interface is not. */
export function EnglishArticleNotice() {
  const { i18n } = useLingui();
  if (i18n.locale === DEFAULT_LOCALE) return null;
  return (
    <p className="-mt-2 text-sm text-fd-muted-foreground">
      {i18n._(msg`This page is not translated yet, so it is shown in English.`)}
    </p>
  );
}

/**
 * Keeps the tab title in the interface language. The prerendered title is
 * `${source} | Orchestrator.inc Docs`; `title` is the name to show instead,
 * already translated (defaults to `source`).
 */
export function LocalizedTitle({ source, title = source }: { source: string; title?: string }) {
  const { i18n } = useLingui();
  const written = useRef<string | null>(null);
  useEffect(() => {
    const english = `${source} | Orchestrator.inc Docs`;
    const localized = i18n._(msg`${title} | Orchestrator.inc Docs`);
    // Only replace this page's own title (English, or our earlier
    // translation), never another page's that a navigation just wrote.
    const apply = () => {
      const current = document.title;
      if (current === localized || (current !== english && current !== written.current)) return;
      document.title = localized;
      written.current = localized;
    };
    // The framework writes the English title after hydration and on
    // navigation. Watch only the <title> element, and the head's direct
    // children in case that element is replaced.
    let watched: Element | null = null;
    const watchTitle = () => {
      const element = document.head.querySelector("title");
      if (!element || element === watched) return;
      watched = element;
      observer.observe(element, { childList: true, subtree: true, characterData: true });
    };
    const observer = new MutationObserver(() => {
      watchTitle();
      apply();
    });
    observer.observe(document.head, { childList: true });
    watchTitle();
    apply();
    return () => observer.disconnect();
  }, [i18n, source, title]);
  return null;
}
