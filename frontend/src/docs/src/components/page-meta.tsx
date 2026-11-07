"use client";

import { msg } from "@lingui/core/macro";
import { useLingui } from "@lingui/react";
import Link from "fumadocs-core/link";
import type * as PageTree from "fumadocs-core/page-tree";
import { useTreeContext, useTreePath } from "fumadocs-ui/contexts/tree";
import { ChevronRight } from "lucide-react";
import { type ReactNode, useMemo } from "react";

type Crumb = { name: ReactNode; url?: string };

/** The sidebar folder whose overview page is at `url`. */
function findFolder(nodes: PageTree.Node[], url: string): PageTree.Folder | undefined {
  for (const node of nodes) {
    if (node.type !== "folder") continue;
    if (node.index?.url === url) return node;
    const found = findFolder(node.children, url);
    if (found) return found;
  }
}

/**
 * The sections above `page`, found by its URL, then the page itself. Going by
 * the URL instead of the page's place in the tree also covers pages the
 * sidebar does not list, so their sections link and keep the sidebar's names.
 */
export function breadcrumbItems(tree: PageTree.Root, page: PageTree.Item): Crumb[] {
  const segments = page.url.split("/").filter(Boolean);
  const sections: Crumb[] = [];
  for (let i = 1; i < segments.length; i++) {
    const url = `/${segments.slice(0, i).join("/")}`;
    const folder = findFolder(tree.children, url);
    if (folder) sections.push({ name: folder.name, url });
  }
  return [...sections, { name: page.name }];
}

/**
 * The trail from the section to the current page, e.g. Built-in capabilities ›
 * Workspaces › Git worktrees. Every section links to its overview page. Pages
 * at the top level have no trail.
 */
export function PageBreadcrumbs() {
  const { i18n } = useLingui();
  const path = useTreePath();
  const { full } = useTreeContext();
  const page = path.at(-1);
  const items = useMemo(() => (page?.type === "page" ? breadcrumbItems(full, page) : []), [full, page]);
  if (items.length < 2) return null;

  return (
    <nav aria-label={i18n._(msg`Breadcrumb`)}>
      <ol className="flex flex-wrap items-center gap-1.5 text-sm text-fd-muted-foreground">
        {items.map((item, index) => {
          const current = index === items.length - 1;
          return (
            <li key={index} className="flex min-w-0 items-center gap-1.5">
              {index > 0 && <ChevronRight className="size-3.5 shrink-0" aria-hidden="true" />}
              {current || !item.url ? (
                <span aria-current={current ? "page" : undefined} className={current ? "truncate font-medium text-fd-foreground" : "truncate"}>
                  {item.name}
                </span>
              ) : (
                <Link href={item.url} className="truncate transition-opacity hover:opacity-80">
                  {item.name}
                </Link>
              )}
            </li>
          );
        })}
      </ol>
    </nav>
  );
}

/**
 * When the article last changed, as an ISO date. The date is shown in the
 * interface language and in UTC, so the prerendered page and the browser agree.
 */
export function LastUpdated({ date }: { date: string }) {
  const { i18n } = useLingui();
  const formatted = i18n.date(date, { day: "numeric", month: "short", year: "numeric", timeZone: "UTC" });
  return (
    <p className="text-sm text-fd-muted-foreground">
      <time dateTime={date}>{i18n._(msg`Last updated ${formatted}`)}</time>
    </p>
  );
}
