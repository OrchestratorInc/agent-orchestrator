import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import { linkPreviewQueryKey } from "../../hooks/useLinkPreview";
import { pullRequestPreviewQueryKey, type PullRequestPreview } from "../../hooks/usePullRequestPreview";
import { queryClient } from "../../lib/query-client";
import { ChatMarkdown } from "./ChatMarkdown";
import { pullRequestChipLabel } from "./PullRequestLink";

const { getMock } = vi.hoisted(() => ({ getMock: vi.fn() }));
vi.mock("../../lib/api-client", async (importOriginal) => ({
	...await importOriginal<typeof import("../../lib/api-client")>(),
	apiClient: { GET: getMock },
}));

const PR = "https://github.com/acme/api/pull/12";

afterEach(() => {
	queryClient.clear();
	getMock.mockReset();
});

function seed(url: string, preview: Partial<PullRequestPreview>) {
	queryClient.setQueryData(pullRequestPreviewQueryKey(url), {
		url, repo: "acme/api", number: 12, title: "Fix the share menu", state: "open",
		additions: 0, deletions: 0, changedFiles: 0, ...preview,
	} satisfies PullRequestPreview);
}

describe("pullRequestChipLabel", () => {
	it("makes a chip only of text that names the same PR", () => {
		expect(pullRequestChipLabel(PR, PR)).toEqual({ number: 12 });
		expect(pullRequestChipLabel(PR, `${PR}/`)).toEqual({ number: 12 });
		expect(pullRequestChipLabel(PR, "#12")).toEqual({ number: 12 });
		expect(pullRequestChipLabel(PR, "PR #12")).toEqual({ number: 12 });
		expect(pullRequestChipLabel(PR, "acme/api#12")).toEqual({ repo: "acme/api", number: 12 });
		expect(pullRequestChipLabel(PR, "#13")).toBeUndefined();
		expect(pullRequestChipLabel(PR, "other/api#12")).toBeUndefined();
		expect(pullRequestChipLabel(PR, "the share fix")).toBeUndefined();
		expect(pullRequestChipLabel(`${PR}/files`, "#12")).toBeUndefined();
		expect(pullRequestChipLabel("https://github.com/acme/api/issues/12", "#12")).toBeUndefined();
	});
});

describe("pull request links in chat", () => {
	const text = `Reviewed ${PR}, which builds on [acme/api#9](https://github.com/acme/api/pull/9), and [the share fix](https://github.com/acme/api/pull/14).`;

	it.each([false, true])("draws PR references as chips, and a worded link as a chip with its words (streaming: %s)", (streaming) => {
		render(<ChatMarkdown streaming={streaming} text={text} />);
		const bare = screen.getByRole("link", { name: "#12" });
		expect(bare).toHaveAttribute("href", PR);
		expect(bare.className).toContain("bg-interactive-active");
		expect(bare.querySelector("[data-link-favicon]")).toBeNull();
		// Until its state is known, a chip carries GitHub's mark in the text colour.
		expect(bare.querySelector("svg")?.getAttribute("class")).toContain("text-foreground");

		const cross = screen.getByRole("link", { name: /^acme\/api\s?#9$/ });
		expect(within(cross).getByText("acme/api")).toHaveClass("text-muted-foreground");

		const worded = screen.getByRole("link", { name: "the share fix" });
		expect(worded.className).toContain("bg-interactive-active");
		expect(worded.querySelector("[data-link-words]")).toHaveTextContent("the share fix");
	});

	it("colours a chip by its PR's state once read", () => {
		seed(PR, { state: "merged" });
		render(<ChatMarkdown text={`See ${PR}`} />);
		const chip = screen.getByRole("link", { name: "#12, merged" });
		expect(chip.querySelector("svg")?.getAttribute("class")).toContain("text-status-merged");
	});

	it("keeps a draft's icon in the muted text colour", () => {
		seed(PR, { state: "draft" });
		render(<ChatMarkdown text={`See ${PR}`} />);
		const chip = screen.getByRole("link", { name: "#12, draft" });
		expect(chip.querySelector("svg")?.getAttribute("class")).toContain("text-muted-foreground");
	});

	it("shows the PR's card on hover", async () => {
		seed(PR, {
			state: "merged", checks: "passing", sourceBranch: "fix/share", targetBranch: "main",
			additions: 42, deletions: 8, changedFiles: 3, stateChangedAt: new Date(Date.now() - 2 * 86_400_000).toISOString(),
		});
		render(<ChatMarkdown text={`See ${PR}`} />);
		await userEvent.setup().hover(screen.getByRole("link", { name: "#12, merged" }));
		const card = await screen.findByTestId("pr-preview-card", undefined, { timeout: 3_000 });
		expect(within(card).getByText("acme/api")).toBeInTheDocument();
		expect(within(card).getByText("Fix the share menu")).toBeInTheDocument();
		expect(within(card).getByText("Merged")).toBeInTheDocument();
		expect(within(card).getByText("2 days ago")).toBeInTheDocument();
		expect(within(card).getByText("fix/share → main")).toBeInTheDocument();
		expect(within(card).getByText("Checks passing")).toBeInTheDocument();
		expect(within(card).getByText("+42")).toBeInTheDocument();
		expect(within(card).getByText(/3 files/)).toBeInTheDocument();
	});

	it("shows the page's own preview when the daemon cannot read the PR", async () => {
		// No GitHub provider: the daemon answers 501.
		getMock.mockResolvedValue({ data: undefined, error: { error: { code: "NOT_IMPLEMENTED", message: "Not implemented" } } });
		queryClient.setQueryData(linkPreviewQueryKey(PR), { url: PR, title: "Fix the share menu by mira" });
		render(<ChatMarkdown text={`See ${PR}`} />);
		await userEvent.setup().hover(screen.getByRole("link", { name: "#12" }));
		expect(await screen.findByTestId("link-preview-card", undefined, { timeout: 3_000 })).toHaveTextContent("Fix the share menu by mira");
		expect(screen.queryByTestId("pr-preview-card")).not.toBeInTheDocument();
		expect(getMock).toHaveBeenCalledWith("/api/v1/pr-preview", expect.objectContaining({ params: { query: { url: PR } } }));
	});
});
