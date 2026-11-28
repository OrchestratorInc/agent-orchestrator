import { fireEvent, render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import type { SessionPRSummary } from "../hooks/useSessionScmSummary";
import { noPRDetails } from "../lib/pr-display";
import { descriptionMedia } from "../lib/pr-description";
import { PRDescriptionBody } from "./PRDescriptionBody";

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

const renderDescription = (pr: SessionPRSummary, cloud = false) => render(<PRDescriptionBody cloud={cloud} pr={pr} />);

describe("PRDescriptionBody", () => {
  it("renders the body as markdown that never runs raw HTML", () => {
    const { container } = renderDescription(
      loaded({ description: "Fixes two bugs.\n\n## Fixes\n\n- the **checkpoint**\n\n<script>window.__pwned = true</script><img src=x onerror=\"window.__pwned = true\">" }),
    );
    expect(screen.getByRole("heading", { name: "Fixes" })).toBeInTheDocument();
    expect(screen.getByText("checkpoint")).toBeInTheDocument();
    expect(container.querySelector("script")).toBeNull();
    expect(container.querySelector("img[onerror]")).toBeNull();
    expect((window as unknown as { __pwned?: boolean }).__pwned).toBeUndefined();
  });

  it("drops an opening heading that only repeats the Description title, and keeps the rest", () => {
    renderDescription(loaded({ description: "## Summary\n\nAdds the docs.\n\n## Testing\n\nBuild passes." }));
    expect(screen.queryByRole("heading", { name: "Summary" })).not.toBeInTheDocument();
    expect(screen.getByText("Adds the docs.")).toBeInTheDocument();
    expect(screen.getByRole("heading", { name: "Testing" })).toBeInTheDocument();
  });

  it("keeps an opening heading that says something of its own", () => {
    renderDescription(loaded({ description: "## Breaking changes\n\nThe old flag is gone." }));
    expect(screen.getByRole("heading", { name: "Breaking changes" })).toBeInTheDocument();
    expect(screen.getByText("The old flag is gone.")).toBeInTheDocument();
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

  it("says when an image cannot load, and keeps the link to the original", async () => {
    renderDescription(loaded({ description: "![shot](https://example.com/private.png)" }));
    await userEvent.click(screen.getByRole("button", { name: "Open image: shot" }));
    fireEvent.error(within(screen.getByTestId("pr-image-gallery")).getByRole("img", { name: "shot" }));
    expect(screen.getByText(/can't be shown here/)).toBeInTheDocument();
    expect(screen.getByRole("link", { name: /Open original/ })).toHaveAttribute("href", "https://example.com/private.png");
  });

  it("shows images full size, each opening the gallery at itself", async () => {
    renderDescription(loaded({ description: "Text.\n\n![one](https://example.com/1.png)\n\n![two](https://example.com/2.png)" }));
    const pane = within(screen.getByTestId("pr-description"));
    await userEvent.click(pane.getByRole("button", { name: "Open image: two" }));
    expect(within(screen.getByTestId("pr-image-gallery")).getByText("2 of 2")).toBeInTheDocument();
    await userEvent.click(within(screen.getByTestId("pr-image-gallery")).getByRole("button", { name: "Next image" }));
    expect(within(screen.getByTestId("pr-image-gallery")).getByText("1 of 2")).toBeInTheDocument();
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

  it("opens a linked image as the image alone, not the link too", async () => {
    renderDescription(loaded({ description: "Demo:\n\n[![demo](https://example.com/demo.png)](https://example.com/video)" }));
    const pane = within(screen.getByTestId("pr-description"));
    expect(pane.getByRole("button", { name: "Open image: demo" }).closest("a")).toBeNull();
    await userEvent.click(pane.getByRole("button", { name: "Open image: demo" }));
    expect(screen.getByTestId("pr-image-gallery")).toBeInTheDocument();
  });

  it("keeps a body that is only a heading", () => {
    renderDescription(loaded({ description: "## Work in progress" }));
    expect(screen.getByRole("heading", { name: "Work in progress" })).toBeInTheDocument();
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

  it("says when the body was cut short", () => {
    renderDescription(loaded({ description: "first\n\nsecond", descriptionTruncated: true }));
    expect(screen.getByText("The description is shortened.")).toBeInTheDocument();
  });

  it("says a PR has no description once details are loaded", () => {
    renderDescription(loaded());
    expect(screen.getByText("No description.")).toBeInTheDocument();
  });
});
