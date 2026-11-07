import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import type { ReactNode } from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { promptSuggestionMessages } from "../../hooks/useCloudPromptSuggestion";
import type { CloudCpAsideSuggestion, CloudCpClientEvent, CloudCpSessionLive, CloudCpSessionViewer } from "../../lib/cloud-cp";
import { recordTeamCollabSupport, resetTeamCollabSupport, type ComposerMention } from "../../lib/cloud-team-collab";
import { useUiStore } from "../../stores/ui-store";
import type { ConversationActivity, ConversationSnapshot } from "../../types/conversation";
import type { WorkspaceSession } from "../../types/workspace";
import { TooltipProvider } from "../ui/tooltip";
import { AsideRow } from "./AsideRow";
import type { ComposerMentionSource } from "./ChatComposer";
import { CloudSessionChatSurface, cloudExcerptText, lastChosenChatSettings, toSnapshot } from "./CloudSessionChatSurface";

const BASE = "https://cp.test";
const mocks = vi.hoisted(() => ({
	live: undefined as CloudCpSessionLive | undefined,
	listChatEvents: vi.fn(),
	sendSessionMessage: vi.fn(),
	steerTurn: vi.fn(),
	listChatModels: vi.fn(),
	listUserAgentModels: vi.fn(),
	resumeSession: vi.fn(),
	acquireComposer: vi.fn(),
	renewComposer: vi.fn(),
	releaseComposer: vi.fn(),
	requestComposerTurn: vi.fn(),
	leaveComposerQueue: vi.fn(),
	takeOverComposer: vi.fn(),
	publishComposerDraft: vi.fn(),
	setSessionChatSettings: vi.fn(),
	me: vi.fn(),
	getMyFeed: vi.fn(),
	listMentionCandidates: vi.fn(),
	postAside: vi.fn(),
	suggestAside: vi.fn(),
	listAsideSuggestions: vi.fn(),
	dismissAsideSuggestion: vi.fn(),
	createShareGrants: vi.fn(),
	chatProps: vi.fn(),
}));
vi.mock("../../hooks/useCloudCp", () => ({
	useCloudCp: () => ({ ready: true, client: mocks, userId: "user-me", baseUrl: "https://cp.test" }),
}));
vi.mock("../../hooks/useCloudSessionLive", () => ({
	useCloudSessionLive: () => mocks.live,
}));
// The chat, as far as these tests need it: the asides in its timeline and the
// strips above its composer.
vi.mock("./ChatWorkspace", () => ({
	ChatWorkspace: (props: { snapshot: ConversationSnapshot; composerNotice?: ReactNode }) => {
		mocks.chatProps(props);
		return (
			<div data-testid="cloud-chat">
				{props.snapshot.items.filter((item): item is ConversationActivity => item.kind === "activity" && item.detail?.event === "chat.aside")
					.map((item) => <AsideRow activity={item} key={item.id} />)}
				<div data-testid="composer-notice">{props.composerNotice}</div>
			</div>
		);
	},
}));

const session = {
	id: "session-1",
	workspaceId: "project-1",
	workspaceName: "storefront",
	title: "Fix token refresh",
	provider: "codex",
	kind: "worker",
	mode: "chat",
	status: "working",
	updatedAt: "2026-11-07T10:00:00Z",
	prs: [],
	cloud: { orgId: "org-1", permissionMode: "standard" },
} satisfies WorkspaceSession;

const me: CloudCpSessionViewer = { userId: "user-me", name: "Asha Rao", role: "member" };
const ben: CloudCpSessionViewer = { userId: "user-ben", name: "Ben Ortiz", role: "editor" };

function liveState(composer: Partial<CloudCpSessionLive["composer"]> = {}, viewers: CloudCpSessionViewer[] = [me, ben]): CloudCpSessionLive {
	return { type: "session.live", viewers, composer: { holder: null, epoch: 0, waitlist: [], ...composer }, canTakeOver: false, you: "user-me", chatSettings: {}, canConfigure: false };
}

const event = (sequence: number, type: string, payload: Record<string, unknown>): CloudCpClientEvent => ({
	sessionId: session.id, sequence, type, payload, createdAt: "2026-11-07T10:00:00Z",
});
const asideEvent = (sequence: number, asideId: string, text: string, authorUserId = "user-ben") =>
	event(sequence, "chat.aside", { asideId, text, authorUserId, mentions: [] });

const suggestion: CloudCpAsideSuggestion = {
	suggestionId: "sg-1", asideId: "a-2", text: "Also cover a refresh after the cookie expires.",
	suggestedBy: { userId: "user-chen", name: "Chen Wei" }, createdAt: "2026-11-07T10:00:00Z",
};

type Props = {
	snapshot: ConversationSnapshot;
	onSend: (text: string, attachments?: unknown, clientMessageId?: string, excerpts?: unknown, files?: unknown, display?: string, mentions?: ComposerMention[]) => Promise<unknown>;
	composerModes?: { mode: "agent" | "aside"; onModeChange: (mode: "agent" | "aside") => void; onSendAside: (text: string, clientMessageId: string | undefined, mentions: ComposerMention[] | undefined) => Promise<unknown> };
	composerMentions?: ComposerMentionSource;
	composerDraftRequest?: { id: string; text: string };
	composerLockedPlaceholder?: string;
};
const lastProps = () => mocks.chatProps.mock.lastCall?.[0] as Props;

function renderSurface(shown: WorkspaceSession = session) {
	const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
	return render(
		<QueryClientProvider client={queryClient}>
			<TooltipProvider>
				<CloudSessionChatSurface session={shown} />
			</TooltipProvider>
		</QueryClientProvider>,
	);
}

beforeEach(() => {
	vi.clearAllMocks();
	localStorage.clear();
	useUiStore.setState({ hideAsides: false });
	resetTeamCollabSupport();
	mocks.live = liveState();
	mocks.listChatEvents.mockResolvedValue({ events: [], hasMore: false, nextAfter: 0 });
	mocks.listChatModels.mockResolvedValue({ models: [] });
	mocks.listUserAgentModels.mockResolvedValue({ models: [] });
	mocks.resumeSession.mockResolvedValue(undefined);
	mocks.sendSessionMessage.mockResolvedValue({ event: {} });
	mocks.postAside.mockResolvedValue({ event: {} });
	mocks.suggestAside.mockResolvedValue({ suggestion });
	mocks.listAsideSuggestions.mockResolvedValue({ items: [] });
	mocks.dismissAsideSuggestion.mockResolvedValue(undefined);
	mocks.createShareGrants.mockResolvedValue({ entries: [] });
	mocks.listMentionCandidates.mockResolvedValue({ people: [], teams: [] });
	mocks.me.mockResolvedValue({ user: { id: "user-me", email: "asha@example.test", displayName: "Asha Rao", authProvider: "workos" }, organizations: [{ id: "org-1", slug: "northwind", displayName: "Northwind", role: "owner" }] });
	mocks.getMyFeed.mockResolvedValue({ items: [], page: { hasMore: false } });
});
afterEach(() => resetTeamCollabSupport());

describe("asides in the timeline", () => {
	const events = [
		event(1, "chat.user_message", { text: "Fix the refresh", turnId: "t1", authorUserId: "user-ben" }),
		event(2, "chat.turn_started", { turnId: "t1" }),
		asideEvent(3, "a-1", "Ping me before you merge."),
		event(4, "chat.assistant_delta", { text: "Done.", turnId: "t1", itemId: "reply" }),
		event(5, "chat.turn_completed", { turnId: "t1" }),
	];

	it("are notes that belong to no turn, never messages", () => {
		const snapshot = toSnapshot(session, events, "user-me");
		const aside = snapshot.items.find((item) => item.id === "cloud-aside-a-1") as ConversationActivity;
		expect(aside).toMatchObject({ kind: "activity", activityKind: "system", sequence: 3 });
		expect(aside.turnId).toBeUndefined();
		expect(aside.detail?.event).toBe("chat.aside");
		expect(aside.detail?.aside).toMatchObject({ asideId: "a-1", text: "Ping me before you merge.", authorUserId: "user-ben" });
		expect(snapshot.turns.map((turn) => turn.id)).toEqual(["t1"]);
		expect(snapshot.items.filter((item) => item.kind === "message").map((item) => item.kind === "message" && item.text)).toEqual(["Fix the refresh", "Done."]);
	});

	it("never reach anything the desktop sends the agent", () => {
		const snapshot = toSnapshot(session, events, "user-me");
		expect(promptSuggestionMessages(snapshot).map((message) => message.text)).not.toContain("Ping me before you merge.");
		expect(lastChosenChatSettings([...events, event(6, "chat.aside", { asideId: "a-9", text: "x", authorUserId: "u", model: "opus" })])).toBeUndefined();
		const quote = cloudExcerptText([{ id: "q", conversationId: "c", messageId: "cloud-event-1", revision: 1, text: "Fix", role: "user" }], snapshot);
		expect(quote).not.toContain("Ping me before you merge.");
	});

	it("leave a malformed aside out", () => {
		expect(toSnapshot(session, [event(1, "chat.aside", { text: "no id" })]).items).toEqual([]);
	});

	it("are left out on this device with Hide asides", async () => {
		recordTeamCollabSupport(BASE, "supported");
		mocks.listChatEvents.mockResolvedValue({ events, hasMore: false, nextAfter: 5 });
		renderSurface();
		await waitFor(() => expect(lastProps().snapshot.items.some((item) => item.id === "cloud-aside-a-1")).toBe(true));
		act(() => useUiStore.getState().setHideAsides(true));
		await waitFor(() => expect(lastProps().snapshot.items.some((item) => item.id === "cloud-aside-a-1")).toBe(false));
		expect(localStorage.getItem("ao.hideAsides")).toBe("true");
	});

	it("can be suggested to the agent from the row", async () => {
		recordTeamCollabSupport(BASE, "supported");
		mocks.listChatEvents.mockResolvedValue({ events, hasMore: false, nextAfter: 5 });
		renderSurface();
		const button = await screen.findByRole("button", { name: "Suggest to agent" });
		// Ben wrote it; the session's live state names him.
		expect(screen.getByText("Ben Ortiz")).toBeInTheDocument();
		await act(async () => fireEvent.click(button));
		expect(mocks.suggestAside).toHaveBeenCalledWith("org-1", "session-1", "a-1");
	});
});

describe("on a control plane without asides", () => {
	it("offers no Aside mode, no people in the @ menu and nothing to suggest", async () => {
		recordTeamCollabSupport(BASE, "unsupported");
		mocks.listChatEvents.mockResolvedValue({ events: [asideEvent(1, "a-1", "left over")], hasMore: false, nextAfter: 1 });
		renderSurface();
		await waitFor(() => expect(lastProps().snapshot.items).toHaveLength(1));
		expect(lastProps().composerModes).toBeUndefined();
		expect(lastProps().composerMentions).toBeUndefined();
		expect(screen.queryByRole("button", { name: "Suggest to agent" })).toBeNull();
		expect(mocks.listAsideSuggestions).not.toHaveBeenCalled();
		expect(mocks.listMentionCandidates).not.toHaveBeenCalled();
	});

	it("waits while it isn't known yet", async () => {
		mocks.getMyFeed.mockReturnValue(new Promise(() => undefined));
		renderSurface();
		await waitFor(() => expect(lastProps()).toBeDefined());
		expect(lastProps().composerModes).toBeUndefined();
	});
});

describe("the composer's Aside mode", () => {
	beforeEach(() => recordTeamCollabSupport(BASE, "supported"));

	it("starts on Agent for someone who can write, and switches", async () => {
		renderSurface();
		await waitFor(() => expect(lastProps().composerModes?.mode).toBe("agent"));
		act(() => lastProps().composerModes!.onModeChange("aside"));
		await waitFor(() => expect(lastProps().composerModes?.mode).toBe("aside"));
	});

	it("starts on Aside for a viewer", async () => {
		mocks.live = liveState({}, [{ ...me, role: "viewer" }, ben]);
		renderSurface();
		await waitFor(() => expect(lastProps().composerModes?.mode).toBe("aside"));
	});

	it("posts an aside with its mentions as tokens, without the composer lease", async () => {
		mocks.live = liveState({ holder: { userId: "user-ben", name: "Ben Ortiz" }, epoch: 4 });
		renderSurface();
		await waitFor(() => expect(lastProps().composerModes).toBeDefined());
		// Ben holds the agent: Agent mode is locked, Aside is not.
		expect(lastProps().composerLockedPlaceholder).toBe("");
		await act(async () => {
			await lastProps().composerModes!.onSendAside("@Chen Wei can you check?", "aside-key", [{ kind: "person", id: "user-chen", name: "Chen Wei", canOpen: true }]);
		});
		expect(mocks.postAside).toHaveBeenCalledWith("org-1", "session-1", { text: "@Chen Wei can you check?", mentions: [{ kind: "person", userId: "user-chen" }] }, { idempotencyKey: "aside-key" });
		expect(mocks.acquireComposer).not.toHaveBeenCalled();
		expect(mocks.sendSessionMessage).not.toHaveBeenCalled();
	});

	it("lets a viewer post an aside", async () => {
		mocks.live = liveState({}, [{ ...me, role: "viewer" }, ben]);
		renderSurface();
		await waitFor(() => expect(lastProps().composerModes?.mode).toBe("aside"));
		await act(async () => { await lastProps().composerModes!.onSendAside("Looks right", "k", undefined); });
		expect(mocks.postAside).toHaveBeenCalledWith("org-1", "session-1", { text: "Looks right" }, { idempotencyKey: "k" });
	});

	it("is not offered on a deleted session", async () => {
		renderSurface({ ...session, isTerminated: true });
		await waitFor(() => expect(lastProps()).toBeDefined());
		expect(lastProps().composerModes).toBeUndefined();
	});
});

describe("mentions", () => {
	beforeEach(() => recordTeamCollabSupport(BASE, "supported"));

	it("send with an agent message as typed tokens", async () => {
		renderSurface();
		await waitFor(() => expect(lastProps().composerMentions).toBeDefined());
		await act(async () => {
			await lastProps().onSend("@Platform heads up", undefined, "m-1", undefined, undefined, undefined, [{ kind: "team", id: "t-1", name: "Platform", memberCount: 3 }]);
		});
		expect(mocks.sendSessionMessage).toHaveBeenCalledWith("org-1", "session-1", expect.objectContaining({ text: "@Platform heads up", mentions: [{ kind: "team", teamId: "t-1" }] }), { idempotencyKey: "m-1" });
	});

	it("look people up for the @ menu, named for the workspace", async () => {
		mocks.listMentionCandidates.mockResolvedValue({ people: [{ userId: "user-chen", name: "Chen Wei", inSession: true, canOpen: true }], teams: [] });
		renderSurface();
		await waitFor(() => expect(lastProps().composerMentions).toBeDefined());
		act(() => lastProps().composerMentions!.onQuery("ch"));
		await waitFor(() => expect(lastProps().composerMentions?.candidates?.people).toHaveLength(1));
		expect(mocks.listMentionCandidates).toHaveBeenLastCalledWith("org-1", "session-1", { q: "ch", limit: 20 }, expect.anything());
		await waitFor(() => expect(lastProps().composerMentions?.workspaceName).toBe("Northwind"));
		expect(lastProps().composerMentions?.selfUserId).toBe("user-me");
		expect([...lastProps().composerMentions!.presentUserIds!]).toEqual(["user-me", "user-ben"]);
	});

	it("ask before notifying a big team: Notify sends, Cancel keeps the draft", async () => {
		renderSurface();
		await waitFor(() => expect(lastProps().composerMentions).toBeDefined());
		const platform: ComposerMention = { kind: "team", id: "t-platform", name: "Platform", memberCount: 6, membersWithoutAccess: 2 };
		let answer: Promise<ComposerMention[] | null> | undefined;
		act(() => { answer = lastProps().composerMentions!.confirm!([platform]); });
		expect(await screen.findByRole("alert")).toHaveTextContent("Notify 6 people in Platform?");
		fireEvent.click(screen.getByRole("button", { name: "Notify" }));
		await expect(answer).resolves.toEqual([platform]);
		act(() => { answer = lastProps().composerMentions!.confirm!([platform]); });
		fireEvent.click(await screen.findByRole("button", { name: "Cancel" }));
		await expect(answer).resolves.toBeNull();
		expect(screen.queryByRole("alert")).toBeNull();
	});

	it("share the session at Watch with someone who can't open it, then send", async () => {
		renderSurface();
		await waitFor(() => expect(lastProps().composerMentions).toBeDefined());
		const cara: ComposerMention = { kind: "person", id: "user-cara", name: "Cara Diaz", canOpen: false };
		let answer: Promise<ComposerMention[] | null> | undefined;
		act(() => { answer = lastProps().composerMentions!.confirm!([cara]); });
		expect(await screen.findByRole("alert")).toHaveTextContent("Cara Diaz can't open this session");
		await act(async () => fireEvent.click(screen.getByRole("button", { name: "Share and send" })));
		expect(mocks.createShareGrants).toHaveBeenCalledWith("org-1", "project-1", { sessionId: "session-1", subjects: [{ userId: "user-cara" }], role: "viewer", modeCap: "read-only" });
		await expect(answer).resolves.toEqual([cara]);
	});

	it("send without the people who can't open it, sharing nothing", async () => {
		renderSurface();
		await waitFor(() => expect(lastProps().composerMentions).toBeDefined());
		const cara: ComposerMention = { kind: "person", id: "user-cara", name: "Cara Diaz", canOpen: false };
		const chen: ComposerMention = { kind: "person", id: "user-chen", name: "Chen Wei", canOpen: true };
		let answer: Promise<ComposerMention[] | null> | undefined;
		act(() => { answer = lastProps().composerMentions!.confirm!([cara, chen]); });
		fireEvent.click(await screen.findByRole("button", { name: "Send without them" }));
		await expect(answer).resolves.toEqual([chen]);
		expect(mocks.createShareGrants).not.toHaveBeenCalled();
	});

	it("keep asking when sharing fails", async () => {
		mocks.createShareGrants.mockRejectedValue(new Error("forbidden"));
		renderSurface();
		await waitFor(() => expect(lastProps().composerMentions).toBeDefined());
		act(() => { void lastProps().composerMentions!.confirm!([{ kind: "person", id: "user-cara", name: "Cara Diaz", canOpen: false }]); });
		await act(async () => fireEvent.click(await screen.findByRole("button", { name: "Share and send" })));
		expect(screen.getByText("Couldn't share. Try again, or send without them.")).toBeInTheDocument();
		expect(screen.getByRole("button", { name: "Send without them" })).toBeEnabled();
	});
});

describe("suggestions from asides", () => {
	beforeEach(() => {
		recordTeamCollabSupport(BASE, "supported");
		mocks.listAsideSuggestions.mockResolvedValue({ items: [suggestion, { ...suggestion, suggestionId: "sg-2", asideId: "a-3", text: "Second" }] });
	});

	it("wait above the driver's composer, oldest first, and Send sends it as a message", async () => {
		renderSurface();
		expect(await screen.findByRole("region", { name: "Chen Wei suggests" })).toHaveTextContent("Also cover a refresh after the cookie expires.");
		expect(screen.queryByText("Second")).toBeNull();
		await act(async () => fireEvent.click(screen.getByRole("button", { name: "Send" })));
		expect(mocks.sendSessionMessage).toHaveBeenCalledWith("org-1", "session-1", expect.objectContaining({ text: suggestion.text, suggestionId: "sg-1" }), expect.objectContaining({ idempotencyKey: expect.any(String) }));
		// The next one waits its turn.
		expect(await screen.findByText(/Second/)).toBeInTheDocument();
	});

	it("Dismiss drops it on the control plane, which tells its author", async () => {
		renderSurface();
		await screen.findByRole("region", { name: "Chen Wei suggests" });
		await act(async () => fireEvent.click(screen.getByRole("button", { name: "Dismiss" })));
		expect(mocks.dismissAsideSuggestion).toHaveBeenCalledWith("org-1", "session-1", "sg-1");
		expect(mocks.sendSessionMessage).not.toHaveBeenCalled();
	});

	it("Edit puts it in the agent draft, and the next message sends it", async () => {
		renderSurface();
		await screen.findByRole("region", { name: "Chen Wei suggests" });
		act(() => lastProps().composerModes!.onModeChange("aside"));
		fireEvent.click(screen.getByRole("button", { name: "Edit" }));
		await waitFor(() => expect(lastProps().composerDraftRequest).toEqual({ id: "sg-1", text: suggestion.text }));
		expect(lastProps().composerModes?.mode).toBe("agent");
		await act(async () => { await lastProps().onSend("Also cover an expired cookie", undefined, "m-2"); });
		expect(mocks.sendSessionMessage).toHaveBeenLastCalledWith("org-1", "session-1", expect.objectContaining({ text: "Also cover an expired cookie", suggestionId: "sg-1" }), { idempotencyKey: "m-2" });
		await act(async () => { await lastProps().onSend("Thanks", undefined, "m-3"); });
		expect(mocks.sendSessionMessage.mock.lastCall?.[2]).not.toHaveProperty("suggestionId");
	});

	it("never show to someone else while another person holds the agent", async () => {
		mocks.live = liveState({ holder: { userId: "user-ben", name: "Ben Ortiz" }, epoch: 3 });
		renderSurface();
		await waitFor(() => expect(mocks.listAsideSuggestions).toHaveBeenCalled());
		await act(async () => undefined);
		expect(screen.queryByRole("region", { name: "Chen Wei suggests" })).toBeNull();
	});

	it("never show to a viewer", async () => {
		mocks.live = liveState({}, [{ ...me, role: "viewer" }, ben]);
		renderSurface();
		await waitFor(() => expect(mocks.listAsideSuggestions).toHaveBeenCalled());
		await act(async () => undefined);
		expect(screen.queryByRole("region", { name: "Chen Wei suggests" })).toBeNull();
	});

	it("mark an aside already waiting as Suggested", async () => {
		mocks.listChatEvents.mockResolvedValue({ events: [asideEvent(1, "a-2", "Also cover it")], hasMore: false, nextAfter: 1 });
		renderSurface();
		expect(await screen.findByText("Suggested")).toBeInTheDocument();
	});
});
