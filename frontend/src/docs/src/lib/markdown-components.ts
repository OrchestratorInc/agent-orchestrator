import { COMPANY } from "./constants";
import { DESKTOP_DOWNLOADS, downloadUrl } from "./downloads";
import { STATUS_LABEL, type PlatformStatus } from "./platform-status";

/*
 * The `.md` copies and llms.txt files are built from each article's processed
 * Markdown, where MDX components stay as JSX tags. Readers of those files
 * (often coding agents) never run the components, so a tag such as
 * `<InstallDownloads />` shows nothing. This turns the components whose
 * content would otherwise be lost into plain Markdown.
 */

const PLATFORM_NAMES: [attribute: string, name: string][] = [
  ["macos", "macOS"],
  ["linux", "Linux"],
  ["windows", "Windows"],
];

/** `name="value"` attributes of one JSX tag. */
function attributes(tag: string): Record<string, string> {
  const result: Record<string, string> = {};
  for (const match of tag.matchAll(/([A-Za-z]+)="((?:[^"\\]|\\.)*)"/g)) result[match[1]!] = match[2]!;
  return result;
}

/** The `<InstallDownloads />` table as a Markdown list of direct download links. */
export function installDownloadsMarkdown(): string {
  const lines = ["Download the latest Orchestrator desktop release:", ""];
  for (const { platform, assets } of DESKTOP_DOWNLOADS) {
    lines.push(`- ${platform}: ${assets.map(([label, asset]) => `[${label}](${downloadUrl(asset)})`).join(" · ")}`);
  }
  return lines.join("\n");
}

function platformSupportMarkdown(tag: string): string {
  const attrs = attributes(tag);
  const status = (name: string) => STATUS_LABEL[(attrs[name] as PlatformStatus | undefined) ?? "full"] ?? STATUS_LABEL.full;
  const lines = PLATFORM_NAMES.map(([attribute, name]) => `- ${name}: ${status(attribute)}`);
  // A JSX `note` is stringified as "<>text</>".
  const note = attrs.note?.replace(/^<>/, "").replace(/<\/>$/, "").trim();
  return ["Platform support:", "", ...lines, ...(note ? ["", note] : [])].join("\n");
}

/** Absolute docs links point at the Markdown copy, so a reader can keep following them. */
function docsHref(href: string): string {
  if (!href.startsWith("/")) return href;
  const [path = "", hash] = href.split("#");
  const trimmed = path.replace(/\/+$/, "");
  return `${COMPANY.DOCS_URL}${trimmed ? trimmed : "/index"}.md${hash ? `#${hash}` : ""}`;
}

function cardMarkdown(tag: string): string {
  const { title = "", href, description } = attributes(tag);
  const label = href ? `[${title}](${docsHref(href)})` : title;
  return `- ${label}${description ? `: ${description}` : ""}`;
}

/** List-item and block-quote markers that can come before a code fence, e.g. "* ```mdx". */
const CONTAINER_PREFIX = /^(?:(?:[*+-]|\d+[.)])\s+|>\s?)*/;

type Fence = { marker: string; quoteDepth: number };

/** The fence that opens a fenced code block on this line, if any, with how many block quotes it sits in. */
function openingFence(line: string): Fence | undefined {
  const trimmed = line.trim();
  const prefix = CONTAINER_PREFIX.exec(trimmed)?.[0] ?? "";
  const marker = /^(`{3,}|~{3,})/.exec(trimmed.slice(prefix.length))?.[1];
  return marker ? { marker, quoteDepth: (prefix.match(/>/g) ?? []).length } : undefined;
}

/**
 * Whether this line closes the open fence. Inside a fence every line is literal
 * content: only the block quotes the fence opened in are removed, so "- ```"
 * does not close a top-level fence but "> ```" closes one opened in a quote.
 */
function closesFence(line: string, fence: Fence): boolean {
  let rest = line.trim();
  for (let depth = 0; depth < fence.quoteDepth; depth++) rest = rest.replace(/^>\s?/, "").trimStart();
  const marker = /^(`{3,}|~{3,})\s*$/.exec(rest)?.[1];
  return !!marker && marker[0] === fence.marker[0] && marker.length >= fence.marker.length;
}

/** Whether a JSX tag is complete: it ends with "/>" outside any quoted attribute value. */
function tagComplete(tag: string): boolean {
  let quote: string | undefined;
  for (let i = 0; i < tag.length; i++) {
    const char = tag[i]!;
    if (quote) {
      if (char === "\\") i++;
      else if (char === quote) quote = undefined;
    } else if (char === '"' || char === "'") quote = char;
    else if (char === "/" && tag[i + 1] === ">") return i + 2 === tag.length;
  }
  return false;
}

/** Components whose whole tag is converted. A tag may span several lines. */
const TAG_START = /^<(PlatformSupport|Card)\b/;

/** Turns MDX components into Markdown, leaving fenced code blocks untouched. */
export function expandComponents(markdown: string): string {
  const lines = markdown.split("\n");
  const out: string[] = [];
  let fence: Fence | undefined;
  let inCards = false;
  for (let index = 0; index < lines.length; index++) {
    const line = lines[index]!;
    const trimmed = line.trim();
    if (fence) {
      if (closesFence(line, fence)) fence = undefined;
      out.push(line);
      continue;
    }
    const opening = openingFence(line);
    if (opening) {
      fence = opening;
      out.push(line);
      continue;
    }

    const tagName = TAG_START.exec(trimmed)?.[1];
    if (tagName) {
      // Collect the rest of a tag the serializer split across lines, e.g. a note with a line break.
      let tag = trimmed;
      let end = index;
      while (!tagComplete(tag) && end + 1 < lines.length) tag += `\n${lines[++end]!.trim()}`;
      if (tagComplete(tag)) {
        out.push(tagName === "Card" ? cardMarkdown(tag) : platformSupportMarkdown(tag));
        index = end;
        continue;
      }
    }

    if (/^<InstallDownloads\s*\/>$/.test(trimmed)) out.push(installDownloadsMarkdown());
    else if (trimmed === "<Cards>") inCards = true;
    else if (trimmed === "</Cards>") inCards = false;
    // Blank lines between cards would split the list into separate paragraphs.
    else if (inCards && trimmed === "") continue;
    else out.push(line);
  }
  return out.join("\n");
}
