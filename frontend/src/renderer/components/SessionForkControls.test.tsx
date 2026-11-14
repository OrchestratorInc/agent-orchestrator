import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, fireEvent, render, renderHook, screen, waitFor } from "@testing-library/react";
import { memo, Profiler, type ReactNode } from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { useUiStore } from "../stores/ui-store";
import type { WorkspaceSession } from "../types/workspace";
import { ForkableTurnContext, ForkSessionContext, ForkTextCopyNotice, LatestForkTurnContext, TurnForkButton, useForkSessionFromMenu } from "./SessionForkControls";
import { useSessionMenuEntries } from "./SessionContextMenuItems";
import { TooltipProvider } from "./ui/tooltip";

const { getMock, postMock, navigateMock, remote } = vi.hoisted(() => ({
	getMock: vi.fn(),
	postMock: vi.fn(),
	navigateMock: vi.fn(),
	// The remote "flaky" host, which a test connects and disconnects.
	remote: { online: false, listeners: new Set<() => void>(), host: { hostId: "flaky", label: "flaky", url: "https://flaky", base: "https://flaky/api" } },
}));

vi.mock("../lib/host-clients", async (importOriginal) => ({
	...(await importOriginal<typeof import("../lib/host-clients")>()),
	clientForSessionHost: (hostId?: string) => {
		// As the real helper does for a remote host that has disconnected.
		if (hostId === "gone" || (hostId === "flaky" && !remote.online)) throw new Error(`host ${hostId} is not connected`);
		return { GET: getMock, POST: postMock };
	},
	connectedHost: (hostId: string) => (hostId === "flaky" && remote.online ? remote.host : undefined),
	subscribeConnectedHosts: (listener: () => void) => {
		remote.listeners.add(listener);
		return () => remote.listeners.delete(listener);
	},
}));
vi.mock("@tanstack/react-router", async (importOriginal) => ({
	...(await importOriginal<typeof import("@tanstack/react-router")>()),
	useNavigate: () => navigateMock,
}));

const session = { id: "p-1", workspaceId: "p", title: "Fix login", status: "idle" } as unknown as WorkspaceSession;

/** Fork on a session's latest finished turn. */
function latestTurnFork(s: WorkspaceSession) {
	return (
		<ForkSessionContext.Provider value={s}>
			<LatestForkTurnContext.Provider value="t-4">
				<ForkableTurnContext.Provider value="t-4"><TurnForkButton /></ForkableTurnContext.Provider>
			</LatestForkTurnContext.Provider>
		</ForkSessionContext.Provider>
	);
}

function renderControls(ui: ReactNode = latestTurnFork(session)) {
	const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
	return render(
		<QueryClientProvider client={client}>
			<TooltipProvider>{ui}</TooltipProvider>
		</QueryClientProvider>,
	);
}

describe("session fork controls", () => {
	beforeEach(() => {
		getMock.mockResolvedValue({ data: { allowed: true, suggestedName: "Fork of Fix login" } });
		postMock.mockResolvedValue({ data: { sessionId: "p-9", displayName: "Fork of Fix login" } });
		useUiStore.setState({ developerMode: true });
	});
	afterEach(() => {
		vi.clearAllMocks();
		useUiStore.setState({ developerMode: false });
	});

	it("shows without developer mode", async () => {
		useUiStore.setState({ developerMode: false });
		renderControls();
		expect(await screen.findByRole("button", { name: "Fork" })).toBeInTheDocument();
	});

	it("forks in one click and opens the new session", async () => {
		renderControls();
		const button = await screen.findByRole("button", { name: "Fork" });
		await waitFor(() => expect(button).toBeEnabled());
		fireEvent.click(button);
		await waitFor(() => expect(navigateMock).toHaveBeenCalled());
		expect(postMock).toHaveBeenCalledWith("/api/v1/sessions/{sessionId}/fork", expect.objectContaining({
			params: { path: { sessionId: "p-1" } },
		}));
		expect(JSON.stringify(navigateMock.mock.calls[0][0])).toContain("p-9");
	});

	it("sends one request across a double click", async () => {
		let resolve: (value: unknown) => void = () => {};
		postMock.mockReturnValue(new Promise((r) => { resolve = r; }));
		renderControls();
		const button = await screen.findByRole("button", { name: "Fork" });
		await waitFor(() => expect(button).toBeEnabled());
		fireEvent.click(button);
		fireEvent.click(button);
		await waitFor(() => expect(postMock).toHaveBeenCalledTimes(1));
		resolve({ data: { sessionId: "p-9", displayName: "Fork of Fix login" } });
		await waitFor(() => expect(navigateMock).toHaveBeenCalledTimes(1));
		expect(postMock).toHaveBeenCalledTimes(1);
	});

	it("is disabled with the refusal message", async () => {
		getMock.mockResolvedValue({ data: { allowed: false, refusal: { code: "FORK_SOURCE_BUSY", message: "Wait for the agent to go idle to fork this session." } } });
		renderControls();
		const button = await screen.findByRole("button", { name: "Fork" });
		await waitFor(() => expect(button).toHaveAttribute("aria-disabled", "true"));
		expect(button).toHaveAttribute("title", "Wait for the agent to go idle to fork this session.");
		fireEvent.click(button);
		expect(postMock).not.toHaveBeenCalled();
	});

	it("forks from the latest turn's action row in a chat", async () => {
		const chat = { ...session, mode: "chat" } as WorkspaceSession;
		renderControls(
			<ForkSessionContext.Provider value={chat}>
				<LatestForkTurnContext.Provider value="t-4">
					<ForkableTurnContext.Provider value="t-4"><TurnForkButton /></ForkableTurnContext.Provider>
					<ForkableTurnContext.Provider value="t-3"><TurnForkButton /></ForkableTurnContext.Provider>
					<ForkableTurnContext.Provider value={null}><TurnForkButton /></ForkableTurnContext.Provider>
				</LatestForkTurnContext.Provider>
			</ForkSessionContext.Provider>,
		);
		const buttons = await screen.findAllByRole("button", { name: "Fork" });
		// One on the latest turn; none on older turns.
		expect(buttons).toHaveLength(1);
		expect(buttons[0]).toHaveAttribute("data-testid", "fork-turn");
		await waitFor(() => expect(buttons[0]).toHaveAttribute("aria-disabled", "false"));
		fireEvent.click(buttons[0]);
		await waitFor(() => expect(navigateMock).toHaveBeenCalled());
	});

	it("asks again once a new fork finishes setting up", async () => {
		const preparing = { ...session, provisionState: "provisioning" } as WorkspaceSession;
		const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
		const view = (s: WorkspaceSession) => (
			<QueryClientProvider client={client}><TooltipProvider>{latestTurnFork(s)}</TooltipProvider></QueryClientProvider>
		);
		const { rerender } = render(view(preparing));
		await waitFor(() => expect(getMock).toHaveBeenCalledTimes(1));
		rerender(view({ ...preparing, provisionState: "ready" } as WorkspaceSession));
		await waitFor(() => expect(getMock).toHaveBeenCalledTimes(2));
	});

	it("forks an older turn where the agent can", async () => {
		getMock.mockResolvedValue({ data: { allowed: true, anyTurn: true } });
		const chat = { ...session, mode: "chat" } as WorkspaceSession;
		renderControls(
			<ForkSessionContext.Provider value={chat}>
				<LatestForkTurnContext.Provider value="t-4">
					<ForkableTurnContext.Provider value="t-2"><TurnForkButton /></ForkableTurnContext.Provider>
				</LatestForkTurnContext.Provider>
			</ForkSessionContext.Provider>,
		);
		const button = await screen.findByRole("button", { name: "Fork" });
		await waitFor(() => expect(button).toHaveAttribute("aria-disabled", "false"));
		fireEvent.click(button);
		await waitFor(() => expect(postMock).toHaveBeenCalled());
		expect(postMock.mock.calls[0][1].body).toMatchObject({ turnId: "t-2" });
	});

	it("forks from the session menu and opens the fork", async () => {
		const client = new QueryClient();
		const { result } = renderHook(() => useForkSessionFromMenu(), {
			wrapper: ({ children }) => <QueryClientProvider client={client}>{children}</QueryClientProvider>,
		});
		result.current({ ...session, mode: "tui" } as WorkspaceSession);
		await waitFor(() => expect(navigateMock).toHaveBeenCalled());
		expect(JSON.stringify(navigateMock.mock.calls[0][0])).toContain("p-9");
	});

	it("shows a refusal from the session menu as a toast", async () => {
		postMock.mockResolvedValue({ error: { code: "FORK_SOURCE_BUSY", message: "Wait for the agent to go idle to fork this session." } });
		const toast = vi.fn();
		useUiStore.setState({ showGlobalToast: toast } as never);
		const client = new QueryClient();
		const { result } = renderHook(() => useForkSessionFromMenu(), {
			wrapper: ({ children }) => <QueryClientProvider client={client}>{children}</QueryClientProvider>,
		});
		result.current(session);
		await waitFor(() => expect(toast).toHaveBeenCalled());
		expect(String(toast.mock.calls[0][1])).toContain("Wait for the agent to go idle");
		expect(navigateMock).not.toHaveBeenCalled();
	});

	it("says a fork's agent was given its conversation as text", async () => {
		getMock.mockResolvedValue({ data: { allowed: true, forkedFrom: { sessionId: "p-0", title: "Fix login", sourceExists: true, textHandoff: true } } });
		renderControls(<ForkSessionContext.Provider value={session}><ForkTextCopyNotice /></ForkSessionContext.Provider>);
		expect(await screen.findByTestId("fork-text-copy-notice")).toHaveTextContent("Forked from Fix login. This agent can't copy its own conversation");
	});

	it("says nothing for a fork that copied its conversation natively", async () => {
		getMock.mockResolvedValue({ data: { allowed: true, forkedFrom: { sessionId: "p-0", title: "Fix login", sourceExists: true } } });
		renderControls(<ForkSessionContext.Provider value={session}><ForkTextCopyNotice /></ForkSessionContext.Provider>);
		await waitFor(() => expect(getMock).toHaveBeenCalled());
		expect(screen.queryByTestId("fork-text-copy-notice")).toBeNull();
	});

	it("greys out the menu's Fork session with the reason when forking isn't possible", async () => {
		getMock.mockResolvedValue({ data: { allowed: false, refusal: { code: "FORK_ORCHESTRATOR", message: "Orchestrators can't be forked." } } });
		const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
		const remote = { ...session, hostId: "host-1", prs: [] } as unknown as WorkspaceSession;
		const { result } = renderHook(() => useSessionMenuEntries(remote), {
			wrapper: ({ children }) => <QueryClientProvider client={client}>{children}</QueryClientProvider>,
		});
		await waitFor(() => {
			const fork = result.current.find((entry) => entry.id === "session-fork");
			expect(fork).toMatchObject({ disabled: true, description: "Orchestrators can't be forked." });
		});
	});

	it("leaves the menu's Fork session on where forking is possible", async () => {
		const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
		const remote = { ...session, hostId: "host-1", prs: [] } as unknown as WorkspaceSession;
		const { result } = renderHook(() => useSessionMenuEntries(remote), {
			wrapper: ({ children }) => <QueryClientProvider client={client}>{children}</QueryClientProvider>,
		});
		await waitFor(() => expect(getMock).toHaveBeenCalled());
		await waitFor(() => expect(result.current.find((entry) => entry.id === "session-fork")).toMatchObject({ disabled: false }));
	});

	it("doesn't re-render a turn's Fork button when the timeline re-renders with the same turn", async () => {
		let renders = 0;
		const Button = memo(function Button() {
			return (
				<Profiler id="fork" onRender={() => { renders++; }}>
					<TurnForkButton />
				</Profiler>
			);
		});
		const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
		// The timeline re-renders on every streaming tick, recreating its providers.
		const timeline = () => (
			<QueryClientProvider client={client}>
				<TooltipProvider>
					<ForkSessionContext.Provider value={session}>
						<LatestForkTurnContext.Provider value={"t-" + 4}>
							<ForkableTurnContext.Provider value={"t-" + 4}><Button /></ForkableTurnContext.Provider>
						</LatestForkTurnContext.Provider>
					</ForkSessionContext.Provider>
				</TooltipProvider>
			</QueryClientProvider>
		);
		const { rerender } = render(timeline());
		await screen.findByRole("button", { name: "Fork" });
		await waitFor(() => expect(getMock).toHaveBeenCalled());
		await act(async () => {});
		const settled = renders;
		rerender(timeline());
		rerender(timeline());
		expect(renders).toBe(settled);
	});

	it("still renders for a session on a remote host that has disconnected", async () => {
		const offline = { ...session, hostId: "gone" } as WorkspaceSession;
		renderControls(latestTurnFork(offline));
		const button = await screen.findByRole("button", { name: "Fork" });
		await waitFor(() => expect(button).toHaveAttribute("aria-disabled", "true"));
		expect(getMock).not.toHaveBeenCalled();
	});

	it("asks again when a disconnected remote host reconnects", async () => {
		remote.online = false;
		const onRemote = { ...session, hostId: "flaky" } as WorkspaceSession;
		renderControls(latestTurnFork(onRemote));
		const button = await screen.findByRole("button", { name: "Fork" });
		await waitFor(() => expect(button).toHaveAttribute("aria-disabled", "true"));
		act(() => {
			remote.online = true;
			for (const listener of remote.listeners) listener();
		});
		await waitFor(() => expect(button).toHaveAttribute("aria-disabled", "false"));
		expect(getMock).toHaveBeenCalledTimes(1);
	});
});
