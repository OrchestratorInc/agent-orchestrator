import { fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { AgentAvatar } from "./AgentAvatar";
import { GithubAvatar } from "./GithubAvatar";
import { scmUserAvatarUrl } from "./scm-avatar";
import { UserAvatar } from "./UserAvatar";
import type { ExternalLinkProps } from "./external-link";
import { PRCardStatusSummary, PRSummaryMeta, PRSummaryParts } from "./PRSummaryDisplay";
import type { PRCardPresentation, PRSummaryPart } from "./pull-request-models";

function ExternalLink({ ariaLabel, children, stopPropagation, ...props }: ExternalLinkProps) {
	return (
		<a
			{...props}
			aria-label={ariaLabel}
			onClick={stopPropagation ? (event) => event.stopPropagation() : undefined}
		>
			{children}
		</a>
	);
}

describe("portable leaf components", () => {
	it("keeps user initials visible until the provider avatar loads and after errors", () => {
		const avatarUrl = "https://avatars.githubusercontent.com/u/123?v=4";
		const { container } = render(<UserAvatar imageUrl={avatarUrl} name="ada-lovelace" />);
		const image = container.querySelector("img");

		expect(container).toHaveTextContent("AL");
		expect(image).toHaveAttribute("src", avatarUrl);
		expect(image).toHaveClass("opacity-0");
		if (image) fireEvent.load(image);
		expect(image).toHaveClass("opacity-100");
		expect(container).not.toHaveTextContent("AL");
		if (image) fireEvent.error(image);
		expect(container.querySelector("img")).not.toBeInTheDocument();
		expect(container).toHaveTextContent("AL");
	});

	it("renders only initials when no provider avatar URL exists", () => {
		const { container } = render(<UserAvatar name="ada-lovelace" />);

		expect(container).toHaveTextContent("AL");
		expect(container.querySelector("img")).not.toBeInTheDocument();
	});

	it("derives provider avatar endpoints only when an observation has no URL", () => {
		expect(
			scmUserAvatarUrl("github", "https://github.com/acme/repo/pull/7", "ada-lovelace"),
		).toBe("https://avatars.githubusercontent.com/ada-lovelace?size=64");
		expect(
			scmUserAvatarUrl("github", "https://git.example.com/acme/repo/pull/7", "ada-lovelace"),
		).toBe("https://git.example.com/ada-lovelace.png?size=64");
		expect(scmUserAvatarUrl("unknown", "https://example.com/pull/7", "ada-lovelace")).toBeUndefined();
	});

	it("keeps the GitHub-specific compatibility component login-derived", () => {
		const { container } = render(<GithubAvatar login="ada-lovelace" />);

		expect(container.querySelector("img")).toHaveAttribute(
			"src",
			"https://avatars.githubusercontent.com/ada-lovelace?size=64",
		);
	});

	it("renders an injected agent logo without owning app assets", () => {
		render(<AgentAvatar logoSources={{ "claude-code": "/logos/claude.svg" }} provider="claude-code" />);

		expect(screen.getByRole("img", { name: "claude-code" })).toHaveAttribute(
			"src",
			"/logos/claude.svg",
		);
	});

	it("names a light/dark logo pair once and leaves both images decorative", () => {
		render(
			<AgentAvatar
				logoSources={{ "claude-code": { light: "/logos/claude-light.svg", dark: "/logos/claude-dark.svg" } }}
				provider="claude-code"
			/>,
		);

		const mark = screen.getByRole("img", { name: "claude-code" });
		const images = Array.from(mark.querySelectorAll("img"));
		expect(images.map((img) => img.getAttribute("src"))).toEqual(["/logos/claude-light.svg", "/logos/claude-dark.svg"]);
		expect(images.every((img) => img.getAttribute("alt") === "")).toBe(true);
	});

	it("renders fallback identity metadata for unknown agents", () => {
		render(<AgentAvatar provider="new-agent" />);

		expect(screen.getByRole("img", { name: "new-agent" })).toHaveTextContent("N");
	});

	it("renders neutral PR metadata with injected plural labels", () => {
		render(
			<PRSummaryMeta
				countNounLabel={(count, noun) => `${count} localized-${noun}`}
				externalLink={ExternalLink}
				pr={{
					provider: "github",
					author: "ada",
					authorAvatarUrl: "https://avatars.githubusercontent.com/u/123?v=4",
					sourceBranch: "feature",
					targetBranch: "main",
					changedFiles: 2,
					additions: 4,
					deletions: 1,
				}}
			/>,
		);

		expect(screen.getByText("feature → main")).toBeInTheDocument();
		expect(screen.getByText("2 localized-file")).toBeInTheDocument();
		expect(screen.getByRole("link", { name: "ada" })).toHaveAttribute("href", "https://github.com/ada");
		expect(screen.getByRole("link", { name: "ada" }).querySelector("img")).toHaveAttribute(
			"src",
			"https://avatars.githubusercontent.com/u/123?v=4",
		);
		expect(screen.getByText("feature → main")).toHaveClass("break-words");
	});

	it("puts the PR view card's branches, size and author on one line", () => {
		const { rerender } = render(
			<PRSummaryMeta
				countNounLabel={(count, noun) => `${count} ${noun}${count === 1 ? "" : "s"}`}
				externalLink={ExternalLink}
				inline
				pr={{ provider: "github", author: "Meraj-08", sourceBranch: "edit/cli", targetBranch: "edit/base", changedFiles: 3, additions: 89, deletions: 1 }}
			/>,
		);
		const line = screen.getByTestId("pr-meta");
		// Head, then base; files, the diff with a real minus sign, then the avatar.
		expect(line).toHaveTextContent(/^edit\/cli.*→.*edit\/base.*3 files\+89−1/);
		expect(line).not.toHaveClass("font-mono");
		// The author's name is the avatar's accessible name, not text on the line.
		expect(screen.getByRole("link", { name: "Meraj-08" })).toHaveAttribute("href", "https://github.com/Meraj-08");
		expect(line).not.toHaveTextContent("Meraj-08");
		expect(screen.getByText("89 added, 1 removed")).toHaveClass("sr-only");

		// A long head keeps its first and last segments; screen readers hear it whole.
		rerender(
			<PRSummaryMeta
				countNounLabel={(count, noun) => `${count} ${noun}${count === 1 ? "" : "s"}`}
				externalLink={ExternalLink}
				inline
				openedViaLabel="via Orchestrator"
				pr={{
					provider: "github",
					author: "ao-by-untrivial[bot]",
					openedBy: { name: "Ada Lovelace", githubLogin: "ada" },
					sourceBranch: "ao/taskr-edit-docs/document-taskr-edit",
					targetBranch: "edit/cli",
					changedFiles: 165,
					additions: 21018,
					deletions: 664,
				}}
			/>,
		);
		expect(screen.getByText("ao/…/document-taskr-edit")).toHaveAttribute("aria-hidden", "true");
		expect(screen.getByText("ao/taskr-edit-docs/document-taskr-edit")).toHaveClass("sr-only");
		expect(screen.getByText("+21,018")).toBeInTheDocument();
		expect(screen.getByText("−664")).toBeInTheDocument();
		// A PR AO's app opened names the person, and says so.
		expect(screen.getByRole("link", { name: "ada via Orchestrator" })).toHaveAttribute("href", "https://github.com/ada");
	});

	it("renders precomputed PR presentation without controller dependencies", () => {
		const presentation: PRCardPresentation = {
			primary: {
				key: "review",
				label: "Review required",
				tone: "review",
				links: [],
			},
			supporting: [
				{
					key: "ci",
					label: "Checks running",
					href: "https://example.com/checks",
					breathe: true,
					tone: "neutral",
					links: [],
				},
			],
		};
		const { container } = render(
			<PRCardStatusSummary externalLink={ExternalLink} presentation={presentation} />,
		);

		expect(screen.getByText("Review required")).toBeInTheDocument();
		expect(screen.getByRole("link", { name: "Checks running" })).toBeInTheDocument();
		expect(container.querySelector(".animate-status-pulse")).toBeInTheDocument();
	});

	it("keeps review details compact and inline with the review detail", () => {
		const reviewDetailsAction = <button type="button">View review details ↗</button>;
		render(
			<PRCardStatusSummary
				externalLink={ExternalLink}
				presentation={{
					primary: { key: "ci", label: "Checks passing", tone: "success", links: [] },
					supporting: [],
					statusRows: [
						{ key: "ci", label: "Checks passing", tone: "success", links: [] },
						{ key: "review", label: "Review status", detail: "Required review not submitted", tone: "review", links: [] },
					],
					readiness: { label: "Not mergeable yet", detail: "A required review is pending.", tone: "error" },
				}}
				reviewDetailsAction={reviewDetailsAction}
			/>,
		);

		const detail = screen.getByText("Required review not submitted");
		expect(detail.parentElement).toHaveClass("flex", "items-baseline");
		expect(detail.parentElement).toContainElement(screen.getByRole("button", { name: "View review details ↗" }));
		expect(detail.closest(".grid")).toHaveClass("grid-cols-1");
	});

	it("leads with what's in the way and opens everything else in one click", async () => {
		const presentation: PRCardPresentation = {
			primary: { key: "lifecycle", label: "Not mergeable yet", tone: "error", links: [] },
			supporting: [],
			statusRows: [{ key: "lifecycle", label: "Not mergeable yet", tone: "error", links: [] }],
			readinessRows: [
				{ key: "merge", label: "No conflicts", tone: "success", links: [] },
				{ key: "ci", label: "Checks failing", href: "https://example.com/checks", tone: "error", links: [] },
				{ key: "review", label: "No reviews yet", tone: "passive", links: [] },
			],
			readinessExplained: true,
			readiness: { label: "Not mergeable yet", detail: "Failing checks block this PR.", tone: "error" },
		};
		render(
			<PRCardStatusSummary
				action={<button type="button">Merge</button>}
				compactReadiness
				externalLink={ExternalLink}
				presentation={presentation}
				readinessDetails={{ ci: <p>format</p> }}
			/>,
		);

		const toggle = screen.getByRole("button", { name: "Checks failing" });
		expect(toggle).toHaveAttribute("aria-expanded", "false");
		expect(screen.getByRole("link", { name: "Checks failing" })).toHaveAttribute("href", "https://example.com/checks");
		expect(screen.queryByText("No conflicts")).not.toBeInTheDocument();
		expect(screen.queryByText("format")).not.toBeInTheDocument();
		expect(screen.getByRole("button", { name: "Merge" })).toBeInTheDocument();
		expect(screen.queryByText("No reviews yet")).not.toBeInTheDocument();
		expect(screen.queryByText("Not mergeable yet")).not.toBeInTheDocument();

		fireEvent.click(toggle);

		expect(toggle).toHaveAttribute("aria-expanded", "true");
		expect(await screen.findByText("format")).toBeInTheDocument();
		expect(screen.getByText("No conflicts")).toBeInTheDocument();
		expect(screen.queryByRole("button", { name: "No conflicts" })).not.toBeInTheDocument();
	});

	it("says the checks row as counts worst first, each with its own glyph", () => {
		render(
			<PRCardStatusSummary
				compactReadiness
				externalLink={ExternalLink}
				presentation={{
					primary: { key: "ci", label: "Checks failing", tone: "error", links: [] },
					supporting: [],
					readinessRows: [
						{ key: "merge", label: "No conflicts", tone: "success", links: [] },
						{
							key: "ci",
							label: "Checks failing",
							tone: "error",
							links: [],
							checks: {
								title: "Checks",
								counts: [
									{ state: "failed", count: 2, label: "2 failing" },
									{ state: "running", count: 1, label: "1 running" },
									{ state: "passed", count: 9, label: "9 passed" },
								],
							},
						},
					],
					readinessExplained: true,
					readiness: { label: "Can't merge yet", detail: "", tone: "error" },
				}}
			/>,
		);
		const tally = screen.getByTestId("pr-checks-tally");
		expect(tally).toHaveTextContent("Checks2 failing1 running9 passed");
		// The counts stand in for the verdict, so it isn't said twice.
		expect(screen.queryByText("Checks failing")).not.toBeInTheDocument();
		expect([...tally.querySelectorAll("[data-state]")].map((count) => count.getAttribute("data-state"))).toEqual(["failed", "running", "passed"]);
		// Each count has a shape, not only a colour; only the running one spins.
		expect(tally.querySelector('[data-state="failed"] svg')).toHaveClass("icon-check-failed");
		expect(tally.querySelector('[data-state="running"] svg')).toHaveClass("motion-safe:animate-spin");
		expect(tally.querySelector('[data-state="passed"] svg')).not.toHaveClass("motion-safe:animate-spin");
		// A row without a link of its own has no arrow beside it.
		expect(screen.queryByRole("link")).not.toBeInTheDocument();
	});

	it("still lists the failed checks when a conflict leads", async () => {
		render(
			<PRCardStatusSummary
				compactReadiness
				externalLink={ExternalLink}
				presentation={{
					primary: { key: "merge", label: "Merge conflict", tone: "error", links: [] },
					supporting: [],
					readinessRows: [
						{ key: "merge", label: "Merge conflict", tone: "error", links: [] },
						{ key: "ci", label: "Checks failing", tone: "error", links: [] },
					],
					readinessExplained: true,
					readiness: { label: "Not mergeable yet", detail: "Resolve the conflict.", tone: "error" },
				}}
				readinessDetails={{ ci: <p>format</p> }}
			/>,
		);

		fireEvent.click(screen.getByRole("button", { name: "Merge conflict" }));
		expect(await screen.findByText("Checks failing")).toBeInTheDocument();
		expect(screen.getByText("format")).toBeInTheDocument();
	});

	it("leads with the checks when nothing is in the way, and has no dropdown with nothing to open", () => {
		const presentation: PRCardPresentation = {
			primary: { key: "merge", label: "Ready to merge", tone: "success", links: [] },
			supporting: [],
			readinessRows: [
				{ key: "merge", label: "No conflicts", tone: "success", links: [] },
				{ key: "ci", label: "Checks passing", href: "https://example.com/checks", tone: "success", links: [] },
			],
			readinessExplained: true,
			readiness: { label: "Mergeable", detail: "Ready.", tone: "success" },
		};
		const { rerender } = render(<PRCardStatusSummary compactReadiness externalLink={ExternalLink} presentation={presentation} />);
		expect(screen.getByRole("button", { name: "Checks passing" })).toBeInTheDocument();

		rerender(<PRCardStatusSummary compactReadiness externalLink={ExternalLink} presentation={{ ...presentation, readinessRows: presentation.readinessRows?.slice(1) }} />);
		expect(screen.queryByRole("button")).not.toBeInTheDocument();
		expect(screen.getByRole("link", { name: /Checks passing/ })).toHaveAttribute("href", "https://example.com/checks");
	});

	it("leads with the verdict when no fact explains it, with why in the dropdown", async () => {
		render(
			<PRCardStatusSummary
				compactReadiness
				externalLink={ExternalLink}
				presentation={{
					primary: { key: "merge", label: "Merge blocked", tone: "warning", links: [] },
					supporting: [],
					readinessRows: [{ key: "merge", label: "No conflicts", tone: "success", links: [] }],
					readinessExplained: false,
					readiness: { label: "Not mergeable yet", detail: "GitHub reports this can't be merged.", tone: "error" },
				}}
			/>,
		);

		fireEvent.click(screen.getByRole("button", { name: "Not mergeable yet" }));
		expect(await screen.findByText("GitHub reports this can't be merged.")).toBeInTheDocument();
		expect(screen.getByText("No conflicts")).toBeInTheDocument();
	});

	it("computes overflow against the requested link limit", () => {
		const parts: PRSummaryPart[] = [
			{
				key: "ci",
				label: "CI",
				status: "Failing",
				links: [{ label: "unit" }, { label: "lint" }, { label: "types" }],
				linkTotal: 3,
				overflowNoun: "check",
				tone: "error",
			},
		];
		render(
			<PRSummaryParts
				externalLink={ExternalLink}
				interactiveLinks={false}
				maxLinks={2}
				parts={parts}
			/>,
		);

		expect(screen.getByText("unit")).toBeInTheDocument();
		expect(screen.getByText("lint")).toBeInTheDocument();
		expect(screen.queryByText("types")).not.toBeInTheDocument();
		expect(screen.getByText("+1 check")).toBeInTheDocument();
	});
});
