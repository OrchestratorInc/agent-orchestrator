import { DEFAULT_LOCALE } from "@/i18n/locales";
import { COMPANY } from "./constants";
import { source } from "./source";
import { articlePath } from "./translations";

type Page = NonNullable<ReturnType<typeof source.getPage>>;
type Node = ReturnType<typeof source.getPageTree>["children"][number];

/** A short summary of the product, at the top of llms.txt. */
export const PRODUCT_SUMMARY =
  "Orchestrator, also known as Agent Orchestrator or AO, is a desktop app for supervising AI coding agents. It gives each worker an isolated workspace, keeps its conversation or terminal available, and connects Git work to pull requests, CI, reviews, and merge conflicts.";

/** The Markdown copy of an English article, e.g. `/quickstart.md`. The home page is `/index.md`. */
export function markdownPath(slugs: string[]): string {
  return `/${slugs.length ? slugs.join("/") : "index"}.md`;
}

/** Where the article's source file lives in the repository. */
export function sourceUrl(page: Page): string {
  return `${COMPANY.GITHUB_URL}/blob/main/frontend/src/docs/content/${page.path}`;
}

/** The article as Markdown, headed by its title, URL and description. */
export async function pageMarkdown(page: Page): Promise<string> {
  const header = [`# ${page.data.title}`, "", `URL: ${COMPANY.DOCS_URL}${articlePath(page.slugs)}`];
  if (page.data.description) header.push("", `> ${page.data.description}`);
  const body = (await page.data.getText("processed")).trim();
  return `${header.join("\n")}\n\n${body}\n`;
}

function link(title: string, href: string): string {
  return `[${title.replace(/([[\]])/g, "\\$1")}](${href})`;
}

function listItem(page: Page): string {
  const text = page.data.description ?? "";
  const item = `- ${link(page.data.title, `${COMPANY.DOCS_URL}${markdownPath(page.slugs)}`)}`;
  return text ? `${item}: ${text}` : item;
}

/** The pages under a sidebar node, in sidebar order, folder index pages first. */
function nodePages(node: Node): Page[] {
  if (node.type === "separator") return [];
  if (node.type === "page") {
    const page = source.getNodePage(node, DEFAULT_LOCALE);
    return page ? [page] : [];
  }
  return [...(node.index ? nodePages(node.index) : []), ...node.children.flatMap(nodePages)];
}

/** Every English article grouped by sidebar section. Articles left out of the sidebar come last. */
function sections(): { title: string; pages: Page[] }[] {
  const result: { title: string; pages: Page[] }[] = [];
  for (const node of source.getPageTree(DEFAULT_LOCALE).children) {
    if (node.type === "separator") {
      result.push({ title: typeof node.name === "string" ? node.name : "", pages: [] });
      continue;
    }
    if (!result.length) result.push({ title: "Docs", pages: [] });
    result[result.length - 1]!.pages.push(...nodePages(node));
  }
  const listed = new Set(result.flatMap((section) => section.pages.map((page) => page.url)));
  result.push({ title: "Other pages", pages: source.getPages(DEFAULT_LOCALE).filter((page) => !listed.has(page.url)) });
  return result;
}

/**
 * The index of the English docs: a summary, then every article grouped by
 * sidebar section, each linking to its Markdown copy.
 */
export function llmsIndex(): string {
  const out = ["# Orchestrator.inc Docs", "", `> ${PRODUCT_SUMMARY}`, ""];
  out.push(
    `Each link below is the Markdown version of a page. ${link("llms-full.txt", `${COMPANY.DOCS_URL}/llms-full.txt`)} has every page in one file.`,
  );
  for (const section of sections()) {
    if (!section.pages.length) continue;
    out.push("", `## ${section.title}`, "", ...section.pages.map(listItem));
  }
  return `${out.join("\n")}\n`;
}

/** Every English article's Markdown in one file, in sidebar order. */
export async function llmsFull(): Promise<string> {
  const pages = sections().flatMap((section) => section.pages);
  const texts = await Promise.all(pages.map(pageMarkdown));
  return texts.join("\n---\n\n");
}
