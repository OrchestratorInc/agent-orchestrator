import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { WorkspaceSession } from "../types/workspace";
import type { BackgroundTaskReading } from "../hooks/useBackgroundTasks";
import { BackgroundTasksButton, SessionBackgroundTasks } from "./BackgroundTasks";

const { queryMock, memoryMock } = vi.hoisted(() => ({ queryMock: vi.fn(), memoryMock: vi.fn() }));
vi.mock("../hooks/useBackgroundTasks", () => ({ useBackgroundTasks: queryMock }));
vi.mock("../hooks/useSessionMemory", () => ({ useDiagnosticsEnabled: () => false, useSessionMemory: memoryMock, formatMemory: String }));

const session = { id: "s", title: "Settings", mode: "chat", provider: "claude-code", status: "idle" } as WorkspaceSession;
const reading: BackgroundTaskReading = { sessionId: "s", controllerGeneration: "g", availability: "ready", tasks: [{ id: "shell", kind: "shell", state: "running", description: "Dev server" }] };

describe("background tasks", () => {
	beforeEach(() => { vi.clearAllMocks(); queryMock.mockReturnValue({ data: reading, refetch: vi.fn(), isPending: false, isError: false }); });

	it("shows a native shell after the turn settles, independently of process diagnostics", async () => {
		render(<BackgroundTasksButton session={session} reading={reading} />);
		await userEvent.click(screen.getByRole("button", { name: "1 shell still running" }));
		expect(screen.getByRole("dialog")).toBeVisible();
		expect(screen.getByText("Dev server")).toBeVisible();
		expect(memoryMock).not.toHaveBeenCalled();
	});

	it("counts mixed tasks and excludes completed work from the badge", () => {
		const tasks = [...reading.tasks, { id: "agent", kind: "agent" as const, state: "running" as const, description: "Review" }, { id: "done", kind: "agent" as const, state: "completed" as const, description: "Done" }];
		render(<BackgroundTasksButton session={session} reading={{ ...reading, tasks }} />);
		expect(screen.getByRole("button", { name: "2 background tasks running" })).toBeVisible();
	});

	it("returns keyboard focus to the trigger when the dialog closes", async () => {
		render(<BackgroundTasksButton session={session} reading={reading} />);
		const trigger = screen.getByRole("button", { name: "1 shell still running" });
		await userEvent.click(trigger);
		await userEvent.keyboard("{Escape}");
		await waitFor(() => expect(trigger).toHaveFocus());
	});

	it("does not turn unsupported or empty readings into a process count", () => {
		const { rerender } = render(<BackgroundTasksButton session={session} reading={{ ...reading, availability: "unavailable" }} />);
		expect(screen.queryByRole("button")).toBeNull();
		rerender(<BackgroundTasksButton session={session} reading={{ ...reading, tasks: [] }} />);
		expect(screen.queryByRole("button")).toBeNull();
	});

	it("keeps selected task details open after completion", async () => {
		const { rerender } = render(<BackgroundTasksButton session={session} reading={reading} />);
		await userEvent.click(screen.getByRole("button", { name: "1 shell still running" }));
		await userEvent.click(screen.getByRole("button", { name: /Dev server/ }));
		queryMock.mockReturnValue({ data: { ...reading, tasks: [{ ...reading.tasks[0], state: "completed" }] }, refetch: vi.fn() });
		rerender(<BackgroundTasksButton session={session} reading={{ ...reading, tasks: [] }} />);
		expect(screen.getByRole("dialog")).toBeVisible();
		expect(screen.getByText("Recently finished")).toBeVisible();
		expect(screen.getAllByText("Completed")).toHaveLength(2);
	});

	it("labels cached data stale and hides local readings on remote or terminated sessions", () => {
		const { rerender } = render(<BackgroundTasksButton session={session} reading={reading} stale />);
		expect(screen.getByRole("button", { name: /stale/ })).toBeVisible();
		rerender(<BackgroundTasksButton session={{ ...session, hostId: "another-host" }} reading={reading} />);
		expect(screen.queryByRole("button")).toBeNull();
		rerender(<BackgroundTasksButton session={{ ...session, isTerminated: true }} reading={reading} />);
		expect(screen.queryByRole("button")).toBeNull();
	});
});

it.each(["codex", "claude-code", "opencode", "opencode-v2", "droid", "kimi", "kimchi", "pi", "cursor", "omp", "qwen", "unreal-agent", "copilot", "agy", "deepseek-harness", "command-code"])("shows reported background tasks for Chat harness %s", async (provider) => {
 const partial = { ...reading, availability: "partial" as const };
 queryMock.mockReturnValue({ data: partial, refetch: vi.fn(), isPending: false, isError: false });
 render(<BackgroundTasksButton session={{ ...session, provider } as WorkspaceSession} reading={partial} />);
 await userEvent.click(screen.getByRole("button", { name: "1 shell still running" }));
 expect(screen.getByText(/Only background jobs explicitly reported/)).toBeVisible();
 expect(screen.getByText("Dev server")).toBeVisible();
});


it("hides the Chat entry when no background task is running", () => {
 queryMock.mockReturnValue({ data: { ...reading, tasks: [] }, isError: false });
 const { rerender } = render(<SessionBackgroundTasks session={session} />);
 expect(screen.queryByRole("button")).toBeNull();
 queryMock.mockReturnValue({ data: reading, isError: false });
 rerender(<SessionBackgroundTasks session={session} />);
 expect(screen.getByRole("button", { name: "1 shell still running" })).toBeVisible();
 queryMock.mockReturnValue({ data: { ...reading, tasks: [{ ...reading.tasks[0], state: "completed" }] }, isError: false });
 rerender(<SessionBackgroundTasks session={session} />);
 expect(screen.queryByRole("button")).toBeNull();
});

it("removes the trigger when the last task finishes while keeping its open panel", async () => {
 queryMock.mockReturnValue({ data: reading, refetch: vi.fn(), isError: false });
 const { rerender } = render(<><textarea aria-label="Message the agent" /><BackgroundTasksButton session={session} reading={reading} /></>);
 await userEvent.click(screen.getByRole("button", { name: "1 shell still running" }));
 const finished = { ...reading, tasks: [{ ...reading.tasks[0], state: "completed" as const }] };
 queryMock.mockReturnValue({ data: finished, refetch: vi.fn(), isError: false });
 rerender(<><textarea aria-label="Message the agent" /><BackgroundTasksButton session={session} reading={finished} /></>);
 expect(screen.queryByRole("button", { name: "1 shell still running" })).toBeNull();
 expect(screen.queryByRole("button", { name: "Background tasks" })).toBeNull();
 expect(screen.getByRole("dialog")).toBeVisible();
 await userEvent.keyboard("{Escape}");
 await waitFor(() => expect(screen.getByRole("textbox", { name: "Message the agent" })).toHaveFocus());
});
