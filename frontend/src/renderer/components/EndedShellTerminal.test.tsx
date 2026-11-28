import { StrictMode } from "react";
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
		<StrictMode>
			<QueryClientProvider client={queryClient}>
				<EndedShellTerminal shell={shell} />
			</QueryClientProvider>
		</StrictMode>,
	);
}

beforeEach(() => {
	postMock.mockReset();
	postMock.mockResolvedValue({
		data: { shellTerminal: { createdAt: ended.createdAt, handleId: "shellterm-new", title: "Build", workingDir: "/worktree" } },
	});
});

describe("EndedShellTerminal", () => {
	it("restores the shell once when first shown, without an ended notice", async () => {
		renderPane(ended);
		await waitFor(() => expect(postMock).toHaveBeenCalledTimes(1));
		expect(postMock.mock.calls[0][1].body).not.toHaveProperty("resumeAgent");
		expect(screen.queryByRole("heading", { name: "Terminal ended" })).toBeNull();
	});

	it("resumes the agent that was running in the shell", async () => {
		renderPane({ ...ended, handleId: "shellterm-agent", resumeHarness: "claude-code" });
		await waitFor(() => expect(postMock).toHaveBeenCalledTimes(1));
		expect(postMock.mock.calls[0][1].body).toMatchObject({ resumeAgent: true });
	});

	it("says the terminal ended when the restore fails and reopens a plain shell on request", async () => {
		postMock.mockResolvedValueOnce({
			error: { code: "SHELL_TERMINAL_AGENT_RESUME_UNSUPPORTED", message: "Reopening with an agent needs bash, zsh, sh or fish." },
		});
		renderPane({ ...ended, handleId: "shellterm-failed", resumeHarness: "codex" });
		expect(await screen.findByRole("alert")).toBeTruthy();
		expect(screen.getByRole("heading", { name: "Terminal ended" })).toBeTruthy();

		fireEvent.click(screen.getByRole("button", { name: "Reopen terminal" }));
		await waitFor(() => expect(postMock).toHaveBeenCalledTimes(2));
		expect(postMock.mock.calls[1][1].body).not.toHaveProperty("resumeAgent");
	});

	it("retries the agent resume after a failure that was not about the agent", async () => {
		postMock.mockResolvedValueOnce({ error: { code: "INTERNAL", message: "Daemon busy" } });
		renderPane({ ...ended, handleId: "shellterm-transient", resumeHarness: "claude-code" });
		fireEvent.click(await screen.findByRole("button", { name: "Reopen terminal" }));
		await waitFor(() => expect(postMock).toHaveBeenCalledTimes(2));
		expect(postMock.mock.calls[1][1].body).toMatchObject({ resumeAgent: true });
	});

	it("tells a pane remounted while the restore is pending that it failed", async () => {
		let fail!: (result: object) => void;
		postMock.mockReturnValueOnce(new Promise((resolve) => (fail = resolve)));
		const shell = { ...ended, handleId: "shellterm-remounted" };
		const first = renderPane(shell);
		await waitFor(() => expect(postMock).toHaveBeenCalledTimes(1));
		first.unmount();
		renderPane(shell);
		fail({ error: { code: "SHELL_TERMINAL_NOT_ENDED", message: "This terminal is still running" } });
		expect(await screen.findByRole("alert")).toBeTruthy();
		expect(postMock).toHaveBeenCalledTimes(1);
	});
});
