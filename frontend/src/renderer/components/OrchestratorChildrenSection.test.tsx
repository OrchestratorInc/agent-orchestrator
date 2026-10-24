import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, test, vi } from "vitest";
import type { OrchestratorChildView } from "../hooks/useOrchestratorChildren";
import type { WorkspaceSession } from "../types/workspace";
import { TooltipProvider } from "./ui/tooltip";

const { captureRendererEvent, navigate, childrenQuery } = vi.hoisted(() => ({
	captureRendererEvent: vi.fn(),
	navigate: vi.fn(),
	childrenQuery: {
		data: undefined as OrchestratorChildView[] | undefined,
		isLoading: false,
		isError: false,
	},
}));

vi.mock("../lib/telemetry", () => ({ captureRendererEvent }));
vi.mock("@tanstack/react-router", () => ({ useNavigate: () => navigate }));
vi.mock("../hooks/useOrchestratorChildren", async (importOriginal) => ({
	...(await importOriginal<object>()),
	useOrchestratorChildren: () => childrenQuery,
}));

import { OrchestratorChildrenSection } from "./OrchestratorChildrenSection";

const session = {
	id: "orch-1",
	workspaceId: "project-1",
	kind: "orchestrator",
	cloud: { orgId: "org-1" },
} as unknown as WorkspaceSession;

const childView = (overrides: Partial<OrchestratorChildView>): OrchestratorChildView => ({
	id: "child-1",
	title: "Fix CI",
	provider: "claude-code",
	status: "working",
	activity: { state: "active", lastActivityAt: "2026-08-30T12:00:00Z" },
	isTerminated: false,
	updatedAt: "2026-08-30T12:00:00Z",
	prs: [],
	...overrides,
});

// The app mounts its TooltipProvider at the router root; these subtree tests
// bring their own.
const section = () => (
	<TooltipProvider>
		<OrchestratorChildrenSection session={session} />
	</TooltipProvider>
);

describe("OrchestratorChildrenSection", () => {
	beforeEach(() => {
		captureRendererEvent.mockClear();
		navigate.mockClear();
		childrenQuery.data = undefined;
		childrenQuery.isLoading = false;
		childrenQuery.isError = false;
	});

	test("renders empty, loading, and error states", () => {
		childrenQuery.isLoading = true;
		const { rerender, unmount } = render(section());
		expect(screen.getByText("Loading workers…")).toBeInTheDocument();

		childrenQuery.isLoading = false;
		childrenQuery.data = [];
		rerender(section());
		expect(screen.getByText("No workers spawned yet.")).toBeInTheDocument();

		childrenQuery.data = undefined;
		childrenQuery.isError = true;
		rerender(section());
		expect(screen.getByRole("status")).toHaveTextContent("Workers are unavailable right now.");
		unmount();
	});

	test("names each PR's repository when a worker has PRs on several", () => {
		const pr = (url: string, number: number) => ({
			url, number, state: "open" as const, ci: "unknown", review: "none", mergeability: "unknown", reviewComments: false, updatedAt: "2026-10-16T12:00:00Z",
		});
		childrenQuery.data = [childView({ prs: [pr("https://github.com/acme/alice/pull/7", 7), pr("https://github.com/acme/11x/pull/7", 7)] })];
		render(section());
		expect(screen.getByRole("link", { name: "acme/alice PR #7" })).toHaveAttribute("href", "https://github.com/acme/alice/pull/7");
		expect(screen.getByRole("link", { name: "acme/11x PR #7" })).toHaveAttribute("href", "https://github.com/acme/11x/pull/7");
	});

	test("renders worker rows with status, PR chips, and terminated de-emphasis", () => {
		childrenQuery.data = [
			childView({
				prs: [
					{
						url: "https://github.com/o/r/pull/42",
						number: 42,
						state: "open",
						ci: "failing",
						review: "none",
						mergeability: "unstable",
						reviewComments: false,
						updatedAt: "2026-08-30T12:00:00Z",
					},
				],
			}),
			childView({ id: "child-2", title: "Old worker", status: "terminated", isTerminated: true }),
		];
		render(section());

		expect(screen.getByText("Workers (2)")).toBeInTheDocument();
		expect(screen.getByText("Fix CI")).toBeInTheDocument();
		const chip = screen.getByRole("link", { name: "PR #42" });
		expect(chip).toHaveAttribute("href", "https://github.com/o/r/pull/42");
		expect(chip).toHaveTextContent("CI failing");

		const rows = screen.getAllByTestId("orchestrator-child-row");
		expect(rows[1].className).toContain("opacity-60");
	});

	test("row click navigates to the worker and reports telemetry", async () => {
		childrenQuery.data = [childView({})];
		render(section());
		await userEvent.click(screen.getByText("Fix CI"));
		expect(navigate).toHaveBeenCalledWith({
			to: "/projects/$projectId/sessions/$sessionId",
			params: { projectId: "project-1", sessionId: "child-1" },
		});
		expect(captureRendererEvent).toHaveBeenCalledWith("ao.renderer.cloud_worker_opened", { has_pr: false });
	});

	test("hovering a worker's status dot explains the tone and the live activity", async () => {
		childrenQuery.data = [childView({
			status: "pr_open",
			activity: { state: "active", lastActivityAt: "2026-08-30T12:00:00Z" },
		})];
		render(section());
		const dot = screen.getByTestId("orchestrator-child-row").querySelector("span[data-session-status]")!;
		await userEvent.hover(dot.parentElement!);
		expect(await screen.findByRole("tooltip")).toHaveTextContent("In review · Working");
	});

	test("reports the workers-viewed event once with the count", () => {
		childrenQuery.data = [childView({}), childView({ id: "child-2" })];
		const { rerender } = render(section());
		rerender(section());
		const viewed = captureRendererEvent.mock.calls.filter((c) => c[0] === "ao.renderer.cloud_workers_viewed");
		expect(viewed).toEqual([["ao.renderer.cloud_workers_viewed", { worker_count: 2 }]]);
	});
});
