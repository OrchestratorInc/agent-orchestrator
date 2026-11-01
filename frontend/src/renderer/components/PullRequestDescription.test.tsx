import { fireEvent, render, screen } from "@testing-library/react";
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
  const overflowing = () => [
    vi.spyOn(HTMLElement.prototype, "scrollHeight", "get").mockReturnValue(120),
    vi.spyOn(HTMLElement.prototype, "clientHeight", "get").mockReturnValue(72),
  ];

  it("renders the body as markdown clamped to four lines that never renders raw HTML, and opens it in place", async () => {
    const heights = overflowing();
    const { container } = renderDescription(
      loaded({ description: "Fixes two bugs.\n\n## Fixes\n\n- the **checkpoint**\n\n<script>window.__pwned = true</script><img src=x onerror=\"window.__pwned = true\">" }),
    );

    const body = screen.getByRole("heading", { name: "Fixes" }).parentElement as HTMLElement;
    expect(body).toHaveClass("line-clamp-4");
    const toggle = screen.getByRole("button", { name: "Show more" });
    expect(toggle).toHaveAttribute("aria-expanded", "false");
    await userEvent.click(toggle);

    expect(body).not.toHaveClass("line-clamp-4");
    expect(screen.getByRole("button", { name: "Show less" })).toHaveAttribute("aria-expanded", "true");
    expect(screen.getByRole("link", { name: /Open PR #7/ })).toHaveAttribute("href", "https://github.com/acme/repo/pull/7");
    expect(container.querySelector("script")).toBeNull();
    expect(container.querySelector("img[onerror]")).toBeNull();
    expect((window as unknown as { __pwned?: boolean }).__pwned).toBeUndefined();
    for (const spy of heights) spy.mockRestore();
  });

  it("drops an opening heading that only repeats the Description title, and keeps the rest", () => {
    renderDescription(loaded({ description: "## Summary\n\nAdds the docs.\n\n## Testing\n\nBuild passes." }));
    expect(screen.queryByRole("heading", { name: "Summary" })).not.toBeInTheDocument();
    expect(screen.getByText("Adds the docs.")).toBeInTheDocument();
    expect(screen.getByRole("heading", { name: "Testing" })).toBeInTheDocument();
  });

  it("shows raw HTML as its text, keys as code, and leaves code spans alone", () => {
    const { container } = renderDescription(loaded({
      description: "Press <kbd>⌘K</kbd> to open <b>Search</b>.<!-- template hint -->\n\nUse `<div>` here.\n\n```\n<span>kept</span>\n```",
    }));
    expect(container.textContent).toContain("Press ⌘K to open Search.");
    expect(screen.getByText("⌘K").tagName).toBe("CODE");
    expect(container.textContent).not.toContain("<kbd>");
    expect(container.textContent).not.toContain("template hint");
    expect(screen.getByText("<div>").tagName).toBe("CODE");
    expect(container.textContent).toContain("<span>kept</span>");
  });

  it("shows images as placeholders that load nothing until one opens in a lightbox", async () => {
    const { container } = renderDescription(loaded({
      description: "Before:\n\n![Old header](https://example.com/old.png)\n\n<img width=\"600\" alt=\"New header\" src=\"https://github.com/user-attachments/assets/abc?x=1&amp;y=2\">",
    }));
    expect(container.querySelector("img")).toBeNull();
    const placeholders = screen.getAllByTestId("pr-description-image");
    expect(placeholders.map((button) => button.textContent)).toEqual(["Old header", "New header"]);

    await userEvent.click(screen.getByRole("button", { name: "Open image: New header" }));
    const dialog = screen.getByRole("dialog", { name: "New header" });
    expect(dialog.querySelector("img")).toHaveAttribute("src", "https://github.com/user-attachments/assets/abc?x=1&y=2");
    expect(screen.getByRole("link", { name: /Open original/ })).toHaveAttribute("href", "https://github.com/user-attachments/assets/abc?x=1&y=2");
  });

  it("keeps an open image open when the card re-renders with fresh details", async () => {
    const description = "![shot](https://example.com/shot.png)";
    const view = renderDescription(loaded({ description }));
    await userEvent.click(screen.getByRole("button", { name: "Open image: shot" }));
    view.rerender(<PullRequestDescription cloud={false} pr={loaded({ description, updatedAt: "2026-10-23T00:00:00Z" })} />);
    expect(screen.getByRole("dialog", { name: "shot" })).toBeInTheDocument();
  });

  it("says when an image cannot load, and keeps the link to the original", async () => {
    renderDescription(loaded({ description: "![shot](https://example.com/private.png)" }));
    await userEvent.click(screen.getByRole("button", { name: "Open image: shot" }));
    fireEvent.error(screen.getByRole("dialog").querySelector("img") as HTMLImageElement);
    expect(screen.getByText(/can't be shown here/)).toBeInTheDocument();
    expect(screen.getByRole("link", { name: /Open original/ })).toHaveAttribute("href", "https://example.com/private.png");
  });

  it("opens a linked image instead of nesting its button in the link, and never opens a non-web source", () => {
    renderDescription(loaded({ description: "[![demo](https://example.com/demo.png)](https://example.com/video)\n\n![local](file:///etc/passwd)" }));
    expect(screen.getByRole("button", { name: "Open image: demo" }).closest("a")).toBeNull();
    expect(screen.queryByRole("button", { name: "Open image: local" })).not.toBeInTheDocument();
    expect(screen.getByText("local")).toBeInTheDocument();
  });

  it("keeps a body that is only a heading", () => {
    renderDescription(loaded({ description: "## Work in progress" }));
    expect(screen.getByRole("heading", { name: "Work in progress" })).toBeInTheDocument();
  });

  it("folds back from Show less", async () => {
    const heights = overflowing();
    renderDescription(loaded({ description: "first\n\nsecond" }));
    await userEvent.click(screen.getByRole("button", { name: "Show more" }));
    await userEvent.click(screen.getByRole("button", { name: "Show less" }));
    expect(screen.getByRole("button", { name: "Show more" })).toBeInTheDocument();
    expect(screen.queryByRole("link", { name: /Open PR #7/ })).not.toBeInTheDocument();
    for (const spy of heights) spy.mockRestore();
  });

  it("offers no toggle for a body that four lines already hold", () => {
    renderDescription(loaded({ description: "Fixes the dashboard." }));
    expect(screen.getByText("Fixes the dashboard.")).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Show more" })).not.toBeInTheDocument();
  });

  it("says when the body was cut short, once it is open, even if it fits", async () => {
    renderDescription(loaded({ description: "first\n\nsecond", descriptionTruncated: true }));
    expect(screen.queryByText("The description is shortened.")).not.toBeInTheDocument();
    await userEvent.click(screen.getByRole("button", { name: "Show more" }));
    expect(screen.getByText("The description is shortened.")).toBeInTheDocument();
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
