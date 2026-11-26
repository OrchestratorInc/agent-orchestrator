"use client";

import { msg } from "@lingui/core/macro";
import { useLingui } from "@lingui/react";
import { buttonVariants } from "fumadocs-ui/components/ui/button";
import { Popover, PopoverContent, PopoverTrigger } from "fumadocs-ui/components/ui/popover";
import { Check, ChevronDown, Copy, ExternalLink } from "lucide-react";
import { useEffect, useRef, useState } from "react";
import { COMPANY } from "@/lib/constants";

const BUTTON = buttonVariants({ color: "secondary", size: "sm", className: "gap-2 [&_svg]:size-3.5" });
const MENU_ITEM =
  "flex items-center gap-2 rounded-md px-2 py-1.5 text-sm text-fd-foreground hover:bg-fd-accent hover:text-fd-accent-foreground [&_svg]:size-3.5";

/**
 * Copies the article as Markdown, opens it in a chat assistant, or shows its
 * source. `markdownPath` is the article's Markdown copy, e.g. `/quickstart.md`.
 */
export function PageActions({ markdownPath, sourceUrl }: { markdownPath: string; sourceUrl: string }) {
  const { i18n } = useLingui();
  const [copied, setCopied] = useState(false);
  const markdown = useRef<Promise<string> | null>(null);
  const markdownUrl = `${COMPANY.DOCS_URL}${markdownPath}`;
  const prompt = i18n._(msg`Read ${markdownUrl} and answer my questions about it.`);

  useEffect(() => {
    if (!copied) return;
    const timer = setTimeout(() => setCopied(false), 1500);
    return () => clearTimeout(timer);
  }, [copied]);

  async function copyMarkdown() {
    // The dev server serves the copies where the route builds them; the
    // export moves them to the site root.
    const url = process.env.NODE_ENV === "development" ? `/markdown${markdownPath}` : markdownPath;
    markdown.current ??= fetch(url).then((response) => {
      if (!response.ok) throw new Error(`${url}: ${response.status}`);
      return response.text();
    });
    const text = markdown.current;
    text.catch(() => {
      markdown.current = null;
    });
    // Handing the clipboard a pending item keeps the click's permission
    // while the Markdown loads, which Safari requires.
    if (typeof ClipboardItem !== "undefined") {
      const blob = text.then((value) => new Blob([value], { type: "text/plain" }));
      await navigator.clipboard.write([new ClipboardItem({ "text/plain": blob })]);
    } else {
      await navigator.clipboard.writeText(await text);
    }
    setCopied(true);
  }

  const assistants = [
    { name: "ChatGPT", href: `https://chatgpt.com/?${new URLSearchParams({ hints: "search", q: prompt })}` },
    { name: "Claude", href: `https://claude.ai/new?${new URLSearchParams({ q: prompt })}` },
  ];

  return (
    <div className="flex flex-wrap items-center gap-2 border-b pb-6">
      <button type="button" className={BUTTON} onClick={() => void copyMarkdown()}>
        {copied ? <Check /> : <Copy />}
        {copied ? i18n._(msg({ message: "Copied", context: "page actions" })) : i18n._(msg`Copy Markdown`)}
      </button>
      <Popover>
        <PopoverTrigger className={BUTTON}>
          {i18n._(msg`Open in`)}
          <ChevronDown />
        </PopoverTrigger>
        <PopoverContent align="start" className="flex w-auto min-w-44 flex-col p-1">
          {assistants.map(({ name, href }) => (
            <a key={name} href={href} target="_blank" rel="noreferrer noopener" className={MENU_ITEM}>
              {i18n._(msg`Open in ${name}`)}
              <ExternalLink className="ms-auto text-fd-muted-foreground" />
            </a>
          ))}
        </PopoverContent>
      </Popover>
      <a href={sourceUrl} target="_blank" rel="noreferrer noopener" className={BUTTON}>
        {i18n._(msg`View on GitHub`)}
        <ExternalLink />
      </a>
    </div>
  );
}
