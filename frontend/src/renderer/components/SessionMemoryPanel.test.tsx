import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { toKanbanColumn } from "@aoagents/product-ui";
import type { WorkspaceSession, WorkspaceSummary } from "../types/workspace";
import { SessionMemoryButton } from "./SessionMemoryPanel";
import { TooltipProvider } from "./ui/tooltip";

const { memoryQueryMock, postMock, workspaceQueryMock } = vi.hoisted(() => ({
	memoryQueryMock: vi.fn(),
	postMock: vi.fn(),
	workspaceQueryMock: vi.fn(),
}));

vi.mock("../hooks/useSessionMemory", async (importOriginal) => ({
	...(await importOriginal<typeof import("../hooks/useSessionMemory")>()),
	useSessionMemory: memoryQueryMock,
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

function session(id: string, title: string, activityState = "idle"): WorkspaceSession {
	return {
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
	return render(
		<QueryClientProvider client={queryClient}>
			<TooltipProvider>
				<SessionMemoryButton projectId="p1" />
			</TooltipProvider>
		</QueryClientProvider>,
	);
}

beforeEach(() => {
	postMock.mockReset().mockResolvedValue({ data: {} });
	const workspace: WorkspaceSummary = {
		id: "p1",
		name: "radic",
		sessions: [session("s-small", "small worker"), session("s-big", "big worker"), session("s-none", "unsampled worker")],
	} as WorkspaceSummary;
	workspaceQueryMock.mockReset().mockReturnValue({ data: [workspace], isError: false, isSuccess: true });
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

describe("SessionMemoryButton", () => {
	it("hides itself when nothing was sampled or the daemon cannot measure", () => {
		memoryQueryMock.mockReturnValue({ isError: true, data: undefined });
		renderButton();
		expect(screen.queryByTestId("session-memory-button")).not.toBeInTheDocument();
	});

	it("shows the total and opens a panel sorted largest first with a working kill", async () => {
		renderButton();
		const button = screen.getByTestId("session-memory-button");
		expect(button).toHaveTextContent("2.7 GB");
		expect(button).toHaveAttribute("aria-label", "2.7 GB memory used by 2 sessions");

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
		await userEvent.click(screen.getByTestId("session-memory-button"));
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
		await userEvent.click(screen.getByTestId("session-memory-button"));
		const table = await screen.findByTestId("session-memory-table");
		const rows = within(table).getAllByTestId("session-memory-row");
		const unsampledRow = rows[2];

		await userEvent.click(unsampledRow);
		expect(screen.queryByTestId("session-memory-process-row")).not.toBeInTheDocument();

		const bigRow = rows[0];
		await userEvent.click(within(bigRow).getByRole("button", { name: "Terminate big worker" }));
		expect(screen.queryByTestId("session-memory-process-row")).not.toBeInTheDocument();
	});

	it("runs the existing cleanup route for the project", async () => {
		renderButton();
		await userEvent.click(screen.getByTestId("session-memory-button"));
		await userEvent.click(await screen.findByRole("button", { name: "Clean up terminated" }));
		await waitFor(() =>
			expect(postMock).toHaveBeenCalledWith("/api/v1/sessions/cleanup", { params: { query: { project: "p1" } } }),
		);
	});
});
