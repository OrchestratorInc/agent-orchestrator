import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import type { SessionPRSummary } from "../hooks/useSessionScmSummary";
import { noPRDetails } from "../lib/pr-display";
import { PullRequestDescription } from "./PullRequestDescription";

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
  ...overrides,
});

const loaded = (overrides: Partial<SessionPRSummary> = {}) => summary({ detailsObservedAt: "2026-10-22T00:00:00Z", ...overrides });

const renderDescription = (pr: SessionPRSummary, cloud = false) => render(<PullRequestDescription cloud={cloud} pr={pr} />);

describe("PullRequestDescription", () => {
  it("previews the body as two plain lines, then expands in place to markdown that never renders raw HTML", async () => {
    const { container } = renderDescription(
      loaded({ description: "## Fixes\n\n- the **checkpoint**\n\n<script>window.__pwned = true</script><img src=x onerror=\"window.__pwned = true\">" }),
    );

    expect(screen.getByText("the checkpoint")).toHaveClass("line-clamp-2");
    expect(screen.queryByRole("heading", { name: "Fixes" })).not.toBeInTheDocument();
    const toggle = screen.getByRole("button", { name: "Show more" });
    expect(toggle).toHaveAttribute("aria-expanded", "false");
    await userEvent.click(toggle);

    expect(await screen.findByRole("heading", { name: "Fixes" })).toBeInTheDocument();
    expect(screen.getAllByRole("button", { name: "Show less" })[0]).toHaveAttribute("aria-expanded", "true");
    expect(screen.getByRole("link", { name: /Open PR #7/ })).toHaveAttribute("href", "https://github.com/acme/repo/pull/7");
    expect(container.querySelector("script")).toBeNull();
    expect(container.querySelector("img[onerror]")).toBeNull();
    expect((window as unknown as { __pwned?: boolean }).__pwned).toBeUndefined();
  });

  it("folds back from the Show less at the end of a long body", async () => {
    renderDescription(loaded({ description: "first\n\nsecond" }));
    await userEvent.click(screen.getByRole("button", { name: "Show more" }));
    // The full body mounts once the preview has faded out.
    await screen.findByRole("link", { name: /Open PR #7/ });
    const [, bottom] = screen.getAllByRole("button", { name: "Show less" });
    await userEvent.click(bottom);
    expect(await screen.findByRole("button", { name: "Show more" })).toBeInTheDocument();
  });

  it("offers no toggle for a short plain body that two lines already hold", () => {
    renderDescription(loaded({ description: "Fixes the dashboard." }));
    expect(screen.getByText("Fixes the dashboard.")).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Show more" })).not.toBeInTheDocument();
  });

  it("offers the toggle when a plain body runs past two lines", () => {
    const heights = [
      vi.spyOn(HTMLElement.prototype, "scrollHeight", "get").mockReturnValue(60),
      vi.spyOn(HTMLElement.prototype, "clientHeight", "get").mockReturnValue(36),
    ];
    renderDescription(loaded({ description: "A plain body long enough to wrap past the preview's two lines." }));
    expect(screen.getByRole("button", { name: "Show more" })).toBeInTheDocument();
    for (const spy of heights) spy.mockRestore();
  });

  it("says when the body was cut short, once it is open", async () => {
    renderDescription(loaded({ description: "first\n\nsecond", descriptionTruncated: true }));
    await userEvent.click(screen.getByRole("button", { name: "Show more" }));
    expect(await screen.findByText("The description is shortened.")).toBeInTheDocument();
  });

  it("says so when details have not been observed yet, instead of claiming there are none", () => {
    renderDescription(summary());
    expect(screen.getByText("Details load on the next sync.")).toBeInTheDocument();
    expect(screen.queryByText("No description.")).not.toBeInTheDocument();
  });

  it("does not promise details that only the local GitHub observer fetches", () => {
    renderDescription(summary({ provider: "gitlab" }));
    expect(screen.getByText("Not available for this pull request yet.")).toBeInTheDocument();
  });

  it("does not promise details for a Cloud session", () => {
    renderDescription(summary(), true);
    expect(screen.getByText("Not available for this pull request yet.")).toBeInTheDocument();
  });

  it("says a PR has no description once details are loaded", () => {
    renderDescription(loaded());
    expect(screen.getByText("No description.")).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Show more" })).not.toBeInTheDocument();
  });
});
