import { describe, expect, it } from "vitest";
import { docsIssueUrl } from "./docs-issue";

function params(url: string) {
  const parsed = new URL(url);
  return { base: `${parsed.origin}${parsed.pathname}`, query: parsed.searchParams, raw: parsed.search };
}

describe("docsIssueUrl", () => {
  it("opens a new issue with the docs labels, the page title and the page URL", () => {
    const { base, query } = params(docsIssueUrl({ title: "Quickstart", pageUrl: "https://docs.orchestrator.inc/quickstart/" }));
    expect(base).toBe("https://github.com/OrchestratorInc/agent-orchestrator/issues/new");
    expect(query.get("title")).toBe("Docs: Quickstart");
    expect(query.get("labels")).toBe("documentation,comp/docs-site");
    expect(query.get("body")).toBe("Page: https://docs.orchestrator.inc/quickstart/\n\nWhat's wrong or missing:\n\n");
  });

  it("encodes titles with &, : and # so they cannot start another parameter", () => {
    const { query, raw } = params(docsIssueUrl({ title: "Git & GitHub: #1 setup?", pageUrl: "https://docs.orchestrator.inc/a/" }));
    expect(query.get("title")).toBe("Docs: Git & GitHub: #1 setup?");
    expect(raw).not.toMatch(/[ #]|& /);
    expect([...query.keys()]).toEqual(["title", "labels", "body"]);
  });

  it("keeps non-Latin titles and the translated page URL", () => {
    const { query } = params(docsIssueUrl({ title: "クイックスタート", pageUrl: "https://docs.orchestrator.inc/ja/quickstart/" }));
    expect(query.get("title")).toBe("Docs: クイックスタート");
    expect(query.get("body")).toContain("Page: https://docs.orchestrator.inc/ja/quickstart/");
  });
});
