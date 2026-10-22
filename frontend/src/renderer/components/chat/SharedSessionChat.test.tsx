import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { CloudCpClientEvent, CloudCpSessionLive, CloudCpSessionViewer } from "../../lib/cloud-cp";
import type { ComposerLeaseState } from "../../hooks/useComposerLease";
import type { WorkspaceSession } from "../../types/workspace";
import { CloudSessionChatSurface, toSnapshot } from "./CloudSessionChatSurface";
import { SharedComposerNotice } from "./SharedComposerNotice";

const mocks = vi.hoisted(() => ({
	live: undefined as CloudCpSessionLive | undefined,
	listChatEvents: vi.fn(),
	sendSessionMessage: vi.fn(),
	steerTurn: vi.fn(),
	listChatModels: vi.fn(),
	acquireComposer: vi.fn(),
	renewComposer: vi.fn(),
	releaseComposer: vi.fn(),
	requestComposerTurn: vi.fn(),
	leaveComposerQueue: vi.fn(),
	takeOverComposer: vi.fn(),
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

function liveState(composer: Partial<CloudCpSessionLive["composer"]> = {}, viewers: CloudCpSessionViewer[] = [me, alice], canTakeOver = false): CloudCpSessionLive {
	return { type: "session.live", viewers, composer: { holder: null, epoch: 0, waitlist: [], ...composer }, canTakeOver };
}

function renderSurface() {
	const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
	return render(
		<QueryClientProvider client={queryClient}>
			<CloudSessionChatSurface session={session} />
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
		await waitFor(() => expect(lastProps()?.composerLockedPlaceholder).toBe("Alice is writing to the agent"));
	});

	it("locks the chat box for a read-only participant", async () => {
		mocks.live = liveState({}, [{ ...me, role: "viewer" }, alice]);
		renderSurface();
		await waitFor(() => expect(lastProps()?.composerLockedPlaceholder).toBe("You can view this session but not write to the agent"));
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
