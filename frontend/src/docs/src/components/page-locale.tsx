"use client";

import { useLingui } from "@lingui/react";
import { msg } from "@lingui/core/macro";
import { useEffect } from "react";
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
 * English. `title` is shown as given, so pass it already translated if needed.
 */
export function LocalizedTitle({ title }: { title: string }) {
  const { i18n } = useLingui();
  useEffect(() => {
    const localized = i18n._(msg`${title} | Agent Orchestrator Docs`);
    const apply = () => {
      if (document.title !== localized) document.title = localized;
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
  }, [i18n, title]);
  return null;
}
