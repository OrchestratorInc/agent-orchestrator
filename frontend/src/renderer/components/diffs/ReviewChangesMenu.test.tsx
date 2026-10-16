import { act, render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { TooltipProvider } from "../ui/tooltip";
import {
	ReviewChangesMenu,
	type ReviewChangesCommits,
	type ReviewChangesMenuProps,
	type ReviewCommitRow,
} from "./ReviewChangesMenu";

const shaOf = (index: number) => String(index).padStart(4, "0").padEnd(40, "c");

function commitRows(count: number): ReviewCommitRow[] {
	return Array.from({ length: count }, (_, index) => ({
		sha: shaOf(index),
		subject: `commit ${index}`,
		additions: index,
		deletions: 1,
		timestamp: new Date(Date.now() - (index + 1) * 3_600_000).toISOString(),
	}));
}

function renderMenu(overrides: Partial<ReviewChangesMenuProps> = {}, commitOverrides: Partial<ReviewChangesCommits> = {}) {
	const commits: ReviewChangesCommits = {
		state: "ready",
		rows: commitRows(3),
		truncated: false,
		allStats: { additions: 12, deletions: 4 },
		onToggleAll: vi.fn(),
		onToggle: vi.fn(),
		onRetry: vi.fn(),
		...commitOverrides,
	};
	const props: ReviewChangesMenuProps = {
		mode: "checked_out",
		value: { kind: "uncommitted" },
		stats: { additions: 3, deletions: 1 },
		scopes: [
			{ scope: "uncommitted", stats: { additions: 3, deletions: 1 } },
			{ scope: "staged", stats: { additions: 1, deletions: 0 } },
			{ scope: "unstaged", stats: { additions: 2, deletions: 1 } },
		],
		onSelectScope: vi.fn(),
		commits,
		...overrides,
	};
	const view = render(
		<TooltipProvider>
			<ReviewChangesMenu {...props} />
		</TooltipProvider>,
	);
	return { props, commits, view };
}

const openMenu = () => userEvent.click(screen.getByRole("button", { name: /^Changes to review:/ }));

describe("ReviewChangesMenu", () => {
	it("names the selection on the trigger with its stats", () => {
		renderMenu();
		const trigger = screen.getByRole("button", { name: "Changes to review: Uncommitted" });
		expect(trigger).toHaveAttribute("title", "Uncommitted");
		expect(trigger).toHaveTextContent("Uncommitted+3−1");
	});

	it("lists the working scopes with stats and a check slot, and closes on a pick", async () => {
		const { props } = renderMenu();
		await openMenu();
		const scopes = await screen.findAllByRole("menuitemradio");
		expect(scopes.map((item) => item.textContent)).toEqual(["Uncommitted+3−1", "Staged+1−0", "Unstaged+2−1"]);
		expect(scopes[0]).toHaveAttribute("aria-checked", "true");
		expect(scopes[1]).toHaveAttribute("aria-checked", "false");

		await userEvent.click(scopes[1]);
		expect(props.onSelectScope).toHaveBeenCalledWith("staged");
		expect(screen.queryByRole("menu")).not.toBeInTheDocument();
	});

	it("still reports a pick of the selected scope so the caller can switch views", async () => {
		const { props } = renderMenu();
		await openMenu();
		await userEvent.click(await screen.findByRole("menuitemradio", { name: /^Uncommitted/ }));
		expect(props.onSelectScope).toHaveBeenCalledWith("uncommitted");
	});

	it("puts commits in a submenu with All commits checked when commits are picked", async () => {
		renderMenu({ value: { kind: "commits", shas: [] }, stats: { additions: 12, deletions: 4 } });
		expect(screen.getByRole("button", { name: "Changes to review: All commits" })).toBeInTheDocument();
		await openMenu();
		expect(screen.getAllByRole("menuitemradio").every((item) => item.getAttribute("aria-checked") === "false")).toBe(true);
		await userEvent.click(await screen.findByRole("menuitem", { name: /^Commits/ }));
		const all = await screen.findByRole("menuitemcheckbox", { name: /^All commits/ });
		expect(all).toHaveAttribute("aria-checked", "true");
		expect(all).toHaveTextContent("All commits+12−4");
		expect(screen.getByRole("menuitemcheckbox", { name: /^commit 0/ })).toHaveAttribute("aria-checked", "false");
	});

	it("keeps the menu open when a commit checkbox is toggled", async () => {
		const { commits } = renderMenu({ mode: "view", value: { kind: "commits", shas: [] } });
		await openMenu();
		const row = await screen.findByRole("menuitemcheckbox", { name: /^commit 1/ });
		expect(row).toHaveTextContent("commit 1+1−1");

		await userEvent.click(row);
		expect(commits.onToggle).toHaveBeenCalledWith(shaOf(1));
		expect(screen.getByRole("menu")).toBeInTheDocument();

		await userEvent.click(screen.getByRole("menuitemcheckbox", { name: /^All commits/ }));
		expect(commits.onToggleAll).toHaveBeenCalledTimes(1);
		expect(screen.getByRole("menu")).toBeInTheDocument();
	});

	it("keeps commit rows plain: no icons or times, the box last, grey totals until ticked", async () => {
		const rows = commitRows(2);
		renderMenu({ mode: "view", value: { kind: "commits", shas: [rows[1].sha] } }, { allStats: { additions: 9, deletions: 3 } });
		await openMenu();
		const all = await screen.findByRole("menuitemcheckbox", { name: /^All commits/ });
		const unticked = screen.getByRole("menuitemcheckbox", { name: /^commit 0/ });
		const ticked = screen.getByRole("menuitemcheckbox", { name: /^commit 1/ });
		for (const row of [all, unticked, ticked]) {
			// Only the checkbox's own check mark is drawn; no row icon, no time.
			expect(row.querySelectorAll("svg").length).toBeLessThanOrEqual(1);
			expect(row.lastElementChild).toHaveClass("size-4", "rounded-sm");
		}
		expect(unticked).not.toHaveTextContent(/\d+[mhd]$/);
		expect(within(unticked).getByText("+0").parentElement).toHaveClass("text-passive");
		expect(within(ticked).getByText("+1")).toHaveClass("text-success");
		expect(within(all).getByText("+9").parentElement).toHaveClass("text-passive");
		// No divider between All commits and the commits.
		expect(screen.getByRole("menu").querySelector('[role="separator"]')).toBeNull();
	});

	it("checks ticked commits and names them on the trigger", async () => {
		const rows = commitRows(3);
		const single = renderMenu({ mode: "view", value: { kind: "commits", shas: [rows[1].sha] } });
		const trigger = screen.getByRole("button", { name: `Changes to review: ${rows[1].sha.slice(0, 7)} commit 1` });
		expect(within(trigger).getByText(rows[1].sha.slice(0, 7))).toHaveClass("font-mono");
		await openMenu();
		expect(await screen.findByRole("menuitemcheckbox", { name: /^commit 1/ })).toHaveAttribute("aria-checked", "true");
		expect(screen.getByRole("menuitemcheckbox", { name: /^All commits/ })).toHaveAttribute("aria-checked", "false");
		single.view.unmount();

		renderMenu({ mode: "view", value: { kind: "commits", shas: [rows[0].sha, rows[2].sha] } });
		expect(screen.getByRole("button", { name: "Changes to review: 2 commits" })).toBeInTheDocument();
	});

	it("shows commits 50 at a time and marks the end of a truncated history", async () => {
		renderMenu({ mode: "view", value: { kind: "commits", shas: [] } }, { rows: commitRows(120), truncated: true });
		await openMenu();
		const commitCount = () => screen.getAllByRole("menuitemcheckbox").length - 1;
		expect(commitCount()).toBe(50);
		expect(screen.queryByText("Older commits aren't listed")).not.toBeInTheDocument();

		await userEvent.click(screen.getByRole("menuitem", { name: "Show 50 more" }));
		expect(commitCount()).toBe(100);
		expect(screen.getByRole("menu")).toBeInTheDocument();

		await userEvent.click(screen.getByRole("menuitem", { name: "Show 20 more" }));
		expect(commitCount()).toBe(120);
		expect(screen.queryByRole("menuitem", { name: /^Show/ })).not.toBeInTheDocument();
		expect(screen.getByRole("menuitem", { name: "Older commits aren't listed" })).toHaveAttribute("aria-disabled", "true");
	});

	it("expands the list to a ticked commit beyond the first page", async () => {
		const rows = commitRows(120);
		renderMenu({ mode: "view", value: { kind: "commits", shas: [rows[75].sha] } }, { rows });
		await openMenu();
		expect(screen.getByRole("menuitemcheckbox", { name: /^commit 75/ })).toHaveAttribute("aria-checked", "true");
		expect(screen.getAllByRole("menuitemcheckbox")).toHaveLength(101);
		expect(screen.getByRole("menuitem", { name: "Show 20 more" })).toBeInTheDocument();
	});

	it("shows the full subject and SHA in a tooltip when a commit row takes keyboard focus", async () => {
		const rows = commitRows(2).map((row) => ({ ...row, subject: `${row.subject}: a long subject that the row truncates` }));
		renderMenu({ mode: "view", value: { kind: "commits", shas: [] } }, { rows });
		await openMenu();
		const row = await screen.findByRole("menuitemcheckbox", { name: /^commit 1:/ });
		act(() => row.focus());
		const tooltip = await screen.findByRole("tooltip");
		expect(tooltip).toHaveTextContent("commit 1: a long subject that the row truncates");
		expect(tooltip).toHaveTextContent(shaOf(1));

		// The open tooltip must not swallow Escape: one press closes the menu.
		await userEvent.keyboard("{Escape}");
		expect(screen.queryByRole("menu")).not.toBeInTheDocument();
	});

	it("opens the commit tooltip on keyboard focus even after pointer use", async () => {
		renderMenu({ mode: "view", value: { kind: "commits", shas: [] } });
		await openMenu();
		const all = await screen.findByRole("menuitemcheckbox", { name: /^All commits/ });
		await userEvent.hover(all);
		await userEvent.keyboard("{ArrowDown}");
		expect(screen.getByRole("menuitemcheckbox", { name: /^commit 0/ })).toHaveFocus();
		expect(await screen.findByRole("tooltip")).toHaveTextContent(shaOf(0));
	});

	it("waits for the hover delay before showing a commit tooltip under the pointer", async () => {
		renderMenu({ mode: "view", value: { kind: "commits", shas: [] } });
		await openMenu();
		const row = await screen.findByRole("menuitemcheckbox", { name: /^commit 1/ });
		const hoverTarget = row.querySelector<HTMLElement>("[data-state]:not([role])");
		expect(hoverTarget).not.toBeNull();
		await userEvent.hover(hoverTarget!);
		expect(row).toHaveFocus();
		expect(screen.queryByRole("tooltip")).not.toBeInTheDocument();
		expect(await screen.findByRole("tooltip")).toHaveTextContent(shaOf(1));
	});

	it("shows loading and error states, and retries without closing", async () => {
		const loading = renderMenu({ mode: "view", value: { kind: "commits", shas: [] } }, { state: "loading", rows: [] });
		await openMenu();
		expect(screen.getByRole("menuitem", { name: "Loading commits…" })).toHaveAttribute("aria-disabled", "true");
		loading.view.unmount();

		const failed = renderMenu({ mode: "view", value: { kind: "commits", shas: [] } }, { state: "error", rows: [] });
		await openMenu();
		expect(screen.getByRole("menuitem", { name: "Couldn't load commits" })).toHaveAttribute("aria-disabled", "true");
		await userEvent.click(screen.getByRole("menuitem", { name: "Retry" }));
		expect(failed.commits.onRetry).toHaveBeenCalledTimes(1);
		expect(screen.getByRole("menu")).toBeInTheDocument();
	});

	it("names the base when another branch has no commits of its own", async () => {
		renderMenu({ mode: "view", value: { kind: "commits", shas: [] } }, { rows: [], rangeMode: "since_base", baseRef: "main" });
		await openMenu();
		expect(screen.getByRole("menuitem", { name: "No commits that aren't on main" })).toBeInTheDocument();
	});

	it("labels a default branch's range as its latest commits", async () => {
		renderMenu({ mode: "view", value: { kind: "commits", shas: [] } }, { rangeMode: "recent" });
		expect(screen.getByRole("button", { name: "Changes to review: Latest 3 commits" })).toBeInTheDocument();
		await openMenu();
		expect(screen.getByRole("menuitemcheckbox", { name: /^Latest 3 commits/ })).toHaveAttribute("aria-checked", "true");
	});
});
