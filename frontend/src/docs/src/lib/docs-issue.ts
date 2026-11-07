import { COMPANY } from "./constants";

/** The issue form in `.github/ISSUE_TEMPLATE/` that docs reports use. */
export const DOCS_ISSUE_TEMPLATE = "docs.yml";

/**
 * A new GitHub issue about a docs page, prefilled with the page title and
 * the URL the reader was on, so a report says which language is wrong.
 * The issue form adds the docs labels, which GitHub applies for every
 * reporter; a `labels` parameter only works for people with triage access.
 * The text stays in English because maintainers triage it.
 */
export function docsIssueUrl({ title, pageUrl }: { title: string; pageUrl: string }): string {
  const params = new URLSearchParams({
    template: DOCS_ISSUE_TEMPLATE,
    title: `Docs: ${title}`,
    "page-url": pageUrl,
  });
  return `${COMPANY.GITHUB_URL}/issues/new?${params}`;
}
