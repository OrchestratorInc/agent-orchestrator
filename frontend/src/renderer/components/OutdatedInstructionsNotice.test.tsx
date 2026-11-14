import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { CloudCpError } from "../lib/cloud-cp/errors";
import type { CloudCpSession } from "../lib/cloud-cp/types";
import { toCloudWorkspaceSession } from "../hooks/useWorkspaceQuery";
import { orchestratorHealth, type WorkspaceSession } from "../types/workspace";
import { OutdatedInstructionsNotice } from "./OutdatedInstructionsNotice";

const mocks = vi.hoisted(() => ({ restart: vi.fn() }));
vi.mock("../hooks/useCloudCp", () => ({
	useCloudCp: () => ({ client: { restartSessionAgent: mocks.restart }, ready: true, baseUrl: "https://cloud.test" }),
}));

function cloudSession(overrides: Partial<CloudCpSession> = {}): CloudCpSession {
	return {
		id: "s-1", orgId: "org-1", projectId: "p-1", kind: "orchestrator", harness: "claude-code",
		displayName: "Orchestrator", branch: "ao/s-1", mode: "trusted", interfaceMode: "tui", deniedCommands: [],
		activityState: "idle", status: "idle", runtimeConnected: true, isTerminated: false, prs: [],
		createdAt: "2026-11-10T00:00:00Z", updatedAt: "2026-11-10T00:00:00Z",
		...overrides,
	};
}

function session(overrides: Partial<CloudCpSession> = {}): WorkspaceSession {
	return toCloudWorkspaceSession(cloudSession(overrides), { id: "p-1", displayName: "Project" }, "org-1");
}

function renderNotice(subject: WorkspaceSession, variant: "board" | "terminal" = "board") {
	const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
	return render(<QueryClientProvider client={client}><OutdatedInstructionsNotice session={subject} variant={variant} /></QueryClientProvider>);
}

beforeEach(() => {
	mocks.restart.mockReset().mockResolvedValue({ requested: true });
});

describe("outdated project instructions", () => {
	it("maps the control plane's outdatedInstructions onto the session, and nothing for an older control plane", () => {
		expect(session({ outdatedInstructions: "restart" }).cloud?.outdatedInstructions).toBe("restart");
		expect(session().cloud).not.toHaveProperty("outdatedInstructions");
	});

	it("puts the orchestrator's changed instructions on the board, in place of the replacement restart", () => {
		const orchestrator = session({ outdatedInstructions: "restart" });
		const health = orchestratorHealth({ id: "p-1", name: "Project", path: "", kind: "cloud", sessions: [orchestrator] });
		expect(health).toEqual({ state: "instructions_changed", apply: "restart", orchestrator });
		expect(orchestratorHealth({ id: "p-1", name: "Project", path: "", kind: "cloud", sessions: [session()] })).toEqual({ state: "ok" });
	});

	it("restarts the agent in place, keeping its conversation, to apply them", async () => {
		renderNotice(session({ outdatedInstructions: "restart" }));
		expect(screen.getByRole("status")).toHaveTextContent(
			"Orchestrator instructions changed after this agent started. Restart the agent to apply them. The conversation is kept.",
		);
		await userEvent.click(screen.getByRole("button", { name: "Restart agent" }));
		await waitFor(() => expect(mocks.restart).toHaveBeenCalledWith("org-1", "s-1", { applyInstructions: true }));
	});

	it("waits for a working agent to finish its turn", () => {
		renderNotice(session({ kind: "worker", outdatedInstructions: "restart", activityState: "active" }), "terminal");
		expect(screen.getByRole("status")).toHaveTextContent(
			"Worker instructions changed after this agent started. Restart the agent once it finishes its turn to apply them.",
		);
		expect(screen.getByRole("button", { name: "Restart agent" })).toBeDisabled();
	});

	it("says when a worker that cannot restart in place applies them, without a restart", () => {
		renderNotice(session({ kind: "worker", outdatedInstructions: "next_start" }), "terminal");
		expect(screen.getByRole("status")).toHaveTextContent(
			"This session's worker can't apply them while it runs. They reach the agent only after the session is paused and resumed.",
		);
		expect(screen.queryByRole("button")).toBeNull();
	});

	it.each(["waiting_input", "blocked"])("waits for an agent that is %s, rather than ending what it waits on", (activityState) => {
		renderNotice(session({ kind: "worker", outdatedInstructions: "restart", activityState }), "terminal");
		expect(screen.getByRole("status")).toHaveTextContent(
			"Worker instructions changed after this agent started. Answer what the agent is waiting on, then restart it to apply them.",
		);
		expect(screen.getByRole("button", { name: "Restart agent" })).toBeDisabled();
	});

	it("restarts once when both the board and the terminal offer it, and shows it on both", async () => {
		let finish: (value: { requested: boolean }) => void = () => {};
		mocks.restart.mockReturnValue(new Promise((resolve) => { finish = resolve; }));
		const subject = session({ outdatedInstructions: "restart" });
		const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
		render(
			<QueryClientProvider client={client}>
				<OutdatedInstructionsNotice session={subject} variant="board" />
				<OutdatedInstructionsNotice session={subject} variant="terminal" />
			</QueryClientProvider>,
		);
		const [board, terminal] = screen.getAllByRole("button", { name: "Restart agent" });
		fireEvent.click(board);
		fireEvent.click(board);
		fireEvent.click(terminal);
		await waitFor(() => expect(terminal).toBeDisabled());
		expect(board).toBeDisabled();
		expect(mocks.restart).toHaveBeenCalledOnce();
		finish({ requested: false });
		expect(await screen.findAllByText("The agent did not restart. Try again in a moment.")).toHaveLength(2);
	});

	it("says a restart the control plane stopped waiting for may still complete, and reads the session again", async () => {
		mocks.restart.mockRejectedValue(new CloudCpError("The worker did not complete the request in time.", { status: 504, code: "WORKER_TIMEOUT" }));
		const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
		const invalidate = vi.spyOn(client, "invalidateQueries");
		vi.useFakeTimers({ shouldAdvanceTime: true });
		try {
			render(<QueryClientProvider client={client}><OutdatedInstructionsNotice session={session({ outdatedInstructions: "restart" })} variant="board" /></QueryClientProvider>);
			fireEvent.click(screen.getByRole("button", { name: "Restart agent" }));
			expect(await screen.findByText(
				"The restart is taking longer than expected. The agent may still be restarting; this notice clears once it runs with them.",
			)).toBeInTheDocument();
			expect(screen.queryByText("The worker did not complete the request in time.")).toBeNull();
			const settled = invalidate.mock.calls.length;
			await vi.advanceTimersByTimeAsync(10_000);
			expect(invalidate.mock.calls.length).toBeGreaterThan(settled);
		} finally {
			vi.useRealTimers();
		}
	});

	it("says when the agent did not restart", async () => {
		mocks.restart.mockResolvedValue({ requested: false });
		renderNotice(session({ outdatedInstructions: "restart" }));
		await userEvent.click(screen.getByRole("button", { name: "Restart agent" }));
		expect(await screen.findByText("The agent did not restart. Try again in a moment.")).toBeInTheDocument();
	});

	it("shows nothing for a session running the current instructions", () => {
		const { container } = renderNotice(session());
		expect(container).toBeEmptyDOMElement();
	});
});
