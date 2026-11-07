import { readFileSync } from "node:fs";
import { dirname, join, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";
import { DOCS_ISSUE_LABELS } from "./constants";
import { DOCS_ISSUE_TEMPLATE, docsIssueUrl } from "./docs-issue";

function params(url: string) {
  const parsed = new URL(url);
  return { base: `${parsed.origin}${parsed.pathname}`, query: parsed.searchParams, raw: parsed.search };
}

// The repository root is five levels above this file.
const repoRoot = resolve(dirname(fileURLToPath(import.meta.url)), "../../../../..");
const template = readFileSync(join(repoRoot, ".github/ISSUE_TEMPLATE", DOCS_ISSUE_TEMPLATE), "utf8");

describe("docsIssueUrl", () => {
  it("opens the docs issue form with the page title and the page URL", () => {
    const { base, query } = params(docsIssueUrl({ title: "Quickstart", pageUrl: "https://docs.orchestrator.inc/quickstart/" }));
    expect(base).toBe("https://github.com/OrchestratorInc/agent-orchestrator/issues/new");
    expect(query.get("template")).toBe("docs.yml");
    expect(query.get("title")).toBe("Docs: Quickstart");
    expect(query.get("page-url")).toBe("https://docs.orchestrator.inc/quickstart/");
  });

  it("leaves the labels to the issue form, which applies them for reporters without triage access", () => {
    const { query } = params(docsIssueUrl({ title: "Quickstart", pageUrl: "https://docs.orchestrator.inc/quickstart/" }));
    expect(query.has("labels")).toBe(false);
    expect(query.has("body")).toBe(false);
  });

  it("encodes titles with &, : and # so they cannot start another parameter", () => {
    const { query, raw } = params(docsIssueUrl({ title: "Git & GitHub: #1 setup?", pageUrl: "https://docs.orchestrator.inc/a/?x=1&y=2#z" }));
    expect(query.get("title")).toBe("Docs: Git & GitHub: #1 setup?");
    expect(query.get("page-url")).toBe("https://docs.orchestrator.inc/a/?x=1&y=2#z");
    expect(raw).not.toMatch(/[ #]|& /);
    expect([...query.keys()]).toEqual(["template", "title", "page-url"]);
  });

  it("keeps non-Latin titles and the translated page URL", () => {
    const { query } = params(docsIssueUrl({ title: "クイックスタート", pageUrl: "https://docs.orchestrator.inc/ja/quickstart/" }));
    expect(query.get("title")).toBe("Docs: クイックスタート");
    expect(query.get("page-url")).toBe("https://docs.orchestrator.inc/ja/quickstart/");
  });
});

describe("the docs issue form", () => {
  it("adds the docs labels", () => {
    const labels = template.match(/^labels:\s*\[(.*)\]\s*$/m)?.[1];
    expect(labels && [...labels.matchAll(/"([^"]+)"/g)].map((match) => match[1])).toEqual([...DOCS_ISSUE_LABELS]);
  });

  it("has the page-url field the link fills in, before the problem and the suggested fix", () => {
    const ids = [...template.matchAll(/^\s+id:\s*(\S+)\s*$/gm)].map((match) => match[1]);
    expect(ids).toEqual(["page-url", "problem", "suggestion"]);
    expect(template).toMatch(/- type: input\n\s+id: page-url/);
  });
});
