import type { ReactNode } from "react";
import { DEFAULT_LOCALE, type SupportedLocale } from "@/i18n/locales";
import { visibleProductName } from "./branding";
import { source } from "./source";

type Tree = ReturnType<typeof source.getPageTree>;
type Node = Tree["children"][number];
type PageNode = Extract<Node, { type: "page" }>;
type Page = NonNullable<ReturnType<typeof source.getPage>>;

function displayDescription(description: ReactNode): ReactNode {
  return typeof description === "string" ? visibleProductName(description) : description;
}

/**
 * Maps each page node, including folder index pages, and shows the product
 * name the way readers know it.
 */
function mapTree(tree: Tree, mapPage: (node: PageNode) => PageNode): Tree {
  const page = (node: PageNode): PageNode => mapPage({ ...node, description: displayDescription(node.description) });
  const visit = (node: Node): Node => {
    if (node.type === "separator") return node;
    if (node.type === "page") return page(node);
    return {
      ...node,
      description: displayDescription(node.description),
      index: node.index && page(node.index),
      children: node.children.map(visit),
    };
  };
  return {
    ...tree,
    description: displayDescription(tree.description),
    children: tree.children.map(visit),
    fallback: tree.fallback && mapTree(tree.fallback, mapPage),
  };
}

/**
 * The sidebar for a language. It lists every article: translated ones by
 * their translated title and URL, the rest by their English title and URL.
 */
export function docsTree(locale: SupportedLocale): Tree {
  const tree = source.getPageTree(DEFAULT_LOCALE);
  if (locale === DEFAULT_LOCALE) return mapTree(tree, (node) => node);

  const translated = new Map<string, Page>();
  for (const page of source.getPages(locale)) {
    const english = source.getPage(page.slugs, DEFAULT_LOCALE);
    if (english) translated.set(english.url, page);
  }
  return {
    ...mapTree(tree, (node) => {
      const page = translated.get(node.url);
      return page ? { ...node, name: page.data.title, url: page.url, description: page.data.description } : node;
    }),
    $id: locale,
  };
}
