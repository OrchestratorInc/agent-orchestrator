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
		// Text only, like the menu's rows: the chevron is its one icon.
		expect(trigger.querySelectorAll("svg")).toHaveLength(1);
		expect(trigger.querySelector("svg")).toHaveClass("lucide-chevron-down");
	});

	it("leaves out scopes with no changes, but keeps the current pick and binary-only changes", async () => {
		const zero = { additions: 0, deletions: 0 };
		const scopes: ReviewChangesMenuProps["scopes"] = [
			{ scope: "uncommitted", stats: zero, empty: false },
			{ scope: "staged", stats: zero, empty: true },
			{ scope: "unstaged", stats: zero, empty: false },
		];
		const view = renderMenu({ value: { kind: "uncommitted" }, stats: zero, scopes });
		// No +0 −0 on the trigger or any row.
		expect(screen.getByRole("button", { name: "Changes to review: Uncommitted" })).toHaveTextContent(/^Uncommitted$/);
		await openMenu();
		// Staged has no files, so it goes; a binary-only change counts no lines but is still a change.
		expect(screen.getAllByRole("menuitemradio").map((item) => item.textContent)).toEqual(["Uncommitted", "Unstaged"]);
		await userEvent.keyboard("{Escape}");
		view.view.unmount();

		// The pick stays listed even when it has nothing, so the menu shows what the trigger names.
		renderMenu({ value: { kind: "staged" }, scopes });
		await openMenu();
		expect(screen.getByRole("menuitemradio", { name: /^Staged/ })).toHaveAttribute("aria-checked", "true");
	});

	it("drops the divider when no scope is left above Commits", async () => {
		renderMenu({ value: { kind: "commits", shas: [] }, scopes: [{ scope: "uncommitted", empty: true }, { scope: "staged", empty: true }, { scope: "unstaged", empty: true }] });
		await openMenu();
		expect(screen.queryByRole("menuitemradio")).not.toBeInTheDocument();
		expect(screen.getByRole("menuitem", { name: /^Commits/ })).toBeInTheDocument();
		expect(screen.getByRole("menu").querySelector('[role="separator"]')).toBeNull();
	});

	it("lists the working scopes with stats and a check slot, and closes on a pick", async () => {
		const { props } = renderMenu();
		await openMenu();
		const scopes = await screen.findAllByRole("menuitemradio");
		expect(scopes.map((item) => item.textContent)).toEqual(["Uncommitted+3−1", "Staged+1−0", "Unstaged+2−1"]);
		// Plain labels: only the selected row's check is drawn.
		expect(scopes[0].querySelectorAll("svg")).toHaveLength(1);
		expect(scopes[1].querySelector("svg")).toBeNull();
		expect(screen.getByRole("menuitem", { name: /^Commits/ }).querySelectorAll("svg")).toHaveLength(1);
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
		expect(screen.queryByRole("menuitemcheckbox")).not.toBeInTheDocument();
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

	it("shows commits 6 at a time in a 7-row scroll box that ends in Show more", async () => {
		renderMenu({ mode: "view", value: { kind: "commits", shas: [] } }, { rows: commitRows(20), truncated: true });
		await openMenu();
		const commitCount = () => screen.getAllByRole("menuitemcheckbox").length - 1;
		expect(commitCount()).toBe(6);
		expect(screen.queryByText("Older commits aren't listed")).not.toBeInTheDocument();
		const list = screen.getByRole("menuitemcheckbox", { name: /^commit 0/ }).parentElement;
		expect(list).toHaveClass("max-h-[12.625rem]", "overflow-y-auto");
		// Six rows and Show more fill the box; later pages scroll to it.
		const more = screen.getByRole("menuitem", { name: "Show 6 more" });
		expect(list?.lastElementChild).toBe(more);
		expect(more).toHaveClass("justify-center");

		await userEvent.click(more);
		expect(commitCount()).toBe(12);
		expect(screen.getByRole("menu")).toBeInTheDocument();
		await userEvent.click(screen.getByRole("menuitem", { name: "Show 6 more" }));
		expect(commitCount()).toBe(18);

		await userEvent.click(screen.getByRole("menuitem", { name: "Show 2 more" }));
		expect(commitCount()).toBe(20);
		expect(screen.queryByRole("menuitem", { name: /^Show/ })).not.toBeInTheDocument();
		const older = screen.getByRole("menuitem", { name: "Older commits aren't listed" });
		expect(older).toHaveAttribute("aria-disabled", "true");
		expect(list).toContainElement(older);
	});

	it("lists up to seven commits whole, with no Show more", async () => {
		renderMenu({ mode: "view", value: { kind: "commits", shas: [] } }, { rows: commitRows(7) });
		await openMenu();
		expect(screen.getAllByRole("menuitemcheckbox")).toHaveLength(8);
		expect(screen.queryByRole("menuitem", { name: /^Show/ })).not.toBeInTheDocument();
	});

	it("lines All commits' box up with the commits' boxes, scrollbar or not", async () => {
		renderMenu({ mode: "view", value: { kind: "commits", shas: [] } }, { rows: commitRows(3) });
		await openMenu();
		const list = screen.getByRole("menuitemcheckbox", { name: /^commit 0/ }).parentElement;
		// The list keeps its scrollbar gutter even when it doesn't scroll; All
		// commits, outside it, is inset by the same gutter and padding.
		expect(list).toHaveClass("[scrollbar-gutter:stable]", "pr-0.5");
		expect(screen.getByRole("menuitemcheckbox", { name: /^All commits/ })).toHaveClass("mr-1.5");
	});

	it("expands the list to a ticked commit beyond the first page", async () => {
		const rows = commitRows(30);
		renderMenu({ mode: "view", value: { kind: "commits", shas: [rows[15].sha] } }, { rows });
		await openMenu();
		expect(screen.getByRole("menuitemcheckbox", { name: /^commit 15/ })).toHaveAttribute("aria-checked", "true");
		// Pages of six up to the ticked commit: 18 commits and All commits.
		expect(screen.getAllByRole("menuitemcheckbox")).toHaveLength(19);
		expect(screen.getByRole("menuitem", { name: "Show 6 more" })).toBeInTheDocument();
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
		// There is nothing for All commits to select.
		expect(screen.queryByRole("menuitemcheckbox", { name: /^All commits/ })).not.toBeInTheDocument();
	});

	it("drops Commits from the menu when the branch has none, and keeps it while they load", async () => {
		const none = renderMenu({}, { rows: [], allStats: { additions: 0, deletions: 0 } });
		await openMenu();
		expect(screen.getAllByRole("menuitemradio")).toHaveLength(3);
		expect(screen.queryByRole("menuitem", { name: /^Commits/ })).not.toBeInTheDocument();
		expect(screen.getByRole("menu").querySelector('[role="separator"]')).toBeNull();
		none.view.unmount();

		renderMenu({}, { state: "loading", rows: [] });
		await openMenu();
		expect(screen.getByRole("menuitem", { name: /^Commits/ })).toBeInTheDocument();
	});

	it("labels a default branch's range as its latest commits", async () => {
		renderMenu({ mode: "view", value: { kind: "commits", shas: [] } }, { rangeMode: "recent" });
		expect(screen.getByRole("button", { name: "Changes to review: Latest 3 commits" })).toBeInTheDocument();
		await openMenu();
		expect(screen.getByRole("menuitemcheckbox", { name: /^Latest 3 commits/ })).toHaveAttribute("aria-checked", "true");
	});
});
