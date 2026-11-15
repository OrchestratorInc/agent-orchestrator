"use client";

import { useLingui } from "@lingui/react";
import { msg } from "@lingui/core/macro";
import { useEffect, useRef } from "react";
import { DEFAULT_LOCALE } from "@/i18n/locales";

export function EnglishArticleNotice() {
  const { i18n } = useLingui();
  if (i18n.locale === DEFAULT_LOCALE) return null;
  return (
    <p className="-mt-2 text-sm text-fd-muted-foreground">
      {i18n._(msg`This page is not translated yet, so it is shown in English.`)}
    </p>
  );
}

export function LocalizedTitle({ source, title = source }: { source: string; title?: string }) {
  const { i18n } = useLingui();
  const written = useRef<string | null>(null);
  useEffect(() => {
    const english = `${source} | Orchestrator.inc Docs`;
    const localized = i18n._(msg`${title} | Orchestrator.inc Docs`);
    const apply = () => {
      const current = document.title;
      if (current === localized || (current !== english && current !== written.current)) return;
      document.title = localized;
      written.current = localized;
    };
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
