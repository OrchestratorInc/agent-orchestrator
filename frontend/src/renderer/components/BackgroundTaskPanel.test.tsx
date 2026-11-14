import { act, fireEvent, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { BackgroundTask } from "../hooks/useBackgroundTasks";
import { BackgroundTaskPanel, TailedOutput } from "./BackgroundTaskPanel";

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

beforeEach(() => {
	vi.clearAllMocks();
	mutateMock.mockResolvedValue(undefined);
	outputMock.mockReturnValue(outputOf("> vite\n  Local: http://localhost:3000/"));
});

describe("background task panel", () => {
	it("shows the task, its command and its output, and stops it", async () => {
		render(<BackgroundTaskPanel isVisible sessionId="s" tab={{ task: task() }} />);
		expect(screen.getByRole("heading", { name: "Dev server on port 3000" })).toBeVisible();
		expect(screen.getByText("Running")).toBeVisible();
		expect(screen.getByText("12m")).toBeVisible();
		expect(screen.getByText("npm run dev -- --port 3000")).toBeVisible();
		expect(screen.getByRole("region", { name: "Output" })).toHaveTextContent("Local: http://localhost:3000/");
		expect(outputMock).toHaveBeenCalledWith("s", "dev", { enabled: true, live: true });
		await userEvent.click(screen.getByRole("button", { name: "Stop Dev server on port 3000" }));
		expect(mutateMock).toHaveBeenCalledWith("dev");
	});

	it("keeps a finished task readable without a stop, and says when a task ended without an outcome", () => {
		const { rerender } = render(<BackgroundTaskPanel isVisible sessionId="s" tab={{ task: task({ state: "failed", endedAt: ago(60_000) }) }} />);
		expect(screen.getByText("Failed")).toBeVisible();
		expect(screen.getByText("11m")).toBeVisible();
		expect(screen.queryByRole("button", { name: /Stop/ })).toBeNull();
		expect(outputMock).toHaveBeenLastCalledWith("s", "dev", { enabled: true, live: false });
		rerender(<BackgroundTaskPanel isVisible sessionId="s" tab={{ task: task(), ended: true }} />);
		expect(screen.getByText("Ended")).toBeVisible();
		expect(screen.queryByRole("button", { name: /Stop/ })).toBeNull();
	});

	it("reads once more when the task ends, and never polls while hidden", () => {
		const refetch = vi.fn();
		outputMock.mockReturnValue({ ...outputOf("tick-1"), refetch });
		const { rerender } = render(<BackgroundTaskPanel isVisible={false} sessionId="s" tab={{ task: task() }} />);
		expect(outputMock).toHaveBeenLastCalledWith("s", "dev", { enabled: false, live: false });
		rerender(<BackgroundTaskPanel isVisible sessionId="s" tab={{ task: task() }} />);
		rerender(<BackgroundTaskPanel isVisible sessionId="s" tab={{ task: task({ state: "completed", endedAt: ago(0) }) }} />);
		expect(refetch).toHaveBeenCalledTimes(1);
	});

	it.each([
		["unavailable", outputOf("", { availability: "unavailable" }), "This agent doesn't report this task's output"],
		["empty", outputOf(""), "No output yet"],
		["error", { data: undefined, isError: true, refetch: vi.fn() }, "Couldn't load the output"],
	])("says so when the output is %s", (_name, result, message) => {
		outputMock.mockReturnValue(result);
		render(<BackgroundTaskPanel isVisible sessionId="s" tab={{ task: task() }} />);
		expect(screen.getByText(message)).toBeVisible();
		expect(screen.queryByRole("region", { name: "Output" })).toBeNull();
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
		const line = region.querySelector("pre")!.firstChild!;
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
