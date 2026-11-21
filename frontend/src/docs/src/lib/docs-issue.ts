import { COMPANY, DOCS_ISSUE_LABELS } from "./constants";

/**
 * A new GitHub issue about a docs page, prefilled with the page title and
 * the URL the reader was on, so a report says which language is wrong.
 * The text stays in English because maintainers triage it.
 */
export function docsIssueUrl({ title, pageUrl }: { title: string; pageUrl: string }): string {
  const params = new URLSearchParams({
    title: `Docs: ${title}`,
    labels: DOCS_ISSUE_LABELS.join(","),
    body: `Page: ${pageUrl}\n\nWhat's wrong or missing:\n\n`,
  });
  return `${COMPANY.GITHUB_URL}/issues/new?${params}`;
}
