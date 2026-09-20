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
	apiErrorCode: (error: unknown) => (error as { code?: string } | null)?.code,
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
	processes: { pid: number; ppid: number; rssBytes: number; cpuPercent: number; command: string }[] = [],
	cpuPercent = 0,
) {
	return { sessionId, rssBytes, processCount, cpuPercent, sampledAt: "2026-09-18T00:00:00Z", processes };
}

/** A 32 GB host with the given amount free; swap idle, cores idle unless said otherwise. */
function host(availableGiB: number, extra: Partial<{ swapBytesPerSec: number; load1: number }> = {}) {
	return {
		totalBytes: 32 * GIB,
		availableBytes: availableGiB * GIB,
		swapTotalBytes: 8 * GIB,
		swapUsedBytes: 0,
		swapBytesPerSec: 0,
		cpuCount: 8,
		load1: 0.5,
		...extra,
	};
}

function appReading(availableGiB: number, extra: Partial<{ swapBytesPerSec: number; load1: number }> = {}) {
	return {
		isError: false,
		data: {
			app: {
				rssBytes: 2 * GIB,
				processCount: 20,
				cpuPercent: 12,
				own: reading("ao", 300 * 1024 ** 2, 3, [{ pid: 7, ppid: 1, rssBytes: 300 * 1024 ** 2, cpuPercent: 1, command: "ao daemon" }]),
			},
			system: host(availableGiB, extra),
			reserve: { bytes: 2 * GIB, auto: true },
			liveCount: 2,
		},
	};
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
	appMemoryMock.mockReset().mockReturnValue(appReading(20));
	memoryQueryMock.mockReset().mockReturnValue({
		isError: false,
		data: new Map([
			["s-small", reading("s-small", 641_728_512, 5)],
			[
				"s-big",
				reading("s-big", 2_254_857_830, 9, [
					{ pid: 111, ppid: 1, rssBytes: 1_800_000_000, cpuPercent: 80.4, command: "claude" },
					{ pid: 222, ppid: 111, rssBytes: 454_857_830, cpuPercent: 1.6, command: "go test" },
				], 82),
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

	it("shows free RAM and a count, coloured by headroom, with the rest in the tooltip", () => {
		const { rerender } = renderButton();
		const button = screen.getByTestId("app-memory-indicator");
		expect(button).toHaveTextContent("20.0 GB free· 2 sessions");
		expect(button).toHaveAttribute("data-memory-tone", "default");
		expect(button).toHaveAttribute("aria-label", "20.0 GB free of 32.0 GB (63%) · AO holds 2.0 GB · load 0.06 per core");

		// A fifth free: getting tight.
		appMemoryMock.mockReturnValue(appReading(6));
		rerender();
		expect(screen.getByTestId("app-memory-indicator")).toHaveAttribute("data-memory-tone", "warning");
		expect(screen.getByTestId("app-memory-indicator")).toHaveTextContent("6.0 GB free· 2 sessions");

		// Under a tenth free and under the reserve: red, and the reserve line appears.
		appMemoryMock.mockReturnValue(appReading(1));
		rerender();
		expect(screen.getByTestId("app-memory-indicator")).toHaveAttribute("data-memory-tone", "critical");
		expect(screen.getByTestId("app-memory-indicator")).toHaveTextContent("1.0 GB free· below 2.0 GB reserve· 2 sessions");
	});

	it("adds the swap rate only while swapping and the load only while the cores are pinned", () => {
		appMemoryMock.mockReturnValue(appReading(20, { swapBytesPerSec: 8 * 1024 ** 2 }));
		const { rerender } = renderButton();
		expect(screen.getByTestId("app-memory-indicator")).toHaveAttribute("data-memory-reason", "swap");
		expect(screen.getByTestId("app-memory-indicator")).toHaveTextContent("20.0 GB free· swap 8 MB/s· 2 sessions");

		appMemoryMock.mockReturnValue(appReading(20, { load1: 12 }));
		rerender();
		expect(screen.getByTestId("app-memory-indicator")).toHaveAttribute("data-memory-tone", "warning");
		expect(screen.getByTestId("app-memory-indicator")).toHaveTextContent("20.0 GB free· load 1.5· 2 sessions");
	});

	it("offers to pause idle sessions only while red", () => {
		const { rerender } = renderButton();
		expect(screen.queryByTestId("memory-pressure-suggestion")).not.toBeInTheDocument();
		appMemoryMock.mockReturnValue(appReading(1));
		rerender();
		expect(screen.getByTestId("memory-pressure-suggestion")).toHaveTextContent("Pause 3 idle · 2.7 GB");
	});

	it("goes grey and falls back to AO's own size where the host cannot be read", () => {
		appMemoryMock.mockReturnValue({ isError: false, data: { app: { rssBytes: 4 * GIB, processCount: 20, cpuPercent: 0 }, liveCount: 1 } });
		renderButton();
		const button = screen.getByTestId("app-memory-indicator");
		expect(button).toHaveTextContent("4.0 GB· 1 session");
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
		expect(within(rows[0]).getByText("82%")).toBeInTheDocument();
		expect(within(rows[2]).getByText("—", { selector: "td span" })).toBeInTheDocument();
		// AO's own processes are pinned last so the totals add up, with no pause or kill.
		const own = within(table).getByTestId("session-memory-own-row");
		expect(own).toHaveTextContent("AO itself (daemon and app)");
		expect(own).toHaveTextContent("300 MB");
		expect(within(own).queryByRole("button")).not.toBeInTheDocument();

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
		expect(within(detail).getByText("80%")).toBeInTheDocument();
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

	it("points at the biggest session only under pressure", async () => {
		renderButton();
		await userEvent.click(screen.getByTestId("app-memory-indicator"));
		await screen.findByTestId("session-memory-table");
		expect(screen.queryByTestId("session-memory-hint")).not.toBeInTheDocument();
		expect(screen.getAllByTestId("session-memory-row")[0]).not.toHaveAttribute("data-highlighted");
	});

	it("highlights the biggest session when the host is under pressure", async () => {
		appMemoryMock.mockReturnValue(appReading(6));
		renderButton();
		await userEvent.click(screen.getByTestId("app-memory-indicator"));
		await screen.findByTestId("session-memory-table");
		expect(screen.getByTestId("session-memory-hint")).toHaveTextContent("Biggest: big worker · 2.1 GB");
		expect(screen.getAllByTestId("session-memory-row")[0]).toHaveAttribute("data-highlighted", "true");
	});

	it("pauses every idle session in one click and reports what it freed", async () => {
		postMock.mockImplementation(async (path: string) =>
			path === "/api/v1/sessions/pause-idle" ? { data: { ok: true, paused: ["s-small", "s-big"], failed: [] } } : { data: {} },
		);
		renderButton();
		await userEvent.click(screen.getByTestId("app-memory-indicator"));
		await screen.findByTestId("session-memory-table");
		await userEvent.click(screen.getByTestId("session-memory-pause-idle"));
		await waitFor(() =>
			expect(postMock).toHaveBeenCalledWith("/api/v1/sessions/pause-idle", { params: { query: {} } }),
		);
		expect(await screen.findByRole("status")).toHaveTextContent("Paused 2 · freed 2.7 GB");
	});

	it("disables bulk pause when nothing is idle", async () => {
		const workspace: WorkspaceSummary = {
			id: "p1",
			name: "radic",
			sessions: [session("s-big", "big worker", "active"), session("s-small", "small worker", "exited")],
		} as WorkspaceSummary;
		workspaceQueryMock.mockReturnValue({ data: [workspace], isError: false, isSuccess: true });
		renderButton();
		await userEvent.click(screen.getByTestId("app-memory-indicator"));
		await screen.findByTestId("session-memory-table");
		expect(screen.getByTestId("session-memory-pause-idle")).toBeDisabled();
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
				body: { policy: "drain" },
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
