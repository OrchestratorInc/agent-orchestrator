import { GeistSans } from "geist/font/sans";
import { DocsLayout } from "fumadocs-ui/layouts/docs";
import type { Metadata } from "next";
import type { ReactNode } from "react";
import type { I18n } from "@lingui/core";
import { Providers } from "@/components/providers";
import { source } from "@/lib/source";
import { baseOptions } from "@/lib/layout.shared";
import { visibleProductName } from "@/lib/branding";
import { COMPANY } from "@/lib/constants";
import { initServerI18n } from "@/i18n/server";
import { splitSlug } from "@/i18n/routing";
import { frameworkTranslations, navLabel } from "@/i18n/ui-messages";
import "../global.css";

type Tree = ReturnType<typeof source.getPageTree>;
type Node = Tree["children"][number];

export const metadata: Metadata = {
  metadataBase: new URL(COMPANY.DOCS_URL),
  icons: {
    icon: [{ url: `${COMPANY.MARKETING_URL}/favicon.svg`, type: "image/svg+xml" }],
  },
};

function displayDescription(description: ReactNode): ReactNode {
  return typeof description === "string" ? visibleProductName(description) : description;
}

function displayName(i18n: I18n, name: ReactNode): ReactNode {
  return typeof name === "string" ? navLabel(i18n, name) : name;
}

function displayNode(i18n: I18n, node: Node): Node {
  if (node.type === "separator") return { ...node, name: displayName(i18n, node.name) };
  if (node.type === "page") return { ...node, description: displayDescription(node.description) };
  return {
    ...node,
    name: displayName(i18n, node.name),
    description: displayDescription(node.description),
    index: node.index && { ...node.index, description: displayDescription(node.index.description) },
    children: node.children.map((child) => displayNode(i18n, child)),
  };
}

function displayTree(i18n: I18n, tree: Tree): Tree {
  return {
    ...tree,
    description: displayDescription(tree.description),
    children: tree.children.map((child) => displayNode(i18n, child)),
    fallback: tree.fallback && displayTree(i18n, tree.fallback),
  };
}

export default async function Layout({
  params,
  children,
}: {
  params: Promise<{ slug?: string[] }>;
  children: ReactNode;
}) {
  const { locale } = splitSlug((await params).slug);
  const i18n = await initServerI18n(locale);

  return (
    <html
      lang={locale}
      className={`dark overscroll-none ${GeistSans.variable}`}
      suppressHydrationWarning
    >
      <body className="min-h-screen bg-background text-foreground antialiased">
        <Providers locale={locale} translations={frameworkTranslations(i18n)}>
          <DocsLayout tree={displayTree(i18n, source.getPageTree(locale))} {...baseOptions(i18n, locale)}>
            {children}
          </DocsLayout>
        </Providers>
      </body>
    </html>
  );
}
