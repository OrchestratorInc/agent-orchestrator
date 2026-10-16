import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { toKanbanColumn } from "@aoagents/product-ui";
import {
	STANDALONE_PROJECT_KIND,
	STANDALONE_WORKSPACE_ID,
	type WorkspaceSession,
	type WorkspaceSummary,
} from "../types/workspace";

const {
	navigateMock,
	postMock,
	workspaceQueryMock,
	usageQueryMock,
	notificationShowMock,
} = vi.hoisted(() => ({
	navigateMock: vi.fn(),
	postMock: vi.fn(),
	workspaceQueryMock: vi.fn(),
	usageQueryMock: vi.fn(),
	notificationShowMock: vi.fn(),
}));

vi.mock("@tanstack/react-router", () => ({
	useNavigate: () => navigateMock,
}));

vi.mock("../hooks/useWorkspaceQuery", () => ({
	workspaceQueryKey: ["workspaces"],
	workspaceQueryKeyForHost: () => ["workspaces"],
	cloudSessionsQueryKey: ["cloud-sessions"],
	useWorkspaceQuery: workspaceQueryMock,
}));

vi.mock("../hooks/useSessionUsageSummaries", () => ({
	useSessionUsageSummaries: usageQueryMock,
}));

vi.mock("../lib/api-client", () => ({
	apiClient: {
		GET: vi.fn().mockResolvedValue({ data: { prs: [] } }),
		POST: (...args: unknown[]) => postMock(...args),
	},
	apiErrorMessage: (_error: unknown, fallback: string) => fallback,
}));

vi.mock("../lib/platform", async (importOriginal) => {
	const actual = await importOriginal<typeof import("../lib/platform")>();
	return {
		...actual,
		usesBoardActionsInPanel: () => true,
	};
});

vi.mock("../lib/bridge", () => ({
	aoBridge: {
		clipboard: { writeText: vi.fn() },
		notifications: {
			show: (...args: unknown[]) => notificationShowMock(...args),
		},
	},
}));

import { appI18n } from "../i18n";
import { aoBridge } from "../lib/bridge";
import { StandaloneArchiveView } from "./StandaloneArchiveView";
import { TooltipProvider } from "./ui/tooltip";

function renderArchive() {
	const queryClient = new QueryClient({
		defaultOptions: { queries: { retry: false } },
	});
	render(
		<QueryClientProvider client={queryClient}>
			<TooltipProvider>
				<StandaloneArchiveView />
			</TooltipProvider>
		</QueryClientProvider>,
	);
	return queryClient;
}

beforeEach(() => {
	navigateMock.mockReset();
	postMock.mockReset().mockResolvedValue({ data: {} });
	workspaceQueryMock
		.mockReset()
		.mockReturnValue({ data: [], isError: false, isSuccess: true });
	usageQueryMock.mockReset().mockReturnValue({ data: new Map() });
	notificationShowMock.mockReset().mockResolvedValue(undefined);
});

describe("StandaloneArchiveView", () => {
	it("shows archived standalone sessions as a list without kanban lanes", () => {
		workspaceQueryMock.mockReturnValue({
			data: [
				workspaceWithSessions([
					terminatedSession({ id: "project-dead", title: "project archived" }),
				]),
				standaloneWorkspaceWithSessions([
					standaloneSession({
						id: "standalone-live",
						title: "active ad hoc",
						status: "idle",
					}),
					terminatedSession({
						id: "standalone-dead",
						title: "archived ad hoc",
						workspaceId: STANDALONE_WORKSPACE_ID,
						workspaceName: "Scratchpad",
						branch: undefined,
					}),
				]),
			],
			isError: false,
			isSuccess: true,
		});

		renderArchive();

		expect(screen.getByTestId("standalone-archive-view")).toBeInTheDocument();
		expect(screen.getByTestId("standalone-archive-title")).toHaveTextContent("1 Archived Session");
		expect(screen.getByRole("button", { name: "New agent" }).closest(".workspace-topbar-actions")).not.toBeNull();
		expect(screen.queryByText("1 archived")).not.toBeInTheDocument();
		expect(screen.queryByText("Terminated")).not.toBeInTheDocument();
		const list = screen.getByRole("list", { name: "Archived sessions" });
		expect(list).toHaveClass("standalone-archive-grid");
		expect(within(list).getByText("archived ad hoc")).toBeInTheDocument();
		expect(screen.queryByText("active ad hoc")).not.toBeInTheDocument();
		expect(screen.queryByText("project archived")).not.toBeInTheDocument();
		expect(screen.queryByText("Building")).not.toBeInTheDocument();
		expect(screen.queryByText("Validating")).not.toBeInTheDocument();
	});

	it("restores a standalone archived session to the projectless session route", async () => {
		workspaceQueryMock.mockReturnValue({
			data: [
				standaloneWorkspaceWithSessions([
					terminatedSession({
						id: "standalone-dead",
						title: "archived ad hoc",
						workspaceId: STANDALONE_WORKSPACE_ID,
						workspaceName: "Scratchpad",
						branch: undefined,
					}),
				]),
			],
			isError: false,
			isSuccess: true,
		});

		renderArchive();
		await userEvent.click(
			screen.getByRole("button", { name: "Restore archived ad hoc" }),
		);

		await waitFor(() =>
			expect(postMock).toHaveBeenCalledWith(
				"/api/v1/sessions/{sessionId}/restore",
				{
					params: { path: { sessionId: "standalone-dead" } },
				},
			),
		);
		await waitFor(() => expect(navigateMock).toHaveBeenCalledWith({
			to: "/sessions/$sessionId",
			params: { sessionId: "standalone-dead" },
		}));
	});
});

describe("archived session card accessible labels", () => {
	const branchSession = () =>
		standaloneWorkspaceWithSessions([
			terminatedSession({
				id: "standalone-dead",
				title: "archived ad hoc",
				workspaceId: STANDALONE_WORKSPACE_ID,
				workspaceName: "Scratchpad",
				branch: "ao/bcd",
			}),
		]);

	afterEach(async () => {
		vi.useRealTimers();
		await act(() => appI18n.changeLanguage("en"));
	});

	it("names the restore and branch copy controls in English with title and branch interpolated", () => {
		workspaceQueryMock.mockReturnValue({ data: [branchSession()], isError: false, isSuccess: true });

		renderArchive();

		expect(screen.getByRole("button", { name: "Restore archived ad hoc" })).toBeInTheDocument();
		expect(screen.getByRole("button", { name: "Copy branch ao/bcd" })).toBeInTheDocument();
	});

	it("names the restore and branch copy controls in German while the headings are German", async () => {
		await act(() => appI18n.changeLanguage("de"));
		workspaceQueryMock.mockReturnValue({ data: [branchSession()], isError: false, isSuccess: true });

		renderArchive();

		expect(screen.getByRole("list", { name: "Archivierte Sitzungen" })).toBeInTheDocument();
		expect(screen.getByRole("button", { name: "archived ad hoc wiederherstellen" })).toBeInTheDocument();
		expect(screen.getByRole("button", { name: "Branch ao/bcd kopieren" })).toBeInTheDocument();
		expect(screen.queryByRole("button", { name: "Restore archived ad hoc" })).not.toBeInTheDocument();
		expect(screen.queryByRole("button", { name: "Copy branch ao/bcd" })).not.toBeInTheDocument();
	});

	it("updates the card's accessible labels when the language changes without remounting", async () => {
		workspaceQueryMock.mockReturnValue({ data: [branchSession()], isError: false, isSuccess: true });

		renderArchive();
		const restore = screen.getByRole("button", { name: "Restore archived ad hoc" });

		await act(() => appI18n.changeLanguage("de"));

		expect(screen.getByRole("button", { name: "archived ad hoc wiederherstellen" })).toBe(restore);
		expect(screen.getByRole("button", { name: "Branch ao/bcd kopieren" })).toBeInTheDocument();
	});

	it("announces the copied branch and reverts the label after the copied timeout", async () => {
		vi.useFakeTimers();
		workspaceQueryMock.mockReturnValue({ data: [branchSession()], isError: false, isSuccess: true });
		renderArchive();

		await act(async () => {
			screen.getByRole("button", { name: "Copy branch ao/bcd" }).click();
		});

		expect(aoBridge.clipboard.writeText).toHaveBeenCalledWith("ao/bcd");
		expect(screen.getByRole("button", { name: "Copied branch ao/bcd" })).toBeInTheDocument();
		act(() => vi.advanceTimersByTime(1_499));
		expect(screen.getByRole("button", { name: "Copied branch ao/bcd" })).toBeInTheDocument();
		act(() => vi.advanceTimersByTime(1));
		expect(screen.getByRole("button", { name: "Copy branch ao/bcd" })).toBeInTheDocument();
	});

	it("clears a pending copied timeout when the card unmounts", async () => {
		vi.useFakeTimers();
		workspaceQueryMock.mockReturnValue({ data: [branchSession()], isError: false, isSuccess: true });
		const { unmount } = render(
			<QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
				<TooltipProvider>
					<StandaloneArchiveView />
				</TooltipProvider>
			</QueryClientProvider>,
		);

		const setTimeoutSpy = vi.spyOn(globalThis, "setTimeout");
		const clearTimeoutSpy = vi.spyOn(globalThis, "clearTimeout");
		await act(async () => {
			screen.getByRole("button", { name: "Copy branch ao/bcd" }).click();
		});
		const copiedCall = setTimeoutSpy.mock.calls.findIndex(([, delay]) => delay === 1_500);
		const copiedTimer = setTimeoutSpy.mock.results[copiedCall]?.value;
		expect(copiedTimer).toBeDefined();
		unmount();

		expect(clearTimeoutSpy).toHaveBeenCalledWith(copiedTimer);
	});
});

function workspaceWithSessions(sessions: WorkspaceSession[]): WorkspaceSummary {
	return {
		id: "p1",
		name: "radic",
		kind: "single_repo",
		path: "/repo",
		sessions,
	};
}

function standaloneWorkspaceWithSessions(
	sessions: WorkspaceSession[],
): WorkspaceSummary {
	return {
		id: STANDALONE_WORKSPACE_ID,
		name: "Scratchpad",
		kind: STANDALONE_PROJECT_KIND,
		path: "Not attached to a project",
		sessions,
	};
}

function standaloneSession(
	overrides: Pick<WorkspaceSession, "id" | "title" | "status"> &
		Partial<WorkspaceSession>,
): WorkspaceSession {
	return {
		workspaceId: STANDALONE_WORKSPACE_ID,
		workspaceName: "Scratchpad",
		provider: "claude-code",
		branch: undefined,
		kanbanColumn: toKanbanColumn(undefined, overrides.status),
		updatedAt: "2026-01-01T00:00:00Z",
		prs: [],
		...overrides,
	};
}

function terminatedSession(
	overrides: Partial<WorkspaceSession> = {},
): WorkspaceSession {
	return {
		id: "s-dead",
		workspaceId: "p1",
		workspaceName: "radic",
		title: "dead worker",
		issueId: "github:INT-17",
		provider: "claude-code",
		kind: "worker",
		branch: "ao/dead-worker",
		status: "terminated",
		kanbanColumn: "archive",
		isTerminated: true,
		updatedAt: "2026-01-01T00:00:00Z",
		prs: [],
		...overrides,
	};
}
