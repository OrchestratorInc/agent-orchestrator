"use client";

import type { I18n } from "@lingui/core";
import { useLingui } from "@lingui/react";
import type * as PageTree from "fumadocs-core/page-tree";
import { DocsLayout } from "fumadocs-ui/layouts/docs";
import { type ReactNode, useMemo } from "react";
import { navLabel } from "@/i18n/ui-messages";
import { baseOptions } from "@/lib/layout.shared";

type Node = PageTree.Root["children"][number];

function translateName(i18n: I18n, name: ReactNode): ReactNode {
  return typeof name === "string" ? navLabel(i18n, name) : name;
}

// Section and folder titles follow the interface language. Page titles come
// from the articles, in the article's language.
function translateNode(i18n: I18n, node: Node): Node {
  if (node.type === "page") return node;
  if (node.type === "separator") return { ...node, name: translateName(i18n, node.name) };
  return {
    ...node,
    name: translateName(i18n, node.name),
    children: node.children.map((child) => translateNode(i18n, child)),
  };
}

function translateTree(i18n: I18n, tree: PageTree.Root): PageTree.Root {
  return {
    ...tree,
    // The layout caches the tree by id, so each language needs its own.
    $id: `${tree.$id ?? "root"}:${i18n.locale}`,
    children: tree.children.map((child) => translateNode(i18n, child)),
    fallback: tree.fallback && translateTree(i18n, tree.fallback),
  };
}

export function LocalizedDocsLayout({
  tree,
  homeUrl,
  children,
}: {
  tree: PageTree.Root;
  homeUrl: string;
  children: ReactNode;
}) {
  const { i18n } = useLingui();
  const localizedTree = useMemo(() => translateTree(i18n, tree), [i18n, tree]);

  return (
    <DocsLayout tree={localizedTree} {...baseOptions(i18n, homeUrl)}>
      {children}
    </DocsLayout>
  );
}
