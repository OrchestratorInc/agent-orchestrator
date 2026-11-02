import type { RootContent } from "mdast";
import type { MdxJsxAttribute, MdxJsxFlowElement } from "mdast-util-mdx";
import { toString } from "mdast-util-to-string";
import remarkMdx from "remark-mdx";
import remarkParse from "remark-parse";
import { unified } from "unified";

export type FaqEntry = { question: string; answer: string };

function isAccordion(node: RootContent): node is MdxJsxFlowElement {
  return node.type === "mdxJsxFlowElement" && node.name === "Accordion";
}

/**
 * The questions and answers of an article written as `<Accordion title="…">`
 * items, as plain text for `FAQPage` structured data.
 */
export function faqEntries(source: string): FaqEntry[] {
  const tree = unified().use(remarkParse).use(remarkMdx).parse(source.replace(/^---\n[\s\S]*?\n---\n/, ""));
  const entries: FaqEntry[] = [];
  const visit = (nodes: RootContent[]) => {
    for (const node of nodes) {
      if (isAccordion(node)) {
        const title = node.attributes.find(
          (attr): attr is MdxJsxAttribute => attr.type === "mdxJsxAttribute" && attr.name === "title",
        );
        const answer = node.children.map((child) => toString(child)).join(" ").replace(/\s+/g, " ").trim();
        if (typeof title?.value === "string" && answer) entries.push({ question: title.value, answer });
      } else if ("children" in node) {
        visit(node.children as RootContent[]);
      }
    }
  };
  visit(tree.children);
  return entries;
}
