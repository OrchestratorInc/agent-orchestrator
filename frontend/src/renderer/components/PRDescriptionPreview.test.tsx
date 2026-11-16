import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { SessionPRSummary } from "../hooks/useSessionScmSummary";
import { noPRDetails } from "../lib/pr-display";
import { resetPRTabs, usePRTabs } from "../lib/pr-tabs";
import { PRDescriptionPreview } from "./PRDescriptionPreview";

const summary = (overrides: Partial<SessionPRSummary> = {}): SessionPRSummary => ({
  ...noPRDetails(),
  url: "https://api.github.com/repos/acme/repo/pulls/7",
  htmlUrl: "https://github.com/acme/repo/pull/7",
  number: 7,
  title: "Fix dashboard",
  state: "open",
  provider: "github",
  repo: "acme/repo",
  author: "ada",
  sourceBranch: "fix/dashboard",
  targetBranch: "main",
  headSha: "abc123",
  additions: 10,
  deletions: 3,
  changedFiles: 2,
  ci: { autoInjectCI: true, state: "passing", failingChecks: [], checks: [] },
  review: { decision: "none", hasUnresolvedHumanComments: false, unresolvedBy: [] },
  mergeability: { state: "mergeable", reasons: [], prUrl: "https://github.com/acme/repo/pull/7", conflictFiles: [] },
  updatedAt: "2026-10-22T00:00:00Z",
  detailsObservedAt: "2026-10-22T00:00:00Z",
  ...overrides,
});

const KEY = "sess-1";

/** Which PR tab the session view would bring forward. */
function ActiveTab() {
  const tabs = usePRTabs(KEY);
  return <output data-testid="active-tab">{tabs.active ?? ""}</output>;
}

const renderPreview = (pr: SessionPRSummary, cloud = false) =>
  render(<><PRDescriptionPreview cloud={cloud} pr={pr} sessionKey={KEY} /><ActiveTab /></>);

describe("PRDescriptionPreview", () => {
  afterEach(() => resetPRTabs());

  it("shows three lines of the body and opens the whole PR in its tab", async () => {
    const heights = [
      vi.spyOn(HTMLElement.prototype, "scrollHeight", "get").mockReturnValue(120),
      vi.spyOn(HTMLElement.prototype, "clientHeight", "get").mockReturnValue(72),
    ];
    try {
      renderPreview(summary({ description: "Fixes two bugs.\n\n## Fixes\n\n- the **checkpoint**" }));
      expect(screen.getByRole("heading", { name: "Fixes" }).parentElement).toHaveClass("line-clamp-3");
      const read = screen.getByRole("button", { name: "Read full description" });
      expect(read).toHaveAttribute("aria-pressed", "false");
      await userEvent.click(read);
      expect(screen.getByTestId("active-tab")).toHaveTextContent("https://api.github.com/repos/acme/repo/pulls/7");
      expect(read).toHaveAttribute("aria-pressed", "true");
    } finally {
      for (const spy of heights) spy.mockRestore();
    }
  });

  it("counts the images in a line under the text, loading no image in the card, and says nothing of tables", async () => {
    const { container } = renderPreview(summary({
      description: "Before and after:\n\n| a | b |\n| --- | --- |\n| ![Old](https://example.com/old.png) | <img alt=\"New\" src=\"https://example.com/new.png\"> |",
    }));
    expect(container.querySelector("img")).toBeNull();
    const media = within(screen.getByTestId("pr-description-media"));
    expect(media.queryByText(/table/)).not.toBeInTheDocument();
    await userEvent.click(media.getByRole("button", { name: "2 images" }));
    expect(within(screen.getByTestId("pr-image-gallery")).getByText("1 of 2")).toBeInTheDocument();
  });

  it("offers no full view for a short body with nothing more to show", () => {
    renderPreview(summary({ description: "Fixes the dashboard." }));
    expect(screen.getByText("Fixes the dashboard.")).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Read full description" })).not.toBeInTheDocument();
  });

  it("offers no full view for a Cloud PR, which has no tab", () => {
    renderPreview(summary({ description: "Long.\n\n![shot](https://example.com/shot.png)" }), true);
    expect(screen.queryByRole("button", { name: "Read full description" })).not.toBeInTheDocument();
  });

  it("says so when details have not been observed yet", () => {
    renderPreview(summary({ detailsObservedAt: undefined }));
    expect(screen.getByText("Details load on the next sync.")).toBeInTheDocument();
  });
});
