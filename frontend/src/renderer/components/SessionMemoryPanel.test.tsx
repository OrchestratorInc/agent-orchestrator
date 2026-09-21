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

const LONG_AGO = new Date(Date.now() - 2 * 60 * 60 * 1000).toISOString();

function session(id: string, title: string, activityState = "idle", lastActivityAt = LONG_AGO): WorkspaceSession {
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
		activity: { state: activityState as "idle", lastActivityAt },
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

/** A 32 GB host with the given amount free and PSI stall percentage. */
function host(availableGiB: number, pressureRaw = 0) {
	return {
		totalBytes: 32 * GIB,
		availableBytes: availableGiB * GIB,
		swapTotalBytes: 8 * GIB,
		swapUsedBytes: 0,
		swapBytesPerSec: 0,
		cpuCount: 8,
		load1: 0.5,
		pressureRaw,
		pressureSource: "psi",
	};
}

/** AO holding `aoGiB` on a host with `availableGiB` free at the given pressure. */
function appReading(availableGiB: number, pressureRaw = 0, aoGiB = 2) {
	return {
		isError: false,
		data: {
			app: {
				rssBytes: aoGiB * GIB,
				processCount: 20,
				cpuPercent: 12,
				own: reading("ao", 300 * 1024 ** 2, 3, [{ pid: 7, ppid: 1, rssBytes: 300 * 1024 ** 2, cpuPercent: 1, command: "ao daemon" }]),
			},
			system: host(availableGiB, pressureRaw),
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

	it("reads a dot and AO's size, plus the fix; grey means nothing to do", () => {
		const { rerender } = renderButton();
		const button = screen.getByTestId("app-memory-indicator");
		expect(button).toHaveTextContent("2.1 GB");
		expect(button).not.toHaveTextContent("Fine");
		expect(button).toHaveAttribute("data-memory-state", "fine");
		expect(button).toHaveAttribute("aria-label", "Fine · 21.5 GB free of 34.4 GB · AO holds 2.1 GB · pressure 0.0");

		// Stalling on memory, AO holds most of what is in use, two sessions idle for hours: stop them.
		appMemoryMock.mockReturnValue(appReading(3, 12, 20));
		rerender();
		expect(screen.getByTestId("app-memory-indicator")).toHaveAttribute("data-memory-state", "tight_soon");
		expect(screen.getByTestId("app-memory-indicator")).toHaveTextContent("21.5 GB· Stop 3 idle · frees 2.9 GB");

		// Tight, but AO is a sliver of what is in use: say so, offer nothing.
		appMemoryMock.mockReturnValue(appReading(1, 40, 2));
		rerender();
		expect(screen.getByTestId("app-memory-indicator")).toHaveAttribute("data-memory-state", "tight");
		expect(screen.getByTestId("app-memory-indicator")).toHaveTextContent("2.1 GB· not AO");
	});

	it("points at the biggest session when nothing is idle", () => {
		const workspace: WorkspaceSummary = {
			id: "p1",
			name: "radic",
			sessions: [session("s-big", "big worker", "active"), session("s-small", "small worker", "active")],
		} as WorkspaceSummary;
		workspaceQueryMock.mockReturnValue({ data: [workspace], isError: false, isSuccess: true });
		appMemoryMock.mockReturnValue(appReading(1, 40, 20));
		renderButton();
		expect(screen.getByTestId("app-memory-indicator")).toHaveTextContent("21.5 GB· Pause big worker");
	});

	it("goes grey and falls back to AO's own size where the host cannot be read", () => {
		appMemoryMock.mockReturnValue({ isError: false, data: { app: { rssBytes: 4 * GIB, processCount: 20, cpuPercent: 0 }, liveCount: 1 } });
		renderButton();
		const button = screen.getByTestId("app-memory-indicator");
		expect(button).toHaveTextContent("4.3 GB");
		expect(button).toHaveAttribute("data-memory-state", "unknown");
	});

	it("opens a window: stacked bar, rows largest first with a stop that frees memory, AO pinned last", async () => {
		renderButton();
		await userEvent.click(screen.getByTestId("app-memory-indicator"));
		const table = await screen.findByTestId("session-memory-table");
		expect(screen.getByTestId("session-memory-stacked")).toHaveTextContent("AO2.1 GB");
		expect(screen.getByTestId("session-memory-stacked")).toHaveTextContent("Available21.5 GB");
		expect(screen.getByTestId("session-memory-stacked")).not.toHaveTextContent("Other");
		// Fine: no suggestion, every row grey.
		expect(screen.queryByTestId("session-memory-suggestion")).not.toBeInTheDocument();
		const rows = within(table).getAllByTestId("session-memory-row");
		expect(rows.map((row) => row.textContent)).toEqual([expect.stringContaining("big worker"), expect.stringContaining("small worker")]);
		expect(rows.every((row) => row.getAttribute("data-chip-tone") === "neutral")).toBe(true);
		expect(within(rows[0]).getByText("2.3 GB")).toBeInTheDocument();
		// An unsampled session is not a row: never "0 MB".
		expect(within(table).queryByText("unsampled worker")).not.toBeInTheDocument();
		const own = within(table).getByTestId("session-memory-own-row");
		expect(own).toHaveTextContent("Daemon and app");
		expect(own).toHaveTextContent("315 MB");
		expect(within(own).queryByRole("button")).not.toBeInTheDocument();

		await userEvent.click(within(rows[0]).getByRole("button", { name: "Stop agent for big worker" }));
		await waitFor(() =>
			expect(postMock).toHaveBeenCalledWith("/api/v1/sessions/{sessionId}/exit-agent", {
				params: { path: { sessionId: "s-big" } },
				body: { policy: "drain" },
			}),
		);
	});

	it("expands any number of rows into their process trees, and collapses each on a second click", async () => {
		renderButton();
		await userEvent.click(screen.getByTestId("app-memory-indicator"));
		const table = await screen.findByTestId("session-memory-table");
		const bigRow = within(table).getAllByTestId("session-memory-row")[0];
		const own = within(table).getByTestId("session-memory-own-row");

		expect(screen.queryByTestId("session-memory-process-row")).not.toBeInTheDocument();
		await userEvent.click(bigRow);
		const children = await screen.findAllByTestId("session-memory-process-row");
		expect(children[0]).toHaveTextContent("├─ claude");
		expect(children[0]).toHaveTextContent("1.8 GB");
		expect(children[1]).toHaveTextContent("└─ go test");

		// A second row opens alongside, not instead.
		await userEvent.click(own);
		expect(screen.getAllByTestId("session-memory-process-row")).toHaveLength(3);

		await userEvent.click(bigRow);
		expect(screen.getAllByTestId("session-memory-process-row")).toHaveLength(1);
	});

	it("shows CPU only while a session is working", async () => {
		const workspace: WorkspaceSummary = {
			id: "p1",
			name: "radic",
			sessions: [session("s-big", "big worker", "active"), session("s-small", "small worker")],
		} as WorkspaceSummary;
		workspaceQueryMock.mockReturnValue({ data: [workspace], isError: false, isSuccess: true });
		renderButton();
		await userEvent.click(screen.getByTestId("app-memory-indicator"));
		const rows = within(await screen.findByTestId("session-memory-table")).getAllByTestId("session-memory-row");
		expect(rows[0]).toHaveTextContent("82%");
		expect(rows[1]).not.toHaveTextContent("%");
	});

	it("colours only the rows that are part of the fix, and the one button stops them", async () => {
		postMock.mockImplementation(async (path: string) =>
			path === "/api/v1/sessions/pause-idle" ? { data: { ok: true, paused: ["s-small", "s-big"], failed: [] } } : { data: {} },
		);
		appMemoryMock.mockReturnValue(appReading(3, 12, 20));
		renderButton();
		await userEvent.click(screen.getByTestId("app-memory-indicator"));
		const table = await screen.findByTestId("session-memory-table");
		expect(screen.getByTestId("session-memory-suggestion")).toHaveTextContent("3 sessions idle for over 30 minutes");
		const rows = within(table).getAllByTestId("session-memory-row");
		expect(rows.every((row) => row.getAttribute("data-chip-tone") === "warning")).toBe(true);

		await userEvent.click(screen.getByTestId("session-memory-fix"));
		await waitFor(() =>
			expect(postMock).toHaveBeenCalledWith("/api/v1/sessions/pause-idle", { params: { query: { idleMinutes: 30 } } }),
		);
	});

	it("marks the single largest session red when the machine is tight and everything is busy", async () => {
		const workspace: WorkspaceSummary = {
			id: "p1",
			name: "radic",
			sessions: [session("s-big", "big worker", "active"), session("s-small", "small worker", "active")],
		} as WorkspaceSummary;
		workspaceQueryMock.mockReturnValue({ data: [workspace], isError: false, isSuccess: true });
		appMemoryMock.mockReturnValue(appReading(1, 40, 20));
		renderButton();
		await userEvent.click(screen.getByTestId("app-memory-indicator"));
		const rows = within(await screen.findByTestId("session-memory-table")).getAllByTestId("session-memory-row");
		expect(rows[0]).toHaveAttribute("data-chip-tone", "critical");
		expect(rows[1]).toHaveAttribute("data-chip-tone", "neutral");
		expect(screen.getByTestId("session-memory-fix")).toHaveTextContent("Pause big worker");
	});

	it("lists a paused agent apart, greyed, with resume", async () => {
		const workspace: WorkspaceSummary = {
			id: "p1",
			name: "radic",
			sessions: [session("s-big", "big worker"), session("s-small", "small worker", "exited")],
		} as WorkspaceSummary;
		workspaceQueryMock.mockReturnValue({ data: [workspace], isError: false, isSuccess: true });
		memoryQueryMock.mockReturnValue({ isError: false, data: new Map([["s-big", reading("s-big", 2_254_857_830, 9)]]) });
		renderButton();
		await userEvent.click(screen.getByTestId("app-memory-indicator"));
		const table = await screen.findByTestId("session-memory-table");
		const pausedRow = within(table).getByTestId("session-memory-paused-row");
		expect(pausedRow).toHaveTextContent("small worker");
		expect(pausedRow).toHaveTextContent("paused · holding nothing");
		await userEvent.click(within(pausedRow).getByRole("button", { name: "Resume agent for small worker" }));
		await waitFor(() =>
			expect(postMock).toHaveBeenCalledWith("/api/v1/sessions/{sessionId}/resume-agent", {
				params: { path: { sessionId: "s-small" } },
			}),
		);
	});
});
