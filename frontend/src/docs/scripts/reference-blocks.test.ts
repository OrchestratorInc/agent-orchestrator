// @vitest-environment node
import { execFileSync } from "node:child_process";
import { existsSync, mkdtempSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { evaluate } from "@mdx-js/mdx";
import { remarkLLMs } from "fumadocs-core/mdx-plugins/remark-llms";
import { createElement } from "react";
import * as runtime from "react/jsx-runtime";
import { renderToStaticMarkup } from "react-dom/server";
import remarkGfm from "remark-gfm";
import { afterEach, expect, it, vi } from "vitest";
import { generateReferences, remarkReferenceBlocks } from "./reference-blocks.mjs";

vi.mock("node:child_process", () => ({ execFileSync: vi.fn() }));
const roots: string[] = [];
const rows = [
  { id: "copilot", terminal: true, chat: false, reviewer: true },
  { id: "docker-agent", terminal: false, chat: true, reviewer: false },
];
const marker = "{/* docsref:shipped-harnesses */}";
const headers = [
  "| Harness | Terminal UI | Chat | Reviewer |",
  "| ハーネス | ターミナル | チャット | レビュアー |",
];
const fixture = (header = headers[0]) => `${marker}\n\n${header}\n| --- | --- | --- | --- |\n\nAvailability notes stay hand-written.\n`;

afterEach(() => {
  vi.resetAllMocks();
  for (const root of roots.splice(0)) rmSync(root, { recursive: true, force: true });
});

async function render(source: string, data = rows, path = "index.mdx") {
  const compiled = await evaluate({ value: source, path }, {
    ...runtime,
    remarkPlugins: [remarkGfm, [remarkReferenceBlocks, { rows: data }], [remarkLLMs, { headingIds: false }]],
  });
  return {
    html: renderToStaticMarkup(createElement(compiled.default)),
    markdown: compiled._markdown as string,
  };
}

it.each(headers)("shares data between HTML and processed Markdown with authored headings: %s", async (header) => {
  const source = fixture(header);
  const { html, markdown } = await render(source, rows, header === headers[0] ? "index.mdx" : "index.ja.mdx");
  expect(html).toContain("<tr><td><code>copilot</code></td><td>Yes</td><td>No</td><td>Yes</td></tr>");
  expect(html).toContain("<tr><td><code>docker-agent</code></td><td>No</td><td>Yes</td><td>No</td></tr>");
  expect(markdown).toMatch(/\| `copilot`\s*\| Yes\s*\| No\s*\| Yes\s*\|/);
  expect(markdown).toMatch(/\| `docker-agent`\s*\| No\s*\| Yes\s*\| No\s*\|/);
  for (const heading of header.split("|").map((cell) => cell.trim()).filter(Boolean)) {
    expect(html).toContain(`<th>${heading}</th>`);
    expect(markdown).toContain(heading);
  }
  expect(markdown).toContain("Availability notes stay hand-written.");
  expect(html + markdown).not.toContain("docsref:");
});

it("changes a rendered capability when the registry export changes, with identical MDX", async () => {
  const source = fixture();
  const before = await render(source);
  const after = await render(source, rows.map((row) => ({ ...row, chat: true })));
  expect(before.html).toContain("<td>No</td>");
  expect(after.html).toContain("<tr><td><code>copilot</code></td><td>Yes</td><td>Yes</td><td>Yes</td></tr>");
  expect(after.markdown).toMatch(/\| `copilot`\s*\| Yes\s*\| Yes\s*\| Yes\s*\|/);
  expect(after.markdown).not.toEqual(before.markdown);
});

it.each([
  ["unknown marker", fixture().replace("shipped-harnesses", "typo")],
  ["missing header", marker],
  ["wrong column count", `${marker}\n\n| Harness | Chat |\n| --- | --- |`],
  ["stale authored rows", fixture().replace("| --- | --- | --- | --- |", "| --- | --- | --- | --- |\n| old | Yes | Yes | Yes |")],
])("fails on %s", async (_name, source) => {
  await expect(render(source)).rejects.toThrow(/marker/i);
});

it.each([
  [],
  [rows[0], rows[0]],
  [...rows].reverse(),
  [{ ...rows[0], id: "a sentence or <markup>" }],
  [{ ...rows[0], chat: "Yes" }],
  [{ ...rows[0], description: "Prose must not be generated" }],
])("rejects empty, unsorted or non-data exports: %j", async (...data) => {
  // Vitest spreads array cases into arguments.
  await expect(render(fixture(), data as typeof rows)).rejects.toThrow(/Docs references:/);
});

it.each(["missing Go", "invalid JSON", "empty export"])("removes stale data and fails the build on %s", (failure) => {
  const root = mkdtempSync(join(tmpdir(), "docsref-"));
  roots.push(root);
  const output = join(root, "agent-harnesses.json");
  writeFileSync(output, JSON.stringify(rows));
  const run = vi.mocked(execFileSync);
  if (failure === "missing Go") run.mockImplementation(() => { throw Object.assign(new Error("spawn go ENOENT"), { code: "ENOENT" }); });
  else run.mockReturnValue(failure === "invalid JSON" ? "{" : "[]");
  expect(() => generateReferences(output)).toThrow(/Install Go.*No cached table/);
  expect(existsSync(output)).toBe(false);
});
