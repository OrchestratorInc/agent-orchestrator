import { fireEvent, render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { SessionPRSummary } from "../hooks/useSessionScmSummary";
import { noPRDetails } from "../lib/pr-display";
import { closePRDescription, resetPRDescriptionPanes, usePRDescriptionPane } from "../lib/pr-description-pane";
import { PRDescriptionPane } from "./PRDescriptionPane";
import { descriptionMedia, PullRequestDescription } from "./PullRequestDescription";

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

const KEY = "sess-1";

/** What the session view would draw in its main area. */
function OpenPane() {
  const pr = usePRDescriptionPane(KEY);
  return pr ? <PRDescriptionPane onClose={() => closePRDescription(KEY)} pr={pr} /> : null;
}

const renderDescription = (pr: SessionPRSummary, cloud = false) => render(<><PullRequestDescription cloud={cloud} pr={pr} sessionKey={KEY} /><OpenPane /></>);

describe("PullRequestDescription", () => {
  afterEach(() => resetPRDescriptionPanes());
  const overflowing = () => [
    vi.spyOn(HTMLElement.prototype, "scrollHeight", "get").mockReturnValue(120),
    vi.spyOn(HTMLElement.prototype, "clientHeight", "get").mockReturnValue(72),
  ];

  it("shows three lines of the body as markdown that never renders raw HTML, and opens the whole body in the main area", async () => {
    const heights = overflowing();
    try {
      const { container } = renderDescription(
        loaded({ description: "Fixes two bugs.\n\n## Fixes\n\n- the **checkpoint**\n\n<script>window.__pwned = true</script><img src=x onerror=\"window.__pwned = true\">" }),
      );

      const body = screen.getByRole("heading", { name: "Fixes" }).parentElement as HTMLElement;
      expect(body).toHaveClass("line-clamp-3");
      expect(screen.queryByTestId("pr-description-pane")).not.toBeInTheDocument();
      await userEvent.click(screen.getByRole("button", { name: "Read full description" }));

      const pane = within(screen.getByTestId("pr-description-pane"));
      expect(pane.getByRole("heading", { name: "#7 Fix dashboard" })).toBeInTheDocument();
      expect(pane.getByText("checkpoint")).toBeInTheDocument();
      expect(pane.getByRole("link", { name: /Open PR #7/ })).toHaveAttribute("href", "https://github.com/acme/repo/pull/7");
      expect(screen.getByRole("button", { name: "Read full description" })).toHaveAttribute("aria-pressed", "true");
      expect(container.querySelector("script")).toBeNull();
      expect(container.querySelector("img[onerror]")).toBeNull();
      expect((window as unknown as { __pwned?: boolean }).__pwned).toBeUndefined();
    } finally {
      for (const spy of heights) spy.mockRestore();
    }
  });

  it("closes the full description with its close button or Escape", async () => {
    renderDescription(loaded({ description: "Short.\n\n![shot](https://example.com/shot.png)" }));
    await userEvent.click(screen.getByRole("button", { name: "Read full description" }));
    await userEvent.click(screen.getByRole("button", { name: "Close description" }));
    expect(screen.queryByTestId("pr-description-pane")).not.toBeInTheDocument();

    await userEvent.click(screen.getByRole("button", { name: "Read full description" }));
    await userEvent.keyboard("{Escape}");
    expect(screen.queryByTestId("pr-description-pane")).not.toBeInTheDocument();
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

  it("counts the images and tables in a line under the text, loading no image in the card", () => {
    const { container } = renderDescription(loaded({
      description: "Before and after:\n\n| a | b |\n| --- | --- |\n| ![Old](https://example.com/old.png) | <img alt=\"New\" src=\"https://example.com/new.png\"> |",
    }));
    expect(container.querySelector("img")).toBeNull();
    const media = within(screen.getByTestId("pr-description-media"));
    expect(media.getByRole("button", { name: "2 images" })).toBeInTheDocument();
    expect(media.getByText("1 table")).toBeInTheDocument();
  });

  it("opens the images in a gallery stepped through with the arrows, keys and thumbnails", async () => {
    renderDescription(loaded({
      description: "Before:\n\n![Old header](https://example.com/old.png)\n\n<img width=\"600\" alt=\"New header\" src=\"https://github.com/user-attachments/assets/abc?x=1&amp;y=2\">",
    }));
    await userEvent.click(screen.getByRole("button", { name: "2 images" }));
    const gallery = within(screen.getByTestId("pr-image-gallery"));
    expect(gallery.getByRole("img", { name: "Old header" })).toHaveAttribute("src", "https://example.com/old.png");
    expect(gallery.getByText("1 of 2")).toBeInTheDocument();

    await userEvent.click(gallery.getByRole("button", { name: "Next image" }));
    expect(gallery.getByRole("img", { name: "New header" })).toHaveAttribute("src", "https://github.com/user-attachments/assets/abc?x=1&y=2");
    expect(gallery.getByRole("link", { name: /Open original/ })).toHaveAttribute("href", "https://github.com/user-attachments/assets/abc?x=1&y=2");
    await userEvent.keyboard("{ArrowRight}");
    expect(gallery.getByText("1 of 2")).toBeInTheDocument();
    await userEvent.click(gallery.getByRole("button", { name: "Show image 2" }));
    expect(gallery.getByText("2 of 2")).toBeInTheDocument();
  });

  it("keeps the open full description current when the PR's details change", async () => {
    const view = renderDescription(loaded({ description: "First take.\n\n![shot](https://example.com/shot.png)" }));
    await userEvent.click(screen.getByRole("button", { name: "Read full description" }));
    expect(within(screen.getByTestId("pr-description-pane")).getByText("First take.")).toBeInTheDocument();
    view.rerender(<><PullRequestDescription cloud={false} pr={loaded({ description: "Second take.\n\n![shot](https://example.com/shot.png)", updatedAt: "2026-10-23T00:00:00Z" })} sessionKey={KEY} /><OpenPane /></>);
    expect(within(screen.getByTestId("pr-description-pane")).getByText("Second take.")).toBeInTheDocument();
  });

  it("says when an image cannot load, and keeps the link to the original", async () => {
    renderDescription(loaded({ description: "![shot](https://example.com/private.png)" }));
    await userEvent.click(screen.getByRole("button", { name: "1 image" }));
    fireEvent.error(within(screen.getByTestId("pr-image-gallery")).getByRole("img", { name: "shot" }));
    expect(screen.getByText(/can't be shown here/)).toBeInTheDocument();
    expect(screen.getByRole("link", { name: /Open original/ })).toHaveAttribute("href", "https://example.com/private.png");
  });

  it("shows images full size in the main area, each opening the gallery at itself", async () => {
    renderDescription(loaded({ description: "Text.\n\n![one](https://example.com/1.png)\n\n![two](https://example.com/2.png)" }));
    await userEvent.click(screen.getByRole("button", { name: "Read full description" }));
    const pane = within(screen.getByTestId("pr-description-pane"));
    await userEvent.click(pane.getByRole("button", { name: "Open image: two" }));
    expect(within(screen.getByTestId("pr-image-gallery")).getByText("2 of 2")).toBeInTheDocument();
    // Escape closes the gallery first, and leaves the description open.
    await userEvent.keyboard("{Escape}");
    expect(screen.getByTestId("pr-description-pane")).toBeInTheDocument();
  });

  it("reads images and tables the way the renderer does, and never counts what sits in code", () => {
    const markdown = [
      "[![demo](https://example.com/demo.png)](https://example.com/video)",
      "![paren](https://example.com/shot_(1).png 'a title')",
      "![ref][shot]",
      "![local](file:///etc/passwd)",
      "```\n![code](https://example.com/c.png)\n| a | b |\n| --- | --- |\n```",
      "    ![indented](https://example.com/i.png)",
      "| a | b |\n| --- | --- |\n| 1 | 2 |",
      "[shot]: https://example.com/ref.png",
    ].join("\n\n");
    const { images, tables } = descriptionMedia(markdown);
    expect(images).toEqual([
      { alt: "demo", src: "https://example.com/demo.png" },
      { alt: "paren", src: "https://example.com/shot_(1).png" },
      { alt: "ref", src: "https://example.com/ref.png" },
    ]);
    expect(tables).toBe(1);
  });

  it("opens a linked image in the full view as the image alone, not the link too", async () => {
    renderDescription(loaded({ description: "Demo:\n\n[![demo](https://example.com/demo.png)](https://example.com/video)" }));
    await userEvent.click(screen.getByRole("button", { name: "Read full description" }));
    const pane = within(screen.getByTestId("pr-description-pane"));
    expect(pane.getByRole("button", { name: "Open image: demo" }).closest("a")).toBeNull();
    await userEvent.click(pane.getByRole("button", { name: "Open image: demo" }));
    expect(screen.getByTestId("pr-image-gallery")).toBeInTheDocument();
  });

  it("keeps a body that is only a heading", () => {
    renderDescription(loaded({ description: "## Work in progress" }));
    expect(screen.getByRole("heading", { name: "Work in progress" })).toBeInTheDocument();
  });

  it("offers no full view for a short body with nothing more to show", () => {
    renderDescription(loaded({ description: "Fixes the dashboard." }));
    expect(screen.getByText("Fixes the dashboard.")).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Read full description" })).not.toBeInTheDocument();
    expect(screen.queryByTestId("pr-description-media")).not.toBeInTheDocument();
  });

  it("says in the full view when the body was cut short, even if it fits the card", async () => {
    renderDescription(loaded({ description: "first\n\nsecond", descriptionTruncated: true }));
    expect(screen.queryByText("The description is shortened.")).not.toBeInTheDocument();
    await userEvent.click(screen.getByRole("button", { name: "Read full description" }));
    expect(within(screen.getByTestId("pr-description-pane")).getByText("The description is shortened.")).toBeInTheDocument();
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
    expect(screen.queryByRole("button", { name: "Read full description" })).not.toBeInTheDocument();
  });
});
