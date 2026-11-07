import { setupI18n } from "@lingui/core";
import { I18nProvider } from "@lingui/react";
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it } from "vitest";
import { docsIssueUrl } from "@/lib/docs-issue";
import { PageHelpful } from "./page-actions";

afterEach(cleanup);

const issueUrl = docsIssueUrl({ title: "Quickstart", pageUrl: "https://docs.orchestrator.inc/quickstart/" });

function renderHelpful() {
  const i18n = setupI18n({ locale: "en", messages: { en: {} } });
  return render(
    <I18nProvider i18n={i18n}>
      <PageHelpful issueUrl={issueUrl} />
    </I18nProvider>,
  );
}

describe("Was this page helpful?", () => {
  it("asks, with Yes as a button and No as a link to the prefilled docs issue", () => {
    renderHelpful();
    expect(screen.getByText("Was this page helpful?")).toBeTruthy();
    expect(screen.getByRole("button", { name: "Yes" })).toBeTruthy();
    const no = screen.getByRole("link", { name: "No" });
    expect(no.getAttribute("href")).toBe(issueUrl);
    expect(no.getAttribute("target")).toBe("_blank");
  });

  it("says thanks after Yes and opens nothing", () => {
    renderHelpful();
    fireEvent.click(screen.getByRole("button", { name: "Yes" }));
    expect(screen.getByRole("status").textContent).toBe("Thanks for letting us know.");
    expect(screen.queryByRole("link")).toBeNull();
  });

  it("points to the opened issue after No", () => {
    renderHelpful();
    fireEvent.click(screen.getByRole("link", { name: "No" }));
    expect(screen.getByRole("status").textContent).toBe("Thanks. Tell us what to fix in the GitHub issue that just opened.");
  });

  it("sets no cookie", () => {
    renderHelpful();
    fireEvent.click(screen.getByRole("button", { name: "Yes" }));
    expect(document.cookie).toBe("");
  });
});
