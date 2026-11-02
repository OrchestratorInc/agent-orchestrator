import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { CloudCpClientEvent, CloudCpSessionLive, CloudCpSessionViewer } from "../../lib/cloud-cp";
import type { ComposerLeaseState } from "../../hooks/useComposerLease";
import type { WorkspaceSession } from "../../types/workspace";
import { CloudSessionChatSurface, fromSessionChatSettings, toSessionChatSettings, toSnapshot } from "./CloudSessionChatSurface";
import { RemoteDraft, SharedComposerNotice } from "./SharedComposerNotice";

const mocks = vi.hoisted(() => ({
	live: undefined as CloudCpSessionLive | undefined,
	listChatEvents: vi.fn(),
	sendSessionMessage: vi.fn(),
	steerTurn: vi.fn(),
	listChatModels: vi.fn(),
	resumeSession: vi.fn(),
	acquireComposer: vi.fn(),
	renewComposer: vi.fn(),
	releaseComposer: vi.fn(),
	requestComposerTurn: vi.fn(),
	leaveComposerQueue: vi.fn(),
	takeOverComposer: vi.fn(),
	setSessionChatSettings: vi.fn(),
	chatProps: vi.fn(),
}));
vi.mock("../../hooks/useCloudCp", () => ({
	useCloudCp: () => ({ ready: true, client: mocks, userId: "user-me", baseUrl: "https://cp.test" }),
}));
vi.mock("../../hooks/useCloudSessionLive", () => ({
	useCloudSessionLive: () => mocks.live,
}));
vi.mock("./ChatWorkspace", () => ({
	ChatWorkspace: (props: unknown) => {
		mocks.chatProps(props);
		return <div data-testid="cloud-chat" />;
	},
}));

const session = {
	id: "session-1",
	workspaceId: "project-1",
	workspaceName: "project",
	title: "Cloud session",
	provider: "codex",
	kind: "worker",
	mode: "chat",
	status: "working",
	updatedAt: "2026-10-22T00:00:00Z",
	prs: [],
	cloud: { orgId: "org-1", permissionMode: "standard" },
} satisfies WorkspaceSession;

const me = { userId: "user-me", name: "Me", role: "member" as const };
const alice = { userId: "user-alice", name: "Alice", role: "editor" as const };

function liveState(
	composer: Partial<CloudCpSessionLive["composer"]> = {}, viewers: CloudCpSessionViewer[] = [me, alice], canTakeOver = false, you = "user-me",
	chatSettings: CloudCpSessionLive["chatSettings"] = {}, canConfigure = canTakeOver,
): CloudCpSessionLive {
	return { type: "session.live", viewers, composer: { holder: null, epoch: 0, waitlist: [], ...composer }, canTakeOver, you, chatSettings, canConfigure };
}

function renderSurface(shown: WorkspaceSession = session) {
	const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
	return render(
		<QueryClientProvider client={queryClient}>
			<CloudSessionChatSurface session={shown} />
		</QueryClientProvider>,
	);
}

const lastProps = () => mocks.chatProps.mock.lastCall?.[0] as {
	onSend: (text: string, attachments: unknown[], clientMessageId: string) => Promise<unknown>;
	onComposerInput?: (text: string) => void;
	composerLockedPlaceholder?: string;
	composerNotice?: unknown;
};

describe("shared session chat", () => {
	beforeEach(() => {
		vi.clearAllMocks();
		mocks.live = undefined;
		mocks.listChatEvents.mockResolvedValue({ events: [], hasMore: false, nextAfter: 0 });
		mocks.listChatModels.mockResolvedValue({ models: [] });
		mocks.resumeSession.mockResolvedValue(undefined);
		mocks.sendSessionMessage.mockResolvedValue({ event: {} });
	});

	it("names the author of another participant's prompt, never the viewer's own or an automation's", () => {
		const event = (sequence: number, payload: Record<string, unknown>): CloudCpClientEvent => ({
			sessionId: session.id, sequence, type: "chat.user_message", payload, createdAt: "2026-10-22T00:00:00Z",
		});
		const items = toSnapshot(session, [
			event(1, { text: "from alice", authorUserId: "user-alice", authorName: "Alice" }),
			event(2, { text: "from me", authorUserId: "user-me", authorName: "Me" }),
			event(3, { text: "report", origin: "automation", senderLabel: "CI", authorUserId: "user-alice", authorName: "Alice" }),
			event(4, { text: "old message" }),
		], "user-me").items;
		expect(items.map((item) => (item as { authorName?: string }).authorName)).toEqual(["Alice", undefined, undefined, undefined]);
		// Without a known viewer nothing is attributed.
		expect((toSnapshot(session, [event(1, { text: "x", authorUserId: "user-alice", authorName: "Alice" })]).items[0] as { authorName?: string }).authorName).toBeUndefined();
	});

	it("sends as before, with no grant, while nobody else is in the session", async () => {
		mocks.live = liveState({}, [me]);
		renderSurface();
		await waitFor(() => expect(lastProps()).toBeDefined());
		lastProps().onComposerInput?.("hello");
		await lastProps().onSend("hello", [], "m-1");
		expect(mocks.acquireComposer).not.toHaveBeenCalled();
		expect(mocks.sendSessionMessage).toHaveBeenLastCalledWith("org-1", session.id, expect.not.objectContaining({ composerEpoch: expect.anything() }), { idempotencyKey: "m-1" });
		expect(lastProps().composerLockedPlaceholder).toBeUndefined();
	});

	it("sends with the held grant in a shared session", async () => {
		mocks.live = liveState({ holder: { userId: "user-me", name: "Me" }, epoch: 12 });
		renderSurface();
		await waitFor(() => expect(lastProps()).toBeDefined());
		await lastProps().onSend("go", [], "m-2");
		expect(mocks.sendSessionMessage).toHaveBeenLastCalledWith("org-1", session.id, expect.objectContaining({ text: "go", composerEpoch: 12 }), { idempotencyKey: "m-2" });
	});

	it("locks the chat box while someone else is writing to the agent", async () => {
		mocks.live = liveState({ holder: { userId: "user-alice", name: "Alice" }, epoch: 3 });
		renderSurface();
		await waitFor(() => expect(lastProps()?.composerLockedPlaceholder).toBe(""));
	});

	it("trusts the server's word on who this person is over the account the app remembers", async () => {
		// The app remembers user-me, but the stream is signed in as Alice:
		// Alice's own prompt is not labelled, and her lease does not lock her out.
		mocks.listChatEvents.mockResolvedValue({
			events: [
				{ sessionId: session.id, sequence: 1, type: "chat.user_message", payload: { text: "mine", authorUserId: "user-alice", authorName: "Alice" }, createdAt: "2026-10-22T00:00:00Z" },
				{ sessionId: session.id, sequence: 2, type: "chat.user_message", payload: { text: "theirs", authorUserId: "user-me", authorName: "Me" }, createdAt: "2026-10-22T00:00:01Z" },
			],
			hasMore: false, nextAfter: 2,
		});
		mocks.live = liveState({ holder: { userId: "user-alice", name: "Alice" }, epoch: 5 }, [me, alice], false, "user-alice");
		renderSurface();
		await waitFor(() => {
			const items = (mocks.chatProps.mock.lastCall?.[0] as { snapshot: { items: Array<{ text?: string; authorName?: string }> } }).snapshot.items;
			expect(items.map((item) => item.authorName)).toEqual([undefined, "Me"]);
		});
		expect(lastProps().composerLockedPlaceholder).toBeUndefined();
	});

	it("shows a guest the owner's model and settings, read-only", async () => {
		mocks.live = liveState({}, [me, alice], false, "user-me", { model: "opus", reasoningEffort: "high", mode: "read-only" }, false);
		localStorage.setItem(`cloud-chat-settings:org-1:${session.id}:codex`, JSON.stringify({ model: "guest-pick" }));
		renderSurface();
		await waitFor(() => expect(lastProps()).toBeDefined());
		const props = mocks.chatProps.mock.lastCall?.[0] as { settingsLocked?: boolean; snapshot: { settings?: { model?: string; reasoningEffort?: string; executionMode?: string } }; onChooseSettings: (next: object, picked?: string) => void };
		expect(props.settingsLocked).toBe(true);
		expect(props.snapshot.settings).toMatchObject({ model: "opus", reasoningEffort: "high", executionMode: "plan" });
		props.onChooseSettings({ model: "haiku" }, "model");
		expect(mocks.setSessionChatSettings).not.toHaveBeenCalled();
	});

	it("makes the owner's choice the session's, on opening it and on every change", async () => {
		localStorage.setItem(`cloud-chat-settings:org-1:${session.id}:codex`, JSON.stringify({ model: "sonnet", reasoningEffort: "medium" }));
		mocks.setSessionChatSettings.mockResolvedValue({ chatSettings: {} });
		mocks.live = liveState({}, [me, alice], true, "user-me", {}, true);
		renderSurface();
		await waitFor(() => expect(mocks.setSessionChatSettings).toHaveBeenCalledWith("org-1", session.id, { model: "sonnet", reasoningEffort: "medium" }));
		const props = mocks.chatProps.mock.lastCall?.[0] as { settingsLocked?: boolean; onChooseSettings: (next: object, picked?: string) => void };
		expect(props.settingsLocked).toBe(false);
		props.onChooseSettings({ model: "opus", reasoningEffort: "high" }, "model");
		await waitFor(() => expect(mocks.setSessionChatSettings).toHaveBeenLastCalledWith("org-1", session.id, { model: "opus", reasoningEffort: "high" }));
	});

	it("shares the worker's model with the session when it replaces the owner's choice", async () => {
		mocks.setSessionChatSettings.mockResolvedValue({ chatSettings: {} });
		mocks.listChatModels.mockResolvedValue({ models: [{ id: "gpt-5.5", displayName: "GPT-5.5" }], model: "gpt-5.5", reasoningEffort: "high" });
		mocks.live = liveState({}, [me, alice], true, "user-me", {}, true);
		renderSurface({ ...session, runtimeConnected: true });
		await waitFor(() => expect(mocks.setSessionChatSettings).toHaveBeenLastCalledWith("org-1", session.id, { model: "gpt-5.5", reasoningEffort: "high" }));
	});

	it("never shares the worker's model from a guest's window", async () => {
		mocks.listChatModels.mockResolvedValue({ models: [{ id: "gpt-5.5", displayName: "GPT-5.5" }], model: "gpt-5.5", reasoningEffort: "high" });
		mocks.live = liveState({}, [me, alice], false, "user-me", { model: "opus" }, false);
		renderSurface({ ...session, runtimeConnected: true });
		await waitFor(() => expect(mocks.listChatModels).toHaveBeenCalled());
		await waitFor(() => expect((mocks.chatProps.mock.lastCall?.[0] as { snapshot: { settings?: { model?: string } } }).snapshot.settings?.model).toBe("opus"));
		expect(mocks.setSessionChatSettings).not.toHaveBeenCalled();
	});

	it("locks the chat box for a read-only participant", async () => {
		mocks.live = liveState({}, [{ ...me, role: "viewer" }, alice]);
		renderSurface();
		await waitFor(() => expect(lastProps()?.composerLockedPlaceholder).toBe("You can view this session but not write to the agent"));
	});

	it("keeps a guest's chat box locked until the server says what this session lets them do", async () => {
		const guestSession = { ...session, cloud: { ...session.cloud, shared: true } };
		mocks.live = undefined;
		const { rerender } = renderSurface(guestSession);
		await waitFor(() => expect(lastProps()?.composerLockedPlaceholder).toBe("Checking what this session lets you do…"));
		mocks.live = liveState({}, [{ ...me, role: "editor" }, alice]);
		const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
		rerender(<QueryClientProvider client={queryClient}><CloudSessionChatSurface session={guestSession} /></QueryClientProvider>);
		await waitFor(() => expect(lastProps()?.composerLockedPlaceholder).toBeUndefined());
	});

	it("leaves the person's own session open before its live state arrives", async () => {
		mocks.live = undefined;
		renderSurface();
		await waitFor(() => expect(lastProps()).toBeDefined());
		expect(lastProps()?.composerLockedPlaceholder).toBeUndefined();
	});
});

function lease(overrides: Partial<ComposerLeaseState> = {}): ComposerLeaseState {
	return {
		shared: true, holding: false, locked: false, canTakeOver: false,
		noteDraft: vi.fn(), requestTurn: vi.fn(), leaveLine: vi.fn(), takeOver: vi.fn(), sent: vi.fn(),
		...overrides,
	};
}

describe("SharedComposerNotice", () => {
	it("shows nothing while nobody else has the session open", () => {
		const { container } = render(<SharedComposerNotice live={liveState({}, [me])} lease={lease({ shared: false })} userId="user-me" />);
		expect(container.innerHTML).toBe("");
	});

	it("lists who else is here while the composer is free", () => {
		render(<SharedComposerNotice live={liveState()} lease={lease()} userId="user-me" />);
		expect(screen.getByRole("status").textContent).toContain("Also here: Alice");
		expect(screen.queryByRole("button")).toBeNull();
	});

	it("offers a turn, and a take-over to those who may, while someone else writes", () => {
		const state = lease({ locked: true, holder: { userId: "user-alice", name: "Alice" }, canTakeOver: true });
		render(<SharedComposerNotice live={liveState({ holder: { userId: "user-alice", name: "Alice" }, epoch: 3 })} lease={state} userId="user-me" />);
		expect(screen.getByRole("status").textContent).toContain("Alice is writing to the agent");
		fireEvent.click(screen.getByRole("button", { name: "Request turn" }));
		fireEvent.click(screen.getByRole("button", { name: "Take over" }));
		expect(state.requestTurn).toHaveBeenCalled();
		expect(state.takeOver).toHaveBeenCalled();
	});

	it("shows the person's place in line and lets them leave it", () => {
		const state = lease({ locked: true, holder: { userId: "user-alice", name: "Alice" }, position: 2 });
		render(<SharedComposerNotice live={liveState({ holder: { userId: "user-alice", name: "Alice" }, epoch: 3, waitlist: [{ userId: "user-bob", name: "Bob" }, { userId: "user-me", name: "Me" }] })} lease={state} userId="user-me" />);
		expect(screen.getByRole("status").textContent).toContain("You're number 2 in line");
		expect(screen.queryByRole("button", { name: "Request turn" })).toBeNull();
		fireEvent.click(screen.getByRole("button", { name: "Leave line" }));
		expect(state.leaveLine).toHaveBeenCalled();
	});

	it("tells the holder it is their turn and who is waiting", () => {
		render(<SharedComposerNotice live={liveState({ holder: { userId: "user-me", name: "Me" }, epoch: 4, waitlist: [{ userId: "user-alice", name: "Alice" }] })} lease={lease({ holding: true, epoch: 4 })} userId="user-me" />);
		const status = screen.getByRole("status").textContent ?? "";
		expect(status).toContain("Your turn to write to the agent");
		expect(status).toContain("Waiting: Alice");
	});
});

describe("RemoteDraft", () => {
	it("shows the writer's text read-only with their caret and name where they type", () => {
		render(<RemoteDraft text="fix the flaky test" cursor={7} name="Bob" />);
		const draft = screen.getByRole("region", { name: "Bob's draft" });
		expect(draft.textContent).toBe("fix theBob flaky test");
		const caret = draft.querySelector("[data-remote-caret]");
		expect(caret?.previousSibling?.textContent ?? draft.firstChild?.textContent).toBe("fix the");
		expect(draft.querySelector("input, textarea, [contenteditable]")).toBeNull();
	});

	it("keeps a caret past the end at the end", () => {
		render(<RemoteDraft text="hi" cursor={99} name="Bob" />);
		expect(screen.getByRole("region").textContent).toBe("hiBob");
	});

	it("appears inside the strip while someone else holds the lease and has typed", () => {
		const state = lease({ locked: true, holder: { userId: "user-alice", name: "Alice" }, remoteDraft: { holder: { userId: "user-alice", name: "Alice" }, text: "deploy it", cursor: 9 } });
		render(<SharedComposerNotice live={liveState({ holder: { userId: "user-alice", name: "Alice" }, epoch: 3 })} lease={state} userId="user-me" />);
		expect(screen.getByRole("region", { name: "Alice's draft" }).textContent).toBe("deploy itAlice");
	});
});

describe("session chat settings mapping", () => {
	it("round-trips the composer's settings, plan mode as a read-only turn", () => {
		const settings = { model: "opus", reasoningEffort: "high", approvalMode: "auto" as const, executionMode: "plan" as const };
		expect(toSessionChatSettings(settings)).toEqual({ model: "opus", reasoningEffort: "high", approvalMode: "auto", mode: "read-only" });
		expect(fromSessionChatSettings(toSessionChatSettings(settings))).toEqual(settings);
		expect(toSessionChatSettings({ executionMode: "agent" })).toEqual({});
		expect(fromSessionChatSettings(undefined)).toEqual({});
	});
});
