import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { ReviewBranchMenu, type ReviewBranchMenuProps, type ReviewBranchRow } from "./ReviewBranchMenu";

const minutesAgo = (minutes: number) => new Date(Date.now() - minutes * 60_000).toISOString();

function branchRow(overrides: Partial<ReviewBranchRow> & Pick<ReviewBranchRow, "id" | "name">): ReviewBranchRow {
	return { kind: "local", checkedOut: false, selected: false, updatedAt: minutesAgo(120), ...overrides };
}

const baseRows: ReviewBranchRow[] = [
	branchRow({ id: "local:main", name: "main", checkedOut: true, selected: true, updatedAt: minutesAgo(5) }),
	branchRow({ id: "local:feature/files", name: "feature/files", prNumber: 12 }),
	branchRow({ id: "pr:42", kind: "pull_request", name: "fork/topic", prNumber: 42, updatedAt: minutesAgo(60 * 24) }),
];

function manyRows(count: number): ReviewBranchRow[] {
	return [
		...baseRows,
		...Array.from({ length: count - baseRows.length }, (_, index) => branchRow({ id: `local:chore/${index}`, name: `chore/${index}` })),
	];
}

function renderMenu(overrides: Partial<ReviewBranchMenuProps> = {}) {
	const props: ReviewBranchMenuProps = {
		trigger: { kind: "checked_out", name: "main" },
		state: "ready",
		rows: baseRows,
		truncated: false,
		onSelect: vi.fn(),
		onRetry: vi.fn(),
		onOpenChange: vi.fn(),
		...overrides,
	};
	const view = render(<ReviewBranchMenu {...props} />);
	return { props, view };
}

const openMenu = () => userEvent.click(screen.getByRole("button", { name: /^Branch to review:/ }));

describe("ReviewBranchMenu", () => {
	it("lists branches with the checked-out one first and their update times", async () => {
		const { props } = renderMenu();
		const trigger = screen.getByRole("button", { name: "Branch to review: main" });
		expect(trigger).toHaveAttribute("title", "main");
		// Text only, like the Changes trigger: the chevron is its one icon.
		expect(trigger.querySelectorAll("svg")).toHaveLength(1);
		expect(trigger.querySelector("svg")).toHaveClass("lucide-chevron-down");
		await openMenu();
		expect(props.onOpenChange).toHaveBeenCalledWith(true);
		const rows = screen.getAllByRole("menuitemradio");
		expect(rows.map((row) => row.textContent)).toEqual([
			"mainChecked out · Updated 5m ago",
			"feature/filesUpdated 2h ago",
			"fork/topicPull request #42 · Updated 1d ago",
		]);
		expect(rows[0]).toHaveAttribute("aria-checked", "true");
		expect(rows[1]).toHaveAttribute("aria-checked", "false");
		expect(screen.queryByRole("searchbox")).not.toBeInTheDocument();
	});

	it("reports a pick of another branch but not of the selected one", async () => {
		const { props } = renderMenu();
		await openMenu();
		await userEvent.click(screen.getByRole("menuitemradio", { name: /^main/ }));
		expect(props.onSelect).not.toHaveBeenCalled();

		await openMenu();
		await userEvent.click(screen.getByRole("menuitemradio", { name: /^fork\/topic/ }));
		expect(props.onSelect).toHaveBeenCalledWith("pr:42");
		expect(screen.queryByRole("menu")).not.toBeInTheDocument();
	});

	it("labels a pull request, a detached HEAD, and a checked-out branch still loading", () => {
		const pr = renderMenu({ trigger: { kind: "pull_request", number: 42, branch: "fork/topic" } });
		expect(screen.getByRole("button", { name: "Branch to review: #42 · fork/topic" })).toBeInTheDocument();
		pr.view.unmount();

		const detached = renderMenu({ trigger: { kind: "checked_out", detached: true } });
		expect(screen.getByRole("button", { name: "Branch to review: Detached HEAD" })).toBeInTheDocument();
		detached.view.unmount();

		renderMenu({ trigger: { kind: "checked_out" }, state: "loading", rows: [] });
		expect(screen.getByRole("button", { name: "Branch to review: Checked out" })).toBeInTheDocument();
	});

	it("names a detached HEAD row", async () => {
		renderMenu({
			trigger: { kind: "checked_out", detached: true },
			rows: [branchRow({ id: "detached", name: "", detached: true, checkedOut: true, selected: true, updatedAt: minutesAgo(5) }), ...baseRows.slice(1)],
		});
		await openMenu();
		expect(screen.getAllByRole("menuitemradio")[0]).toHaveTextContent("Detached HEADChecked out · Updated 5m ago");
	});

	it("adds search once the list outgrows its 7 rows, filters by name or PR number, and ArrowDown enters the list", async () => {
		const seven = renderMenu({ rows: manyRows(7) });
		await openMenu();
		// Seven fit the box: no search, no Show more.
		expect(screen.getAllByRole("menuitemradio")).toHaveLength(7);
		expect(screen.queryByRole("searchbox")).not.toBeInTheDocument();
		expect(screen.queryByRole("menuitem", { name: /^Show \d+ more$/ })).not.toBeInTheDocument();
		seven.view.unmount();

		renderMenu({ rows: manyRows(9) });
		await openMenu();
		const search = screen.getByRole("searchbox", { name: "Search branches" });
		await userEvent.type(search, "FEATURE");
		expect(screen.getAllByRole("menuitemradio").map((row) => row.textContent)).toEqual(["feature/filesUpdated 2h ago"]);

		await userEvent.clear(search);
		await userEvent.type(search, "#42");
		expect(screen.getAllByRole("menuitemradio")).toHaveLength(1);
		expect(screen.getByRole("menuitemradio")).toHaveTextContent("fork/topic");

		await userEvent.keyboard("{ArrowDown}");
		expect(screen.getByRole("menuitemradio")).toHaveFocus();

		await userEvent.clear(search);
		await userEvent.type(search, "nothing-like-this");
		expect(screen.queryByRole("menuitemradio")).not.toBeInTheDocument();
		expect(screen.getByRole("menuitem", { name: "No matching branches" })).toBeInTheDocument();
	});

	it("clears the search when the menu closes", async () => {
		renderMenu({ rows: manyRows(10) });
		await openMenu();
		await userEvent.type(screen.getByRole("searchbox"), "chore/1");
		expect(screen.getAllByRole("menuitemradio")).toHaveLength(1);
		await userEvent.keyboard("{Escape}");
		expect(screen.queryByRole("menu")).not.toBeInTheDocument();

		await openMenu();
		expect(screen.getByRole("searchbox")).toHaveValue("");
		expect(screen.getAllByRole("menuitemradio")).toHaveLength(6);
	});

	it("lists 6 branches at a time in a 7-row scroll box that ends in Show more, keeping the picked one in reach", async () => {
		renderMenu({ rows: manyRows(20) });
		await openMenu();
		expect(screen.getAllByRole("menuitemradio")).toHaveLength(6);
		const list = screen.getAllByRole("menuitemradio")[0].parentElement;
		expect(list).toHaveClass("max-h-[16.125rem]", "overflow-y-auto");
		// The same Show more as the commits list, at the end of the scroll.
		const more = screen.getByRole("menuitem", { name: "Show 6 more" });
		expect(list?.lastElementChild).toBe(more);
		expect(more).toHaveClass("justify-center");
		await userEvent.click(more);
		expect(screen.getAllByRole("menuitemradio")).toHaveLength(12);
		await userEvent.click(screen.getByRole("menuitem", { name: "Show 6 more" }));
		expect(screen.getAllByRole("menuitemradio")).toHaveLength(18);
		await userEvent.click(screen.getByRole("menuitem", { name: "Show 2 more" }));
		expect(screen.getAllByRole("menuitemradio")).toHaveLength(20);
		expect(screen.queryByRole("menuitem", { name: /^Show \d+ more$/ })).not.toBeInTheDocument();
		await userEvent.keyboard("{Escape}");

		const picked = manyRows(20).map((row, index) => ({ ...row, selected: index === 9 }));
		renderMenu({ rows: picked, trigger: { kind: "branch", name: picked[9].name } });
		await userEvent.click(screen.getByRole("button", { name: `Branch to review: ${picked[9].name}` }));
		expect(screen.getAllByRole("menuitemradio")).toHaveLength(12);
		expect(screen.getByRole("menuitemradio", { name: new RegExp(`^${picked[9].name}`) })).toHaveAttribute("aria-checked", "true");
	});

	it("shows loading, error with retry, and the cap footer", async () => {
		const loading = renderMenu({ state: "loading", rows: [] });
		await openMenu();
		expect(screen.getByRole("menuitem", { name: "Loading branches…" })).toHaveAttribute("aria-disabled", "true");
		loading.view.unmount();

		const failed = renderMenu({ state: "error", rows: [] });
		await openMenu();
		expect(screen.getByRole("menuitem", { name: "Couldn't load branches" })).toBeInTheDocument();
		await userEvent.click(screen.getByRole("menuitem", { name: "Retry" }));
		expect(failed.props.onRetry).toHaveBeenCalledTimes(1);
		expect(screen.getByRole("menu")).toBeInTheDocument();
		failed.view.unmount();

		renderMenu({ truncated: true });
		await openMenu();
		expect(screen.getByRole("menuitem", { name: "Showing the 3 most recent branches" })).toBeInTheDocument();
	});
});
