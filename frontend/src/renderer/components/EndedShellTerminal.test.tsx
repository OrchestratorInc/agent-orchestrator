import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";

const { postMock } = vi.hoisted(() => ({ postMock: vi.fn() }));

vi.mock("../lib/host-clients", () => ({
	clientForSessionHost: () => ({ GET: vi.fn(async () => ({ data: { shellTerminals: [] } })), POST: postMock }),
}));
vi.mock("../stores/terminal-shell-store", () => ({
	terminalShellRequestValue: () => "auto",
	useTerminalShellStore: { getState: () => ({ load: async () => undefined, preference: { kind: "auto" } }) },
}));

import { EndedShellTerminal } from "./EndedShellTerminal";
import { TooltipProvider } from "./ui/tooltip";
import type { ShellTerminal } from "../hooks/useShellTerminals";

const ended: ShellTerminal = {
	createdAt: "2026-11-09T09:00:00Z",
	endedAt: "2026-11-10T20:44:00Z",
	handleId: "shellterm-ended",
	sessionId: "session-1",
	title: "Build",
	workingDir: "/worktree",
};

function renderPane(shell: ShellTerminal) {
	const queryClient = new QueryClient({ defaultOptions: { mutations: { retry: false }, queries: { retry: false } } });
	return render(
		<QueryClientProvider client={queryClient}>
			<TooltipProvider>
				<EndedShellTerminal shell={shell} />
			</TooltipProvider>
		</QueryClientProvider>,
	);
}

beforeEach(() => {
	postMock.mockReset();
	postMock.mockResolvedValue({
		data: { shellTerminal: { createdAt: ended.createdAt, handleId: "shellterm-new", title: "Build", workingDir: "/worktree" } },
	});
});

describe("EndedShellTerminal", () => {
	it("says the terminal ended and reopens a plain shell only when asked", async () => {
		renderPane(ended);
		expect(screen.getByRole("heading", { name: "Terminal ended" })).toBeTruthy();
		expect(screen.getByText(/Nothing it ran before will run again/)).toBeTruthy();
		// No agent was seen, so there is nothing to resume.
		expect(screen.queryByRole("button", { name: /Reopen with/ })).toBeNull();
		expect(postMock).not.toHaveBeenCalled();

		fireEvent.click(screen.getByRole("button", { name: "Reopen terminal" }));
		await waitFor(() => expect(postMock).toHaveBeenCalledTimes(1));
		expect(postMock.mock.calls[0][1].body).not.toHaveProperty("resumeAgent");
	});

	it("offers to resume the agent last seen in the shell", async () => {
		renderPane({ ...ended, resumeHarness: "claude-code" });
		fireEvent.click(screen.getByRole("button", { name: "Reopen with Claude Code" }));
		await waitFor(() => expect(postMock).toHaveBeenCalledTimes(1));
		expect(postMock.mock.calls[0][1].body).toMatchObject({ resumeAgent: true });
	});

	it("shows a failed reopen in place", async () => {
		postMock.mockResolvedValue({ error: { code: "SHELL_TERMINAL_NOT_ENDED", message: "This terminal is still running" } });
		renderPane(ended);
		fireEvent.click(screen.getByRole("button", { name: "Reopen terminal" }));
		expect(await screen.findByRole("alert")).toBeTruthy();
	});
});
