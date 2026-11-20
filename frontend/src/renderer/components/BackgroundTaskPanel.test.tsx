import type { ComponentProps } from "react";
import { act, fireEvent, render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { BackgroundTask } from "../hooks/useBackgroundTasks";
import { useUiStore } from "../stores/ui-store";
import { BackgroundTaskPanel, BackgroundTasksPanel, TailedOutput, newestTasksFirst } from "./BackgroundTaskPanel";
import { resetStopsInFlightForTest } from "./BackgroundTasks";
import { TooltipProvider } from "./ui/tooltip";

const { outputMock, mutateMock } = vi.hoisted(() => ({ outputMock: vi.fn(), mutateMock: vi.fn() }));
vi.mock("../hooks/useBackgroundTasks", () => ({
	useBackgroundTaskOutput: outputMock,
	useStopBackgroundTask: () => ({ mutateAsync: mutateMock }),
}));

const ago = (ms: number) => new Date(Date.now() - ms).toISOString();
const task = (extra: Partial<BackgroundTask> = {}): BackgroundTask => ({
	id: "dev", kind: "shell", state: "running", description: "npm run dev -- --port 3000", command: "npm run dev -- --port 3000",
	startedAt: ago(12 * 60_000), stoppable: true, ...extra,
});
const outputOf = (output: string, extra: Record<string, unknown> = {}) => ({
	data: { sessionId: "s", taskId: "dev", availability: "ready", output, ...extra }, isError: false, refetch: vi.fn(),
});

/** The task view as the side panel mounts it: under the app's tooltip provider, with a way back to the list. */
function Panel({ onBack = () => {}, ...props }: Omit<ComponentProps<typeof BackgroundTaskPanel>, "onBack"> & { onBack?: () => void }) {
	return <TooltipProvider><BackgroundTaskPanel onBack={onBack} {...props} /></TooltipProvider>;
}

beforeEach(() => {
	vi.clearAllMocks();
	resetStopsInFlightForTest();
	mutateMock.mockResolvedValue(undefined);
	outputMock.mockReturnValue(outputOf("> vite\n  Local: http://localhost:3000/"));
});

describe("background task panel", () => {
	it("shows the task, its command and its output, and stops it", async () => {
		render(<Panel isVisible sessionId="s" tab={{ task: task() }} />);
		expect(screen.getByRole("region", { name: "Dev server on port 3000" })).toBeVisible();
		expect(screen.getByText("Running")).toBeVisible();
		expect(screen.getByText("12m")).toBeVisible();
		expect(screen.getByText("npm run dev -- --port 3000")).toBeVisible();
		expect(screen.getByText("2 lines")).toBeVisible();
		expect(screen.getByRole("region", { name: "Output" })).toHaveTextContent("Local: http://localhost:3000/");
		expect(outputMock).toHaveBeenCalledWith("s", "dev", { enabled: true, live: true });
		await userEvent.click(screen.getByRole("button", { name: "Stop Dev server on port 3000" }));
		expect(mutateMock).toHaveBeenCalledWith("dev");
	});

	it("counts the output's warnings and errors and tints their rows", () => {
		outputMock.mockReturnValue(outputOf("✓ Ready in 1.4s\n⚠ Fast Refresh had to perform a full reload\nsrc/a.tsx:4:2 - error TS2322: nope\nFound 1 error.\n"));
		render(<Panel isVisible sessionId="s" tab={{ task: task() }} />);
		expect(screen.getByText("4 lines")).toBeVisible();
		expect(screen.getByText("1 warning")).toBeVisible();
		expect(screen.getByText("1 error")).toBeVisible();
		const row = (text: string) => screen.getByText(text, { exact: false, selector: "pre > span" });
		expect(row("Ready in 1.4s")).toHaveClass("text-(--color-term-green)");
		expect(row("Fast Refresh")).toHaveClass("bg-(--color-term-yellow)/12");
		expect(row("error TS2322")).toHaveClass("bg-(--color-term-red)/12");
		expect(row("Found 1 error.").className).not.toMatch(/term-/);
	});

	it("keeps a finished task readable without a stop, and says when a task ended without an outcome", () => {
		const { rerender } = render(<Panel isVisible sessionId="s" tab={{ task: task({ state: "failed", endedAt: ago(60_000) }) }} />);
		expect(screen.getByText("Failed")).toBeVisible();
		expect(screen.getByText("11m")).toBeVisible();
		expect(screen.queryByRole("button", { name: /Stop/ })).toBeNull();
		expect(outputMock).toHaveBeenLastCalledWith("s", "dev", { enabled: true, live: false });
		rerender(<Panel isVisible sessionId="s" tab={{ task: task(), ended: true }} />);
		expect(screen.getByText("Ended")).toBeVisible();
		expect(screen.queryByRole("button", { name: /Stop/ })).toBeNull();
	});

	it("reads once more when the task ends, and never polls while hidden", () => {
		const refetch = vi.fn();
		outputMock.mockReturnValue({ ...outputOf("tick-1"), refetch });
		const { rerender } = render(<Panel isVisible={false} sessionId="s" tab={{ task: task() }} />);
		expect(outputMock).toHaveBeenLastCalledWith("s", "dev", { enabled: false, live: false });
		rerender(<Panel isVisible sessionId="s" tab={{ task: task() }} />);
		rerender(<Panel isVisible sessionId="s" tab={{ task: task({ state: "completed", endedAt: ago(0) }) }} />);
		expect(refetch).toHaveBeenCalledTimes(1);
	});

	it("keeps a task readable while its reading fails to refresh, without Stop or live output, until it recovers", () => {
		const { rerender } = render(<Panel isVisible sessionId="s" tab={{ task: task() }} />);
		const stopButton = () => screen.getByRole("button", { name: "Stop Dev server on port 3000" });
		expect(stopButton()).toBeEnabled();
		expect(outputMock).toHaveBeenLastCalledWith("s", "dev", { enabled: true, live: true });

		rerender(<Panel isVisible sessionId="s" stale tab={{ task: task() }} />);
		expect(screen.getByText("Running")).toBeVisible();
		expect(screen.getByText("stale")).toBeVisible();
		expect(screen.getByRole("region", { name: "Output" })).toHaveTextContent("Local: http://localhost:3000/");
		expect(stopButton()).toBeDisabled();
		expect(outputMock).toHaveBeenLastCalledWith("s", "dev", { enabled: true, live: false });

		rerender(<Panel isVisible sessionId="s" tab={{ task: task() }} />);
		expect(screen.queryByText("stale")).toBeNull();
		expect(stopButton()).toBeEnabled();
		expect(outputMock).toHaveBeenLastCalledWith("s", "dev", { enabled: true, live: true });
	});

	it.each([
		["unavailable", outputOf("", { availability: "unavailable" }), "This agent doesn't report this task's output"],
		["empty", outputOf(""), "No output yet"],
		["error", { data: undefined, isError: true, refetch: vi.fn() }, "Couldn't load the output"],
	])("says so when the output is %s", (_name, result, message) => {
		outputMock.mockReturnValue(result);
		render(<Panel isVisible sessionId="s" tab={{ task: task() }} />);
		expect(screen.getByText(message)).toBeVisible();
		expect(screen.queryByRole("region", { name: "Output" })).toBeNull();
	});
});

describe("background tasks list", () => {
	const uiKey = "list-session";
	function List() {
		const state = useUiStore((store) => store.inspectorSessions[uiKey]);
		const open = state?.taskTabs?.find((tab) => tab.task.id === state.activeTaskTab);
		return (
			<TooltipProvider>
				<BackgroundTasksPanel isVisible openTask={open} sessionId="s" tasks={state?.taskTabs ?? []} uiKey={uiKey} />
			</TooltipProvider>
		);
	}

	it("lists running tasks, then finished ones, opens one and comes back to its row", async () => {
		useUiStore.setState({ inspectorSessions: { [uiKey]: { isOpen: true, view: "task" } } });
		useUiStore.getState().syncTaskTabs(uiKey, [
			task(),
			task({ id: "build", description: "npm run build", command: "npm run build", state: "failed", startedAt: ago(5_000), endedAt: ago(3_000), stoppable: false }),
		]);
		render(<List />);
		const running = screen.getByRole("group", { name: "Running" });
		expect(running).toHaveTextContent("Running · 1");
		expect(running).toHaveTextContent("Dev server on port 3000");
		expect(running).toHaveTextContent("12m");
		const finished = screen.getByRole("group", { name: "Finished" });
		expect(within(finished).getByText("Failed")).toBeVisible();
		expect(within(finished).getByText("2s")).toBeVisible();

		await userEvent.click(within(finished).getByRole("button", { name: /Building/ }));
		expect(screen.getByRole("region", { name: "Building" })).toBeVisible();
		expect(screen.getByRole("button", { name: "All background tasks" })).toHaveFocus();
		expect(screen.queryByRole("button", { name: /Stop/ })).toBeNull();

		await userEvent.click(screen.getByRole("button", { name: "All background tasks" }));
		expect(useUiStore.getState().inspectorSessions[uiKey]?.activeTaskTab).toBeUndefined();
		expect(screen.getByRole("button", { name: /Building/ })).toHaveFocus();
	});
});

describe("background task order", () => {
	it("puts the newest start first, whatever order the tasks arrived in", () => {
		// One batch, ordered by ID as the daemon sends it: the newest task has the lowest ID.
		const entries = [
			{ task: task({ id: "a", description: "newest", startedAt: ago(5_000) }) },
			{ task: task({ id: "m", description: "no start time", startedAt: undefined }) },
			{ task: task({ id: "z", description: "oldest", startedAt: ago(60 * 60_000) }) },
		];
		expect(newestTasksFirst(entries).map((entry) => entry.task.id)).toEqual(["a", "z", "m"]);
	});
});

describe("tailed output", () => {
	function layout(region: HTMLElement, { scrollHeight, clientHeight }: { scrollHeight: number; clientHeight: number }) {
		Object.defineProperty(region, "scrollHeight", { configurable: true, value: scrollHeight });
		Object.defineProperty(region, "clientHeight", { configurable: true, value: clientHeight });
	}

	it("follows new lines, holds still while the user reads back, and resumes on Jump to latest", async () => {
		const { rerender } = render(<TailedOutput label="Output" text={"tick-1\ntick-2"} truncated={false} />);
		const region = screen.getByRole("region", { name: "Output" });
		layout(region, { scrollHeight: 1000, clientHeight: 200 });
		rerender(<TailedOutput label="Output" text={"tick-1\ntick-2\ntick-3"} truncated={false} />);
		expect(region.scrollTop).toBe(1000);
		expect(screen.queryByRole("button", { name: "Jump to latest" })).toBeNull();

		// Reading back: the text in view stays put while lines arrive.
		region.scrollTop = 300;
		fireEvent.scroll(region);
		rerender(<TailedOutput label="Output" text={"tick-2\ntick-3\ntick-4"} truncated />);
		expect(region).toHaveTextContent("tick-1");
		expect(region).not.toHaveTextContent("tick-4");
		expect(region.scrollTop).toBe(300);

		await userEvent.click(screen.getByRole("button", { name: "Jump to latest" }));
		expect(region).toHaveTextContent("tick-4");
		expect(region).toHaveTextContent("Older output isn't shown");
		expect(region.scrollTop).toBe(1000);
		expect(screen.queryByRole("button", { name: "Jump to latest" })).toBeNull();
	});

	it("resumes following when the user scrolls back to the end", () => {
		const { rerender } = render(<TailedOutput label="Output" text="tick-1" truncated={false} />);
		const region = screen.getByRole("region", { name: "Output" });
		layout(region, { scrollHeight: 1000, clientHeight: 200 });
		region.scrollTop = 100;
		fireEvent.scroll(region);
		rerender(<TailedOutput label="Output" text={"tick-1\ntick-2"} truncated={false} />);
		expect(region).not.toHaveTextContent("tick-2");
		region.scrollTop = 796;
		fireEvent.scroll(region);
		expect(region).toHaveTextContent("tick-2");
		expect(screen.queryByRole("button", { name: "Jump to latest" })).toBeNull();
	});

	it("holds the text still while some of it is selected", () => {
		const { rerender } = render(<TailedOutput label="Output" text="tick-1" truncated={false} />);
		const region = screen.getByRole("region", { name: "Output" });
		const line = region.querySelector("pre > span")!.firstChild!;
		act(() => {
			document.getSelection()!.setBaseAndExtent(line, 0, line, 4);
			document.dispatchEvent(new Event("selectionchange"));
		});
		rerender(<TailedOutput label="Output" text={"tick-1\ntick-2"} truncated={false} />);
		expect(region).not.toHaveTextContent("tick-2");
		act(() => {
			document.getSelection()!.removeAllRanges();
			document.dispatchEvent(new Event("selectionchange"));
		});
		expect(region).toHaveTextContent("tick-2");
	});
});
