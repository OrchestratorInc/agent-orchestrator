import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { useUiStore } from "../../stores/ui-store";
import { TooltipProvider } from "../ui/tooltip";
import { UsageView } from "./UsageView";

vi.mock("../NotificationCenter", () => ({ NotificationCenter: () => null }));

const mocks = vi.hoisted(() => ({
	report: vi.fn(),
	reportRefetch: vi.fn(),
	workspaces: [] as Array<{ id: string; name: string; kind?: string }>,
}));

vi.mock("../../hooks/useWorkspaceQuery", () => ({ useWorkspaceQuery: () => ({ data: mocks.workspaces }) }));
vi.mock("../../hooks/useUsageReport", () => ({
	useUsageReport: (days: number, offset: number, projectId?: string) => {
		mocks.report(days, offset, projectId);
		return { data: undefined, isLoading: false, isFetching: false, error: null, isPlaceholderData: false, refetch: mocks.reportRefetch };
	},
	useUsageLimitWindows: () => ({ data: undefined, isLoading: false, isFetching: false, error: null, refetch: vi.fn() }),
}));
vi.mock("../../hooks/useAgentAccounts", () => ({
	useAgentAccounts: () => ({ data: [], isLoading: false, isFetching: false, error: null }),
	useAgentAccountActions: () => ({ refresh: vi.fn() }),
}));
vi.mock("./UsageLimits", () => ({ UsageLimits: () => <p>Limits content</p> }));
vi.mock("./UsageWindows", () => ({ UsageWindows: () => <p>Windows content</p> }));

function renderView() {
	const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
	return render(<QueryClientProvider client={client}><TooltipProvider><UsageView /></TooltipProvider></QueryClientProvider>);
}

describe("UsageView", () => {
	beforeEach(() => {
		mocks.report.mockReset();
		mocks.reportRefetch.mockReset();
		mocks.workspaces = [{ id: "demo", name: "Demo" }, { id: "web", name: "Web app" }];
		useUiStore.setState({ developerMode: false });
	});

	it("keeps only the title in the topbar and switches views from pills", async () => {
		const user = userEvent.setup();
		renderView();
		const title = screen.getByRole("heading", { name: "Usage", level: 1 });
		expect(title.closest("header")?.querySelectorAll("button")).toHaveLength(0);
		const views = screen.getByRole("group", { name: "Usage view" });
		expect(within(views).getAllByRole("button").map((button) => button.textContent)).toEqual(["Spend", "Limits"]);
		expect(within(views).getByRole("button", { name: "Spend" })).toHaveAttribute("aria-pressed", "true");
		expect(screen.getByRole("button", { name: "Filter" })).toBeInTheDocument();
		await user.click(within(views).getByRole("button", { name: "Limits" }));
		expect(within(views).getByRole("button", { name: "Limits" })).toHaveAttribute("aria-pressed", "true");
		expect(screen.getByText("Limits content")).toBeInTheDocument();
		// Project and period only apply to spend; refresh stays for every view.
		expect(screen.queryByRole("button", { name: "Filter" })).not.toBeInTheDocument();
		expect(screen.queryByRole("button", { name: "Previous period" })).not.toBeInTheDocument();
		expect(screen.getByRole("button", { name: "Refresh" })).toBeInTheDocument();
	});

	it("offers the experimental windows view only in developer mode", () => {
		useUiStore.setState({ developerMode: true });
		renderView();
		const views = screen.getByRole("group", { name: "Usage view" });
		expect(within(views).getByRole("button", { name: /Windows/ })).toHaveTextContent("Experimental");
	});

	it("filters spend by project from the Filter menu and clears it from the chip", async () => {
		const user = userEvent.setup();
		renderView();
		expect(mocks.report).toHaveBeenLastCalledWith(7, 0, undefined);
		await user.click(screen.getByRole("button", { name: "Filter" }));
		await user.click(await screen.findByRole("menuitemradio", { name: "Web app" }));
		expect(mocks.report).toHaveBeenLastCalledWith(7, 0, "web");
		await user.click(screen.getByRole("button", { name: "Remove filter Project: Web app" }));
		expect(mocks.report).toHaveBeenLastCalledWith(7, 0, undefined);
		expect(screen.queryByRole("button", { name: /Remove filter/ })).not.toBeInTheDocument();
	});

	it("steps the period back and forward from the toolbar", async () => {
		const user = userEvent.setup();
		renderView();
		const next = screen.getByRole("button", { name: "Next period" });
		expect(next).toBeDisabled();
		await user.click(screen.getByRole("button", { name: "Previous period" }));
		expect(mocks.report).toHaveBeenLastCalledWith(7, 1, undefined);
		await user.click(next);
		expect(mocks.report).toHaveBeenLastCalledWith(7, 0, undefined);
	});

	it("hides the project filter when there is no project to choose", () => {
		mocks.workspaces = [];
		renderView();
		expect(screen.queryByRole("button", { name: "Filter" })).not.toBeInTheDocument();
	});
});
