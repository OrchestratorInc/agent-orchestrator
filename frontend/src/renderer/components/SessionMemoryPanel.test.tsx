import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { toKanbanColumn } from "@aoagents/product-ui";
import type { WorkspaceSession, WorkspaceSummary } from "../types/workspace";
import { AppMemoryIndicator } from "./SessionMemoryPanel";
import { TooltipProvider } from "./ui/tooltip";

const { appMemoryMock, memoryQueryMock, postMock, workspaceQueryMock } = vi.hoisted(() => ({
	appMemoryMock: vi.fn(),
	memoryQueryMock: vi.fn(),
	postMock: vi.fn(),
	workspaceQueryMock: vi.fn(),
}));

vi.mock("../hooks/useSessionMemory", async (importOriginal) => ({
	...(await importOriginal<typeof import("../hooks/useSessionMemory")>()),
	useSessionMemory: memoryQueryMock,
	useAppMemory: appMemoryMock,
}));

vi.mock("../hooks/useWorkspaceQuery", () => ({
	workspaceQueryKey: ["workspaces"],
	useWorkspaceQuery: workspaceQueryMock,
}));

vi.mock("../lib/api-client", () => ({
	apiClient: { POST: (...args: unknown[]) => postMock(...args) },
	apiErrorMessage: (_error: unknown, fallback: string) => fallback,
}));

vi.mock("../lib/telemetry", () => ({ captureRendererEvent: vi.fn() }));

const GIB = 1024 ** 3;

function session(id: string, title: string, activityState = "idle"): WorkspaceSession {
	return {
		pausedAt: activityState === "exited" ? "2026-09-18T00:00:00Z" : undefined,
		id,
		title,
		workspaceId: "p1",
		workspaceName: "radic",
		provider: "claude-code",
		status: "idle",
		kanbanColumn: toKanbanColumn(undefined, "idle"),
		updatedAt: "2026-09-18T00:00:00Z",
		activity: { state: activityState as "idle", lastActivityAt: "2026-09-18T00:00:00Z" },
		prs: [],
	};
}

function reading(
	sessionId: string,
	rssBytes: number,
	processCount: number,
	processes: { pid: number; ppid: number; rssBytes: number; command: string }[] = [],
) {
	return { sessionId, rssBytes, processCount, sampledAt: "2026-09-18T00:00:00Z", processes };
}

function renderButton() {
	const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
	const tree = () => (
		<QueryClientProvider client={queryClient}>
			<TooltipProvider>
				<AppMemoryIndicator />
			</TooltipProvider>
		</QueryClientProvider>
	);
	const result = render(tree());
	return { ...result, rerender: () => result.rerender(tree()) };
}

beforeEach(() => {
	postMock.mockReset().mockResolvedValue({ data: {} });
	const workspace: WorkspaceSummary = {
		id: "p1",
		name: "radic",
		sessions: [session("s-small", "small worker"), session("s-big", "big worker"), session("s-none", "unsampled worker")],
	} as WorkspaceSummary;
	workspaceQueryMock.mockReset().mockReturnValue({ data: [workspace], isError: false, isSuccess: true });
	appMemoryMock.mockReset().mockReturnValue({
		isError: false,
		data: { app: { rssBytes: 4 * GIB, processCount: 20 }, system: { totalBytes: 32 * GIB, availableBytes: 20 * GIB } },
	});
	memoryQueryMock.mockReset().mockReturnValue({
		isError: false,
		data: new Map([
			["s-small", reading("s-small", 641_728_512, 5)],
			[
				"s-big",
				reading("s-big", 2_254_857_830, 9, [
					{ pid: 111, ppid: 1, rssBytes: 1_800_000_000, command: "claude" },
					{ pid: 222, ppid: 111, rssBytes: 454_857_830, command: "go test" },
				]),
			],
		]),
	});
});

describe("AppMemoryIndicator", () => {
	it("hides itself when nothing was sampled or the daemon cannot measure", () => {
		appMemoryMock.mockReturnValue({ isError: true, data: undefined });
		renderButton();
		expect(screen.queryByTestId("app-memory-indicator")).not.toBeInTheDocument();
	});

	it("colours the percent by AO's share and by host headroom", () => {
		appMemoryMock.mockReturnValue({
			isError: false,
			data: { app: { rssBytes: 2 * GIB, processCount: 20 }, system: { totalBytes: 32 * GIB, availableBytes: 20 * GIB } },
		});
		const { rerender } = renderButton();
		const button = screen.getByTestId("app-memory-indicator");
		expect(button).toHaveTextContent("2.0 GB");
		expect(button).toHaveAttribute("data-memory-tone", "default");
		expect(button).toHaveAttribute("aria-label", "AO is using 2.0 GB of 32.0 GB (6%) · 20.0 GB free");

		appMemoryMock.mockReturnValue({
			isError: false,
			data: { app: { rssBytes: 4 * GIB, processCount: 20 }, system: { totalBytes: 32 * GIB, availableBytes: 20 * GIB } },
		});
		rerender();
		expect(screen.getByTestId("app-memory-indicator")).toHaveAttribute("data-memory-tone", "warning");

		// Same small share, but the host is almost out of memory.
		appMemoryMock.mockReturnValue({
			isError: false,
			data: { app: { rssBytes: 2 * GIB, processCount: 20 }, system: { totalBytes: 32 * GIB, availableBytes: 1 * GIB } },
		});
		rerender();
		expect(screen.getByTestId("app-memory-indicator")).toHaveAttribute("data-memory-tone", "critical");
	});

	it("falls back to the absolute size where host RAM is unreadable", () => {
		appMemoryMock.mockReturnValue({ isError: false, data: { app: { rssBytes: 4 * GIB, processCount: 20 } } });
		renderButton();
		const button = screen.getByTestId("app-memory-indicator");
		expect(button).toHaveTextContent("4.0 GB");
		expect(button).toHaveAttribute("data-memory-tone", "unknown");
	});

	it("opens a panel sorted largest first with a working kill", async () => {
		renderButton();
		const button = screen.getByTestId("app-memory-indicator");

		await userEvent.click(button);
		const table = await screen.findByTestId("session-memory-table");
		const rows = within(table).getAllByTestId("session-memory-row");
		expect(rows.map((row) => row.textContent)).toEqual([
			expect.stringContaining("big worker"),
			expect.stringContaining("small worker"),
			expect.stringContaining("unsampled worker"),
		]);
		expect(within(rows[0]).getByText("2.1 GB")).toBeInTheDocument();
		expect(within(rows[2]).getByText("—", { selector: "td span" })).toBeInTheDocument();

		await userEvent.click(within(rows[0]).getByRole("button", { name: "Terminate big worker" }));
		const confirm = await screen.findByRole("dialog", { name: "Terminate big worker?" });
		await userEvent.click(within(confirm).getByRole("button", { name: "Yes, terminate session" }));
		await waitFor(() =>
			expect(postMock).toHaveBeenCalledWith("/api/v1/sessions/{sessionId}/kill", {
				params: { path: { sessionId: "s-big" } },
			}),
		);
	});

	it("expands a row to show what is occupying its memory, and collapses on a second click", async () => {
		renderButton();
		await userEvent.click(screen.getByTestId("app-memory-indicator"));
		const table = await screen.findByTestId("session-memory-table");
		const bigRow = within(table).getAllByTestId("session-memory-row")[0];

		expect(screen.queryByTestId("session-memory-process-row")).not.toBeInTheDocument();
		await userEvent.click(bigRow);
		const detail = await screen.findByTestId("session-memory-process-row");
		expect(within(detail).getByText("claude")).toBeInTheDocument();
		expect(within(detail).getByText("1.7 GB")).toBeInTheDocument();
		expect(within(detail).getByText("111")).toBeInTheDocument();
		expect(within(detail).getByText("go test")).toBeInTheDocument();

		await userEvent.click(bigRow);
		expect(screen.queryByTestId("session-memory-process-row")).not.toBeInTheDocument();
	});

	it("does not expand a row with no per-process reading, and kill does not toggle the row", async () => {
		renderButton();
		await userEvent.click(screen.getByTestId("app-memory-indicator"));
		const table = await screen.findByTestId("session-memory-table");
		const rows = within(table).getAllByTestId("session-memory-row");
		const unsampledRow = rows[2];

		await userEvent.click(unsampledRow);
		expect(screen.queryByTestId("session-memory-process-row")).not.toBeInTheDocument();

		const bigRow = rows[0];
		await userEvent.click(within(bigRow).getByRole("button", { name: "Terminate big worker" }));
		expect(screen.queryByTestId("session-memory-process-row")).not.toBeInTheDocument();
	});

	it("runs the existing cleanup route across every project", async () => {
		renderButton();
		await userEvent.click(screen.getByTestId("app-memory-indicator"));
		await userEvent.click(await screen.findByRole("button", { name: "Delete terminated workspaces" }));
		await waitFor(() =>
			expect(postMock).toHaveBeenCalledWith("/api/v1/sessions/cleanup", { params: { query: {} } }),
		);
	});

	it("pauses a running agent and resumes an exited one from its row", async () => {
		const workspace: WorkspaceSummary = {
			id: "p1",
			name: "radic",
			sessions: [session("s-big", "big worker"), session("s-small", "small worker", "exited")],
		} as WorkspaceSummary;
		workspaceQueryMock.mockReturnValue({ data: [workspace], isError: false, isSuccess: true });
		renderButton();
		await userEvent.click(screen.getByTestId("app-memory-indicator"));
		const table = await screen.findByTestId("session-memory-table");

		await userEvent.click(within(table).getByRole("button", { name: "Pause agent for big worker" }));
		await waitFor(() =>
			expect(postMock).toHaveBeenCalledWith("/api/v1/sessions/{sessionId}/exit-agent", {
				params: { path: { sessionId: "s-big" } },
			}),
		);
		await userEvent.click(within(table).getByRole("button", { name: "Resume agent for small worker" }));
		await waitFor(() =>
			expect(postMock).toHaveBeenCalledWith("/api/v1/sessions/{sessionId}/resume-agent", {
				params: { path: { sessionId: "s-small" } },
			}),
		);
	});
});
