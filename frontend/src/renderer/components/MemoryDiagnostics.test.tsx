import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { Profiler } from "react";
import { render, screen } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { toKanbanColumn } from "@aoagents/product-ui";
import type { WorkspaceSession, WorkspaceSummary } from "../types/workspace";
import { useUiStore } from "../stores/ui-store";
import { MemoryDiagnostics } from "./SessionMemoryPanel";
import { TooltipProvider } from "./ui/tooltip";

// The real memory hooks, unlike SessionMemoryPanel.test.tsx: the loop this
// guards against lived in their `select`, which those mocks replace.
const { getMock } = vi.hoisted(() => ({ getMock: vi.fn() }));

vi.mock("../lib/api-client", () => ({
	apiClient: { GET: (...args: unknown[]) => getMock(...args) },
	apiErrorCode: () => undefined,
	apiErrorMessage: (_error: unknown, fallback: string) => fallback,
}));

const workspaces: WorkspaceSummary[] = [];
vi.mock("../hooks/useWorkspaceQuery", () => ({
	workspaceQueryKey: ["workspaces"],
	useWorkspaceQuery: () => ({ data: workspaces }),
}));

const usage = { data: new Map() };
vi.mock("../hooks/useSessionUsageSummaries", () => ({ useSessionUsageSummaries: () => usage }));

vi.mock("../lib/telemetry", () => ({ captureRendererEvent: vi.fn() }));

const GIB = 1024 ** 3;

function session(id: string): WorkspaceSession {
	return {
		id,
		title: id,
		workspaceId: "p1",
		workspaceName: "radic",
		provider: "claude-code",
		status: "idle",
		kanbanColumn: toKanbanColumn(undefined, "idle"),
		updatedAt: "2026-09-18T00:00:00Z",
		activity: { state: "idle", lastActivityAt: "2026-09-18T00:00:00Z" },
		prs: [],
	};
}

const system = {
	cpuPercent: 10,
	cpuMeasured: true,
	totalBytes: 32 * GIB,
	availableBytes: 16 * GIB,
	swapTotalBytes: 0,
	swapUsedBytes: 0,
	swapBytesPerSec: 0,
	cpuCount: 8,
	load1: 0.5,
	pressureRaw: 0,
	pressureSource: "psi",
};

describe("MemoryDiagnostics", () => {
	beforeEach(() => {
		workspaces.splice(0, workspaces.length, { id: "p1", name: "radic", sessions: [session("s1")] } as unknown as WorkspaceSummary);
		useUiStore.setState({ developerMode: true, diagnostics: true });
		getMock.mockImplementation(async (path: string) => {
			if (path === "/api/v1/usage/memory/pressure") return { data: system };
			return {
				data: {
					sessions: [{ sessionId: "s1", rssBytes: GIB, processCount: 1, cpuPercent: 1, sampledAt: "2026-09-18T00:00:00Z", processes: [] }],
					system,
					app: { rssBytes: 2 * GIB, processCount: 3, cpuPercent: 5, cpuMeasured: true },
				},
			};
		});
	});

	afterEach(() => {
		useUiStore.setState({ developerMode: false, diagnostics: false });
	});

	it("settles after a reading instead of re-rendering itself in a loop", async () => {
		const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
		let commits = 0;
		render(
			<QueryClientProvider client={queryClient}>
				<TooltipProvider>
					<Profiler id="diagnostics" onRender={() => (commits += 1)}>
						<MemoryDiagnostics />
					</Profiler>
				</TooltipProvider>
			</QueryClientProvider>,
		);
		expect(await screen.findByText("s1")).toBeInTheDocument();
		await new Promise((resolve) => setTimeout(resolve, 50));
		// No new data arrives in this window (the next sample is two seconds
		// away), so nothing should render. A fresh `select` result per render
		// fed SessionsTable's onRows effect, which re-rendered the parent,
		// which ran `select` again: "Maximum update depth exceeded".
		commits = 0;
		await new Promise((resolve) => setTimeout(resolve, 200));
		expect(commits).toBe(0);
	});
});
