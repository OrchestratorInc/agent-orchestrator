import type { BlockContent, DefinitionContent, ListItem, Nodes, Parents, PhrasingContent, RootContent } from "mdast";
import type { MdxJsxAttribute, MdxJsxExpressionAttribute, MdxJsxFlowElement, MdxJsxTextElement } from "mdast-util-mdx";
import type { Info, State } from "mdast-util-to-markdown";
import { withTrailingSlash } from "../i18n/paths";
import { COMPANY } from "./constants";
import { DOWNLOADS } from "./downloads";

// The Markdown copies of the docs (llms.txt, llms-full.txt and each `.md`
// page) are read by tools that do not run MDX, so the components below are
// written out as plain Markdown and site links as absolute URLs.

type JsxElement = MdxJsxFlowElement | MdxJsxTextElement;
type Block = BlockContent | DefinitionContent;

/** Matches a JSX-looking tag such as `<Card` or `<Steps>`. Exported Markdown must not contain one outside code. */
export const JSX_TAG = /<[A-Z][A-Za-z]*[\s/>]/;

/** An absolute docs URL for a site link such as `/quickstart#install`. Other links are returned unchanged. */
export function absoluteDocsUrl(href: string): string {
  if (!href.startsWith("/") || href.startsWith("//")) return href;
  const [path = "", hash] = href.split("#", 2);
  const url = `${COMPANY.DOCS_URL}${withTrailingSlash(path)}`;
  return hash === undefined ? url : `${url}#${hash}`;
}

function attribute(node: JsxElement, name: string): string | undefined {
  const found = node.attributes.find(
    (attr: MdxJsxAttribute | MdxJsxExpressionAttribute): attr is MdxJsxAttribute =>
      attr.type === "mdxJsxAttribute" && attr.name === name,
  );
  if (!found || found.value == null) return undefined;
  if (typeof found.value === "string") return found.value;
  // An expression such as `note={<>Some text</>}`: keep its text.
  return found.value.value.replace(/<\/?>/g, "").trim() || undefined;
}

function text(value: string): PhrasingContent {
  return { type: "text", value };
}

function strong(value: string): PhrasingContent {
  return { type: "strong", children: [text(value)] };
}

/** The element's children as blocks, wrapping loose inline content in a paragraph. */
function blocks(node: JsxElement): Block[] {
  if (node.type === "mdxJsxTextElement") return [{ type: "paragraph", children: node.children }];
  return node.children as Block[];
}

function listItem(children: Block[]): ListItem {
  return { type: "listItem", spread: false, children };
}

function card(node: JsxElement): ListItem {
  const title = attribute(node, "title") ?? "";
  const href = attribute(node, "href");
  const description = attribute(node, "description");
  const line: PhrasingContent[] = [href ? { type: "link", url: absoluteDocsUrl(href), children: [text(title)] } : strong(title)];
  if (description) line.push(text(`: ${description}`));
  const rest = description ? [] : blocks(node);
  return listItem([{ type: "paragraph", children: line }, ...rest]);
}

function childElements(node: JsxElement, name: string): JsxElement[] {
  return node.children.filter(
    (child): child is MdxJsxFlowElement => child.type === "mdxJsxFlowElement" && child.name === name,
  );
}

function downloadsTable(): RootContent {
  const cell = (children: PhrasingContent[]) => ({ type: "tableCell" as const, children });
  return {
    type: "table",
    align: [null, null],
    children: [
      { type: "tableRow", children: [cell([text("Platform")]), cell([text("Download")])] },
      ...DOWNLOADS.map(({ platform, assets }) => ({
        type: "tableRow" as const,
        children: [
          cell([text(platform)]),
          cell(
            assets.flatMap(({ label, url }, index): PhrasingContent[] => [
              ...(index > 0 ? [text(" · ")] : []),
              { type: "link", url, children: [text(label)] },
            ]),
          ),
        ],
      })),
    ],
  };
}

/** The plain Markdown that stands in for a docs component. */
function replacement(node: JsxElement): RootContent[] {
  switch (node.name) {
    case "Cards":
      return [{ type: "list", ordered: false, spread: false, children: childElements(node, "Card").map(card) }];
    case "Card":
      return [{ type: "list", ordered: false, spread: false, children: [card(node)] }];
    case "Steps":
      return [
        {
          type: "list",
          ordered: true,
          spread: true,
          children: childElements(node, "Step").map((step) => ({ ...listItem(blocks(step)), spread: true })),
        },
      ];
    case "Accordion": {
      const title = attribute(node, "title");
      return [...(title ? [{ type: "paragraph" as const, children: [strong(title)] }] : []), ...blocks(node)];
    }
    case "Callout": {
      const title = attribute(node, "title");
      const heading: Block[] = title ? [{ type: "paragraph", children: [strong(title)] }] : [];
      return [{ type: "blockquote", children: [...heading, ...blocks(node)] }];
    }
    case "Tab": {
      const value = attribute(node, "value") ?? attribute(node, "title");
      return [...(value ? [{ type: "paragraph" as const, children: [strong(value)] }] : []), ...blocks(node)];
    }
    case "InstallDownloads":
      return [downloadsTable()];
    case "PlatformSupport": {
      const label: Record<string, string> = { full: "supported", partial: "limited", none: "not supported" };
      const platforms = [
        ["macOS", attribute(node, "macos")],
        ["Linux", attribute(node, "linux")],
        ["Windows", attribute(node, "windows")],
      ].map(([name, status]) => `${name}: ${label[status ?? "full"] ?? status}`);
      const note = attribute(node, "note");
      return [{ type: "paragraph", children: [text(`${platforms.join(". ")}.${note ? ` ${note}` : ""}`)] }];
    }
    case "PluginCard": {
      const name = attribute(node, "name") ?? "";
      const href = attribute(node, "href");
      const description = attribute(node, "description");
      const line: PhrasingContent[] = [href ? { type: "link", url: absoluteDocsUrl(href), children: [text(name)] } : strong(name)];
      if (description) line.push(text(`: ${description}`));
      return [{ type: "list", ordered: false, spread: false, children: [listItem([{ type: "paragraph", children: line }])] }];
    }
    default:
      // Layout-only wrappers such as `Accordions`, `Tabs` and `PluginGrid`.
      return node.children;
  }
}

/**
 * The `stringify` hook of the processed Markdown: writes docs components as
 * plain Markdown and makes site links absolute. Anything else is left to the
 * default stringifier.
 */
export function stringifyForExport(node: Nodes, parent: Parents | undefined, state: State, info: Info): string | undefined {
  switch (node.type) {
    case "link":
    case "image":
    case "definition": {
      const url = absoluteDocsUrl(node.url);
      return url === node.url ? undefined : state.handle({ ...node, url }, parent, state, info);
    }
    case "mdxJsxTextElement":
      return state.containerPhrasing({ type: "paragraph", children: replacement(node) as PhrasingContent[] }, info);
    case "mdxJsxFlowElement":
      return state.containerFlow({ type: "root", children: replacement(node) }, info);
  }
}
