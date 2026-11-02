// @vitest-environment node
import { readdirSync, readFileSync } from "node:fs";
import { join, relative } from "node:path";
import { fileURLToPath } from "node:url";
import { remarkLLMs } from "fumadocs-core/mdx-plugins/remark-llms";
import remarkGfm from "remark-gfm";
import remarkMdx from "remark-mdx";
import remarkParse from "remark-parse";
import { unified } from "unified";
import { describe, expect, it } from "vitest";
import { absoluteDocsUrl, JSX_TAG, PROCESSED_MARKDOWN } from "./markdown-export";

const contentDir = fileURLToPath(new URL("../../content/", import.meta.url));

/** The English articles. Translations are not exported as Markdown. */
function articles(dir = contentDir): string[] {
  return readdirSync(dir, { withFileTypes: true }).flatMap((entry) => {
    const path = join(dir, entry.name);
    if (entry.isDirectory()) return articles(path);
    return /^[^.]+\.mdx$/.test(entry.name) ? [path] : [];
  });
}

/** An article's Markdown copy, made the way the docs build makes it. */
async function exported(source: string): Promise<string> {
  const body = source.replace(/^---\n[\s\S]*?\n---\n/, "");
  const processor = unified()
    .use(remarkParse)
    .use(remarkMdx)
    .use(remarkGfm)
    .use(remarkLLMs, { ...PROCESSED_MARKDOWN, _data: true });
  const file = { value: body, data: {} as { markdown?: string } };
  await processor.run(processor.parse(body), file as never);
  return file.data.markdown ?? "";
}

/** Markdown with code blocks and code spans removed, where a `<Tag>` is only an example. */
function withoutCode(markdown: string): string {
  return markdown.replace(/^(`{3,}|~{3,})[^\n]*\n[\s\S]*?^\1\s*$/gm, "").replace(/(`+)[^`]*?\1/g, "");
}

describe("Markdown export", () => {
  it.each(articles().map((path) => [relative(contentDir, path), path]))("%s has no JSX tags", async (_name, path) => {
    const markdown = withoutCode(await exported(readFileSync(path, "utf8")));
    expect(markdown.split("\n").filter((line) => JSX_TAG.test(line))).toEqual([]);
  });

  it.each(articles().map((path) => [relative(contentDir, path), path]))("%s has no HTML tags", async (_name, path) => {
    const markdown = withoutCode(await exported(readFileSync(path, "utf8")));
    expect(markdown.split("\n").filter((line) => /<\/?[a-z][a-z0-9-]*(\s[^>]*)?\/?>/.test(line))).toEqual([]);
  });

  it.each(articles().map((path) => [relative(contentDir, path), path]))("%s has no site-relative links", async (_name, path) => {
    const markdown = withoutCode(await exported(readFileSync(path, "utf8")));
    expect(markdown.match(/\]\(\/[^)]*\)/g) ?? []).toEqual([]);
  });

  it("writes cards as a list of absolute links", async () => {
    const markdown = await exported(
      '<Cards>\n  <Card title="Quickstart" description="Run one worker." href="/quickstart" />\n</Cards>\n',
    );
    expect(markdown.trim()).toBe("* [Quickstart](https://docs.orchestrator.inc/quickstart/): Run one worker.");
  });

  it("writes steps as a numbered list and callouts as quotes", async () => {
    const markdown = await exported(
      '<Steps>\n  <Step>\n    Install.\n  </Step>\n  <Step>\n    Run.\n  </Step>\n</Steps>\n\n<Callout title="Note">\n  Read [this](/faq#cost).\n</Callout>\n',
    );
    expect(markdown).toContain("1. Install.");
    expect(markdown).toContain("2. Run.");
    expect(markdown).toContain("> **Note**");
    expect(markdown).toContain("> Read [this](https://docs.orchestrator.inc/faq/#cost).");
  });

  it("keeps content placed between cards", async () => {
    const markdown = await exported('<Cards>\n  <Card title="A" href="/a" />\n\n  Some note.\n</Cards>\n');
    expect(markdown).toContain("[A](https://docs.orchestrator.inc/a/)");
    expect(markdown).toContain("Some note.");
  });

  it("drops heading anchors and unwraps HTML", async () => {
    const markdown = await exported(
      '<span id="old-id" />\n## Title\n\n<details>\n<summary>\n\t<span className="note"><code>ao</code> is frozen.</span>\n</summary>\n\nBody.\n</details>\n',
    );
    expect(markdown).toBe("## Title\n\n`ao` is frozen.\n\nBody.\n");
  });

  it("keeps inline content next to blocks in one paragraph", async () => {
    const markdown = await exported("<div>\nSee <code>ao</code> here.\n\n- item\n</div>\n");
    expect(markdown).toBe("See `ao` here.\n\n* item\n");
  });

  it("writes the downloads as a table", async () => {
    const markdown = await exported("<InstallDownloads />\n");
    expect(markdown).toContain("| Platform");
    expect(markdown).toContain("agent-orchestrator-darwin-arm64.dmg");
  });

  it("keeps external links and fragments", () => {
    expect(absoluteDocsUrl("https://orchestrator.inc/compare/")).toBe("https://orchestrator.inc/compare/");
    expect(absoluteDocsUrl("#configuration-fields")).toBe("#configuration-fields");
    expect(absoluteDocsUrl("/")).toBe("https://docs.orchestrator.inc/");
    expect(absoluteDocsUrl("/llms-full.txt")).toBe("https://docs.orchestrator.inc/llms-full.txt");
    expect(absoluteDocsUrl("/search?q=ci")).toBe("https://docs.orchestrator.inc/search?q=ci");
  });
});
