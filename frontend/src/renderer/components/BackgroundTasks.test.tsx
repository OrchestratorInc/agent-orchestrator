import { act, render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { WorkspaceSession } from "../types/workspace";
import type { BackgroundTask, BackgroundTaskReading } from "../hooks/useBackgroundTasks";
import { BackgroundTasksBadge, ComposerBackgroundTasks, SessionBackgroundTasks, formatElapsed } from "./BackgroundTasks";

const { queryMock, mutateMock } = vi.hoisted(() => ({ queryMock: vi.fn(), mutateMock: vi.fn() }));
vi.mock("../hooks/useBackgroundTasks", () => ({
	useBackgroundTasks: queryMock,
	useStopBackgroundTask: () => ({ mutateAsync: mutateMock }),
}));

const session = { id: "s", title: "Settings", mode: "chat", provider: "claude-code", status: "idle" } as WorkspaceSession;
const ago = (ms: number) => new Date(Date.now() - ms).toISOString();
const shell = (id: string, description: string, extra: Partial<BackgroundTask> = {}): BackgroundTask => ({
	id, kind: "shell", state: "running", description, startedAt: ago(12 * 60_000), stoppable: true, ...extra,
});
const readingOf = (tasks: BackgroundTask[], availability: BackgroundTaskReading["availability"] = "ready"): BackgroundTaskReading => ({
	sessionId: "s", controllerGeneration: "g", availability, tasks,
});
const reading = readingOf([shell("shell", "Dev server")]);

beforeEach(() => {
	vi.clearAllMocks();
	mutateMock.mockResolvedValue(undefined);
	queryMock.mockReturnValue({ data: reading, isError: false });
});

describe("board badge", () => {
	it("is plain text: the count shows, nothing opens", () => {
		const tasks = [...reading.tasks, { id: "agent", kind: "agent" as const, state: "running" as const, description: "Review" }, { id: "done", kind: "agent" as const, state: "completed" as const, description: "Done" }];
		render(<BackgroundTasksBadge session={session} reading={readingOf(tasks)} />);
		expect(screen.getByText("2 background tasks running")).toBeVisible();
		expect(screen.queryByRole("button")).toBeNull();
	});

	it("labels cached data stale and hides unsupported, empty, remote and terminated readings", () => {
		const { rerender, container } = render(<BackgroundTasksBadge session={session} reading={reading} stale />);
		expect(screen.getByText("1 shell still running · stale")).toBeVisible();
		for (const [next, data] of [
			[session, readingOf(reading.tasks, "unavailable")],
			[session, readingOf([])],
			[{ ...session, hostId: "another-host" }, reading],
			[{ ...session, isTerminated: true }, reading],
		] as const) {
			rerender(<BackgroundTasksBadge session={next} reading={data} />);
			expect(container).toBeEmptyDOMElement();
		}
	});
});

describe("composer tab", () => {
	it("shows one task on one line with its time and a stop square", async () => {
		render(<SessionBackgroundTasks session={session} />);
		const tab = screen.getByRole("region", { name: "Background tasks" });
		expect(within(tab).getByText("Dev server")).toBeVisible();
		expect(within(tab).getByText("12m")).toBeVisible();
		// A dev server is meant to keep running: no spinner or dot, but screen readers hear its state.
		const row = within(tab).getByText("Dev server").closest("li")!;
		expect(within(row).queryByRole("img")).toBeNull();
		expect(row).toHaveTextContent("Running");
		await userEvent.click(within(tab).getByRole("button", { name: "Stop Dev server" }));
		expect(mutateMock).toHaveBeenCalledWith("shell");
		// One request at a time: the square waits for the task to leave.
		expect(within(tab).getByRole("button", { name: "Stop Dev server" })).toBeDisabled();
	});

	it("names a raw shell command by what it does", async () => {
		queryMock.mockReturnValue({ data: readingOf([shell("dev", "PORT=3000 npm run dev > /tmp/dev.log 2>&1")]) });
		render(<SessionBackgroundTasks session={session} />);
		const tab = screen.getByRole("region", { name: "Background tasks" });
		expect(within(tab).getByText("Dev server on port 3000")).toBeVisible();
		expect(tab).not.toHaveTextContent("/tmp/dev.log");
		expect(within(tab).getByRole("button", { name: "Stop Dev server on port 3000" })).toBeVisible();
		// The label is the summary; the tooltip keeps the command.
		expect(within(tab).getByText("Dev server on port 3000")).toHaveAttribute("title", "PORT=3000 npm run dev > /tmp/dev.log 2>&1");
	});

	it("tells like tasks apart by folder, then by command", async () => {
		render(<ComposerBackgroundTasks sessionId="s" reading={readingOf([
			shell("web", "cd frontend && npm run dev"),
			shell("api", "cd backend && npm run dev"),
			shell("a", "npm run test -- a"),
			shell("b", "npm run test -- b"),
		])} />);
		await userEvent.click(screen.getByRole("button", { name: /4 running/ }));
		await userEvent.click(screen.getByRole("button", { name: "Stop Dev server · backend" }));
		expect(mutateMock).toHaveBeenCalledWith("api");
		expect(screen.getByRole("button", { name: "Stop Dev server · frontend" })).toBeVisible();
		expect(screen.getByRole("button", { name: "Stop npm run test -- a" })).toBeVisible();
	});

	it("shows nothing but the kind for a blank description", () => {
		queryMock.mockReturnValue({ data: readingOf([shell("blank", "   ")]) });
		render(<SessionBackgroundTasks session={session} />);
		expect(screen.getByRole("button", { name: "Stop Shell" })).toBeVisible();
	});

	it("names a failed task without saying it is still going", () => {
		queryMock.mockReturnValue({ data: readingOf([shell("t", "npm run test", { state: "failed", endedAt: ago(1_000) })]) });
		render(<SessionBackgroundTasks session={session} />);
		const row = screen.getByText("Tests").closest("li")!;
		expect(within(row).getByRole("img", { name: "Failed" })).toBeVisible();
		expect(row).not.toHaveTextContent("Running");
	});

	it("offers no stop where the provider cannot stop the task", () => {
		queryMock.mockReturnValue({ data: readingOf([shell("shell", "Dev server", { stoppable: false })]) });
		render(<SessionBackgroundTasks session={session} />);
		expect(screen.getByText("Dev server")).toBeVisible();
		expect(screen.queryByRole("button")).toBeNull();
	});

	it("leaves agents to Subagents and finished work out", () => {
		queryMock.mockReturnValue({ data: readingOf([
			{ id: "agent", kind: "agent", state: "running", description: "Audit the catalogs" },
			shell("done", "Install", { state: "completed", endedAt: ago(1_000) }),
			shell("stopped", "Old server", { state: "stopped", endedAt: ago(1_000) }),
		]) });
		const { container } = render(<SessionBackgroundTasks session={session} />);
		expect(container).toBeEmptyDOMElement();
	});

	it("renders nothing for unsupported readings or sessions", () => {
		queryMock.mockReturnValue({ data: readingOf(reading.tasks, "unavailable") });
		const { container, rerender } = render(<SessionBackgroundTasks session={session} />);
		expect(container).toBeEmptyDOMElement();
		queryMock.mockReturnValue({ data: reading });
		rerender(<SessionBackgroundTasks session={{ ...session, hostId: "another-host" }} />);
		expect(container).toBeEmptyDOMElement();
	});

	it("folds several tasks into a count and opens them as rows", async () => {
		const tasks = [
			shell("lint", "Run the Go linter", { state: "failed", startedAt: ago(5 * 60_000), endedAt: ago(3 * 60_000) }),
			shell("dev", "Start the renderer dev server"),
			shell("watch", "Watch the composer tests", { startedAt: ago(9 * 60_000) }),
		];
		render(<ComposerBackgroundTasks sessionId="s" reading={readingOf(tasks)} />);
		const toggle = screen.getByRole("button", { name: /2 running/ });
		expect(toggle).toHaveAttribute("aria-expanded", "false");
		expect(screen.getByText("1 failed")).toBeVisible();
		// Closed, no task names and no stop: stop belongs to a single task.
		expect(screen.queryByText("Start the renderer dev server")).toBeNull();
		expect(screen.queryByRole("button", { name: /^Stop/ })).toBeNull();

		await userEvent.click(toggle);
		expect(toggle).toHaveAttribute("aria-expanded", "true");
		const rows = within(screen.getByRole("list")).getAllByRole("listitem");
		// Failures stay above the five-row cap, without a stop.
		expect(rows.map((row) => row.textContent)).toEqual(["Run the Go linter2m", "Start the renderer dev serverRunning12m", "Watch the composer testsRunning9m"]);
		// The red dot ends the row, in the stop square's place, after the time.
		expect(rows[0].lastElementChild).toContainElement(within(rows[0]).getByRole("img", { name: "Failed" }));
		expect(within(rows[0]).queryByRole("button")).toBeNull();
		await userEvent.click(within(rows[2]).getByRole("button", { name: "Stop Watch the composer tests" }));
		expect(mutateMock).toHaveBeenCalledWith("watch");
	});

	it("closes the list when work drops to one line and stays closed when it grows again", async () => {
		const two = readingOf([shell("a", "Dev server"), shell("b", "Watcher")]);
		const { rerender } = render(<ComposerBackgroundTasks sessionId="s" reading={two} />);
		await userEvent.click(screen.getByRole("button", { name: /2 running/ }));
		expect(screen.getByRole("list")).toBeVisible();
		rerender(<ComposerBackgroundTasks sessionId="s" reading={readingOf([shell("a", "Dev server")])} />);
		expect(screen.queryByRole("list")).toBeNull();
		rerender(<ComposerBackgroundTasks sessionId="s" reading={two} />);
		expect(screen.getByRole("button", { name: /2 running/ })).toHaveAttribute("aria-expanded", "false");
	});

	it("says when a stop fails and gives the square back", async () => {
		mutateMock.mockRejectedValueOnce(new Error("stop failed"));
		render(<ComposerBackgroundTasks sessionId="s" reading={reading} />);
		await userEvent.click(screen.getByRole("button", { name: "Stop Dev server" }));
		expect(screen.getByRole("status")).toHaveTextContent("Couldn't stop");
		expect(screen.getByRole("button", { name: "Stop Dev server" })).toBeEnabled();
	});

	it("handles an earlier stop failure after another task is stopped", async () => {
		let rejectFirst!: (error: Error) => void;
		let resolveSecond!: () => void;
		mutateMock.mockImplementationOnce(() => new Promise<void>((_resolve, reject) => { rejectFirst = reject; }))
			.mockImplementationOnce(() => new Promise<void>((resolve) => { resolveSecond = resolve; }));
		render(<ComposerBackgroundTasks sessionId="s" reading={readingOf([shell("a", "Server"), shell("b", "Watcher")])} />);
		await userEvent.click(screen.getByRole("button", { name: /2 running/ }));
		await userEvent.click(screen.getByRole("button", { name: "Stop Server" }));
		await userEvent.click(screen.getByRole("button", { name: "Stop Watcher" }));
		await act(async () => { resolveSecond(); rejectFirst(new Error("first failed")); });
		expect(screen.getByRole("status")).toHaveTextContent("Couldn't stop");
		expect(screen.getByRole("button", { name: "Stop Server" })).toBeEnabled();
		expect(screen.getByRole("button", { name: "Stop Watcher" })).toBeDisabled();
	});

	it("gives the square back when the task outlives its stop", async () => {
		vi.useFakeTimers({ shouldAdvanceTime: true });
		try {
			render(<ComposerBackgroundTasks sessionId="s" reading={reading} />);
			await userEvent.click(screen.getByRole("button", { name: "Stop Dev server" }));
			expect(screen.getByRole("button", { name: "Stop Dev server" })).toBeDisabled();
			act(() => { vi.advanceTimersByTime(10_000); });
			expect(screen.getByRole("button", { name: "Stop Dev server" })).toBeEnabled();
		} finally { vi.useRealTimers(); }
	});
});

it("formats elapsed time compactly", () => {
	expect([0, 38_000, 12 * 60_000 + 59_000, 65 * 60_000, -5_000].map(formatElapsed)).toEqual(["0s", "38s", "12m", "1h 5m", "0s"]);
});

it.each(["codex", "claude-code", "opencode", "opencode-v2", "droid", "kimi", "kimchi", "pi", "cursor", "omp", "qwen", "unreal-agent", "copilot", "agy", "deepseek-harness", "command-code"])("shows reported background tasks for Chat harness %s", (provider) => {
	queryMock.mockReturnValue({ data: readingOf([shell("shell", "Dev server", { stoppable: false })], "partial") });
	render(<SessionBackgroundTasks session={{ ...session, provider } as WorkspaceSession} />);
	expect(screen.getByRole("region", { name: "Background tasks" })).toHaveTextContent("Dev server");
});

it("hides cached controls after a reading fails", () => {
	const view = render(<SessionBackgroundTasks session={session} />);
	expect(screen.getByRole("button", { name: "Stop Dev server" })).toBeVisible();
	queryMock.mockReturnValue({ data: reading, isError: true });
	view.rerender(<SessionBackgroundTasks session={session} />);
	expect(screen.queryByRole("region", { name: "Background tasks" })).toBeNull();
});
it("shows no spinner on a live task", () => {
	const { container } = render(<ComposerBackgroundTasks sessionId="s" reading={reading} />);
	expect(container.querySelector(".animate-spin")).toBeNull();
});
it("keeps the remaining Stop node and focus when two tasks become one", async () => {
	const a = shell("a", "Server"),
		b = shell("b", "Watcher");
	const view = render(<ComposerBackgroundTasks sessionId="s" reading={readingOf([a, b])} />);
	await userEvent.click(screen.getByRole("button", { name: /2 running/ }));
	const remaining = screen.getByRole("button", { name: "Stop Watcher" });
	remaining.focus();
	view.rerender(<ComposerBackgroundTasks sessionId="s" reading={readingOf([b])} />);
	expect(screen.getByRole("button", { name: "Stop Watcher" })).toBe(remaining);
	expect(remaining).toHaveFocus();
});
it("moves focus to the next task, then the composer when focused tasks disappear", async () => {
	const a = shell("a", "Server"),
		b = shell("b", "Watcher");
	const renderTasks = (tasks: BackgroundTask[]) => (
		<div>
			<ComposerBackgroundTasks sessionId="s" reading={readingOf(tasks)} />
			<form className="cursor-chat-composer"><textarea aria-label="Message" /></form>
		</div>
	);
	const view = render(renderTasks([a, b]));
	await userEvent.click(screen.getByRole("button", { name: /2 running/ }));
	screen.getByRole("button", { name: "Stop Server" }).focus();
	view.rerender(renderTasks([b]));
	expect(screen.getByRole("button", { name: "Stop Watcher" })).toHaveFocus();
	view.rerender(renderTasks([]));
	expect(screen.getByRole("textbox", { name: "Message" })).toHaveFocus();
});
it("does not steal focus from another input when a task disappears", () => {
	const view = render(
		<div>
			<ComposerBackgroundTasks sessionId="s" reading={reading} />
			<input aria-label="Other" />
		</div>,
	);
	screen.getByRole("button", { name: "Stop Dev server" }).focus();
	screen.getByRole("textbox", { name: "Other" }).focus();
	view.rerender(
		<div>
			<ComposerBackgroundTasks sessionId="s" reading={readingOf([])} />
			<input aria-label="Other" />
		</div>,
	);
	expect(screen.getByRole("textbox", { name: "Other" })).toHaveFocus();
});

it("does not move disclosure focus when a poll refreshes expanded rows", async () => {
	const tasks = [shell("a", "Server"), shell("b", "Watcher")];
	const view = render(<ComposerBackgroundTasks sessionId="s" reading={readingOf(tasks)} />);
	const toggle = screen.getByRole("button", { name: /2 running/ });
	await userEvent.click(toggle);
	expect(toggle).toHaveFocus();
	view.rerender(<ComposerBackgroundTasks sessionId="s" reading={readingOf(tasks.map((task) => ({ ...task })))} />);
	expect(toggle).toHaveFocus();
});


it.each([false, true])("does not restore stale task focus after a transcript click (stopping=%s)", async (stopping) => {
	const renderTasks = (tasks: BackgroundTask[]) => <div>
		<p data-testid="transcript">Select this transcript text</p>
		<ComposerBackgroundTasks sessionId="s" reading={readingOf(tasks)} />
		<form className="cursor-chat-composer"><textarea aria-label="Message" /></form>
	</div>;
	const view = render(renderTasks(reading.tasks));
	const stop = screen.getByRole("button", { name: "Stop Dev server" });
	stop.focus();
	if (stopping) await userEvent.click(stop);
	const transcript = screen.getByTestId("transcript");
	await userEvent.click(transcript);
	// JSDOM retains disabled-button focus until unmount; assert its final
	// focus below, while the ordinary null-target blur is observable now.
	if (!stopping) expect(document.body).toHaveFocus();
	const selection = window.getSelection()!;
	const range = document.createRange();
	range.selectNodeContents(transcript);
	selection.removeAllRanges();
	selection.addRange(range);
	view.rerender(renderTasks([]));
	expect(document.body).toHaveFocus();
	expect(selection.toString()).toBe("Select this transcript text");
});

it("returns task focus to the composer rather than queued or elicitation inputs", () => {
	const renderTasks = (tasks: BackgroundTask[]) => <div>
		<div data-testid="queued-dock"><textarea aria-label="Queued message" /></div>
		<div data-testid="elicitation-dock"><input role="textbox" aria-label="Elicitation" /></div>
		<ComposerBackgroundTasks sessionId="s" reading={readingOf(tasks)} />
		<form className="cursor-chat-composer"><textarea aria-label="Message" /></form>
	</div>;
	const view = render(renderTasks(reading.tasks));
	screen.getByRole("button", { name: "Stop Dev server" }).focus();
	view.rerender(renderTasks([]));
	expect(screen.getByRole("textbox", { name: "Message" })).toHaveFocus();
});

it("does not restore task focus after leaving the window", () => {
	const renderTasks = (tasks: BackgroundTask[]) => <div>
		<ComposerBackgroundTasks sessionId="s" reading={readingOf(tasks)} />
		<form className="cursor-chat-composer"><textarea aria-label="Message" /></form>
	</div>;
	const view = render(renderTasks(reading.tasks));
	const stop = screen.getByRole("button", { name: "Stop Dev server" });
	stop.focus();
	stop.blur();
	window.dispatchEvent(new Event("blur"));
	view.rerender(renderTasks([]));
	expect(document.body).toHaveFocus();
});


it("relinquishes task focus on a null-target blur while the control is mounted", () => {
	const renderTasks = (tasks: BackgroundTask[]) => <div>
		<ComposerBackgroundTasks sessionId="s" reading={readingOf(tasks)} />
		<form className="cursor-chat-composer"><textarea aria-label="Message" /></form>
	</div>;
	const view = render(renderTasks(reading.tasks));
	const stop = screen.getByRole("button", { name: "Stop Dev server" });
	stop.focus();
	stop.blur();
	view.rerender(renderTasks([]));
	expect(document.body).toHaveFocus();
});
