import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import type { ReactNode } from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { promptSuggestionMessages } from "../../hooks/useCloudPromptSuggestion";
import type { CloudCpClientEvent, CloudCpSessionLive, CloudCpSessionViewer, CloudCpSuggestion } from "../../lib/cloud-cp";
import { CloudCpError } from "../../lib/cloud-cp/errors";
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
	createSessionAside: vi.fn(),
	createSessionSuggestion: vi.fn(),
	listSessionSuggestions: vi.fn(),
	sendSessionSuggestion: vi.fn(),
	dismissSessionSuggestion: vi.fn(),
	createShareGrants: vi.fn(),
	chatProps: vi.fn(),
}));
vi.mock("../../hooks/useCloudCp", () => ({
	useCloudCp: () => ({ ready: true, client: mocks, userId: "user-me", baseUrl: "https://cp.test" }),
}));
vi.mock("../../hooks/useCloudOffering", () => ({
	useCloudOffering: () => ({ features: ["attachments"], maxAttachmentBytes: 1_000_000 }),
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
const asideEvent = (sequence: number, asideId: string, text: string, authorUserId = "user-ben", authorName: string | undefined = "Ben Ortiz") =>
	event(sequence, "chat.aside", { asideId, text, authorUserId, ...(authorName === undefined ? {} : { authorName }), mentions: [] });

const suggestion: CloudCpSuggestion = {
	id: "sg-1", orgId: "org-1", sessionId: "session-1", kind: "aside", asideId: "a-2", text: "Also cover a refresh after the cookie expires.",
	suggestedBy: { userId: "user-chen", name: "Chen Wei" }, author: { userId: "user-chen", name: "Chen Wei" }, status: "pending",
	createdAt: "2026-11-07T10:00:00Z", updatedAt: "2026-11-07T10:00:00Z",
};
const second: CloudCpSuggestion = { ...suggestion, id: "sg-2", asideId: "a-3", text: "Second", createdAt: "2026-11-07T10:05:00Z", updatedAt: "2026-11-07T10:05:00Z" };

type Props = {
	snapshot: ConversationSnapshot;
	onSend: (text: string, attachments?: unknown, clientMessageId?: string, excerpts?: unknown, files?: unknown, display?: string, mentions?: ComposerMention[]) => Promise<unknown>;
	onSteer?: (text: string, attachments?: unknown, clientMessageId?: string, recoverOnly?: boolean, mentions?: ComposerMention[]) => Promise<unknown>;
	onStageAttachments?: unknown;
	composerModes?: { mode: "agent" | "aside"; onModeChange: (mode: "agent" | "aside") => void; onSendAside: (text: string, clientMessageId: string | undefined, mentions: ComposerMention[] | undefined) => Promise<unknown> };
	composerMentions?: ComposerMentionSource;
	composerDraftRequest?: { id: string; text: string };
	composerLockedPlaceholder?: string;
};
const lastProps = () => mocks.chatProps.mock.lastCall?.[0] as Props;

let queryClient: QueryClient;
function renderSurface(shown: WorkspaceSession = session) {
	queryClient = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
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
	mocks.createSessionAside.mockResolvedValue({ event: {} });
	mocks.createSessionSuggestion.mockResolvedValue({ suggestion });
	mocks.listSessionSuggestions.mockResolvedValue({ items: [] });
	mocks.sendSessionSuggestion.mockImplementation(async (_org: string, _session: string, id: string) => ({ event: {}, suggestion: { ...suggestion, id, status: "sent" } }));
	mocks.dismissSessionSuggestion.mockImplementation(async (_org: string, _session: string, id: string) => ({ suggestion: { ...suggestion, id, status: "dismissed" } }));
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
		await act(async () => fireEvent.click(button));
		expect(mocks.createSessionSuggestion).toHaveBeenCalledWith("org-1", "session-1", { asideId: "a-1" }, { idempotencyKey: expect.any(String) });
		// Its answer marks the aside at once.
		mocks.createSessionSuggestion.mockResolvedValue({ suggestion: { ...suggestion, asideId: "a-1" } });
	});

	it("names their author as the event recorded them", async () => {
		recordTeamCollabSupport(BASE, "supported");
		mocks.listChatEvents.mockResolvedValue({ events: [asideEvent(1, "a-1", "Hi", "user-ben", "Ben O.")], hasMore: false, nextAfter: 1 });
		renderSurface();
		expect(await screen.findByText("Ben O.")).toBeInTheDocument();
		expect(screen.queryByText("Ben Ortiz")).toBeNull();
	});

	it("name an old aside's author from the people in the session", async () => {
		recordTeamCollabSupport(BASE, "supported");
		mocks.listChatEvents.mockResolvedValue({ events: [asideEvent(1, "a-1", "Hi", "user-ben", undefined)], hasMore: false, nextAfter: 1 });
		renderSurface();
		expect(await screen.findByText("Ben Ortiz")).toBeInTheDocument();
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
		expect(mocks.listSessionSuggestions).not.toHaveBeenCalled();
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
		expect(mocks.createSessionAside).toHaveBeenCalledWith("org-1", "session-1", { text: "@Chen Wei can you check?", mentions: [{ kind: "person", userId: "user-chen" }] }, { idempotencyKey: "aside-key" });
		expect(mocks.acquireComposer).not.toHaveBeenCalled();
		expect(mocks.sendSessionMessage).not.toHaveBeenCalled();
	});

	it("lets a viewer post an aside", async () => {
		mocks.live = liveState({}, [{ ...me, role: "viewer" }, ben]);
		renderSurface();
		await waitFor(() => expect(lastProps().composerModes?.mode).toBe("aside"));
		await act(async () => { await lastProps().composerModes!.onSendAside("Looks right", "k", undefined); });
		expect(mocks.createSessionAside).toHaveBeenCalledWith("org-1", "session-1", { text: "Looks right" }, { idempotencyKey: "k" });
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

	it("say calmly when the control plane doesn't know someone, and still send without them", async () => {
		mocks.createShareGrants.mockRejectedValue(new CloudCpError("Not found", { status: 404, code: "not_found" }));
		renderSurface();
		await waitFor(() => expect(lastProps().composerMentions).toBeDefined());
		const cara: ComposerMention = { kind: "person", id: "user-cara", name: "Cara Diaz", canOpen: false };
		let answer: Promise<ComposerMention[] | null> | undefined;
		act(() => { answer = lastProps().composerMentions!.confirm!([cara]); });
		await act(async () => fireEvent.click(await screen.findByRole("button", { name: "Share and send" })));
		expect(screen.getByText("Couldn't share with Cara Diaz. Send without them?")).toHaveAttribute("role", "status");
		fireEvent.click(screen.getByRole("button", { name: "Send without them" }));
		await expect(answer).resolves.toEqual([]);
	});

	it("go back to the draft calmly when the control plane refuses them", async () => {
		mocks.sendSessionMessage.mockRejectedValue(new CloudCpError("Not allowed.", { status: 422, code: "mention_not_allowed" }));
		mocks.createSessionAside.mockRejectedValue(new CloudCpError("Too many.", { status: 422, code: "too_many_mentioned" }));
		renderSurface();
		await waitFor(() => expect(lastProps().composerModes).toBeDefined());
		const chen: ComposerMention = { kind: "person", id: "user-chen", name: "Chen Wei", canOpen: true };
		await expect(lastProps().onSend("@Chen Wei hi", undefined, "m-1", undefined, undefined, undefined, [chen]))
			.rejects.toMatchObject({ message: "You can mention only people in this session.", code: "mention_not_allowed", calm: true });
		await expect(lastProps().composerModes!.onSendAside("@Platform hi", "a-1", [{ kind: "team", id: "t-1", name: "Platform" }]))
			.rejects.toMatchObject({ message: "That would notify too many people. Mention fewer.", code: "too_many_mentioned", calm: true });
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
	const holding = () => liveState({ holder: { userId: "user-me", name: "Asha Rao" }, epoch: 7 });
	const appendEvents = (...added: CloudCpClientEvent[]) => act(() => {
		queryClient.setQueryData<CloudCpClientEvent[]>(["cloud-chat-events", "org-1", "session-1"], (current) => [...(current ?? []), ...added]);
	});

	beforeEach(() => {
		recordTeamCollabSupport(BASE, "supported");
		mocks.live = holding();
		mocks.listSessionSuggestions.mockResolvedValue({ items: [suggestion, second] });
	});

	it("wait above the driver's composer, oldest first, and Send goes through the suggestion's route", async () => {
		renderSurface();
		expect(await screen.findByRole("region", { name: "Chen Wei suggests" })).toHaveTextContent("Also cover a refresh after the cookie expires.");
		expect(screen.queryByText(/Second/)).toBeNull();
		await act(async () => fireEvent.click(screen.getByRole("button", { name: "Send" })));
		// Its own text, the turn's settings and the driver's grant; no ordinary message.
		expect(mocks.sendSessionSuggestion).toHaveBeenCalledWith("org-1", "session-1", "sg-1", { mode: "standard", composerEpoch: 7 }, { idempotencyKey: expect.any(String) });
		expect(mocks.sendSessionMessage).not.toHaveBeenCalled();
		// The next one waits its turn.
		expect(await screen.findByText(/Second/)).toBeInTheDocument();
	});

	it("Dismiss declines it with the driver's grant", async () => {
		renderSurface();
		await screen.findByRole("region", { name: "Chen Wei suggests" });
		await act(async () => fireEvent.click(screen.getByRole("button", { name: "Dismiss" })));
		expect(mocks.dismissSessionSuggestion).toHaveBeenCalledWith("org-1", "session-1", "sg-1", { composerEpoch: 7 });
		expect(await screen.findByText(/Second/)).toBeInTheDocument();
		expect(mocks.sendSessionSuggestion).not.toHaveBeenCalled();
	});

	it("say calmly, on one line, that someone else drives now", async () => {
		mocks.sendSessionSuggestion.mockRejectedValue(new CloudCpError("Someone else is writing to the agent.", { status: 409, code: "COMPOSER_LOCKED" }));
		renderSurface();
		await screen.findByRole("region", { name: "Chen Wei suggests" });
		await act(async () => fireEvent.click(screen.getByRole("button", { name: "Send" })));
		const line = await screen.findByText("Someone else is driving now.");
		expect(line).toHaveAttribute("role", "status");
		expect(line).toHaveClass("text-muted-foreground");
		expect(screen.queryByRole("alert")).toBeNull();
		// It still waits.
		expect(screen.getByRole("region", { name: "Chen Wei suggests" })).toBeInTheDocument();
	});

	it("say calmly, on one line, that it was already sent or dismissed, and move on", async () => {
		mocks.dismissSessionSuggestion.mockRejectedValue(new CloudCpError("Already resolved.", { status: 409, code: "suggestion_resolved" }));
		renderSurface();
		await screen.findByRole("region", { name: "Chen Wei suggests" });
		await act(async () => fireEvent.click(screen.getByRole("button", { name: "Dismiss" })));
		expect(await screen.findByText("Already sent or dismissed.")).toHaveAttribute("role", "status");
		expect(screen.queryByRole("alert")).toBeNull();
		expect(await screen.findByText(/Second/)).toBeInTheDocument();
	});

	it("say a real failure as one", async () => {
		mocks.sendSessionSuggestion.mockRejectedValue(new Error("offline"));
		renderSurface();
		await screen.findByRole("region", { name: "Chen Wei suggests" });
		await act(async () => fireEvent.click(screen.getByRole("button", { name: "Send" })));
		expect(await screen.findByRole("alert")).toHaveTextContent("Couldn't do that. Try again.");
	});

	it("Edit puts it in the agent draft, and the next send sends it, as edited, through its route", async () => {
		renderSurface();
		await screen.findByRole("region", { name: "Chen Wei suggests" });
		act(() => lastProps().composerModes!.onModeChange("aside"));
		fireEvent.click(screen.getByRole("button", { name: "Edit" }));
		await waitFor(() => expect(lastProps().composerDraftRequest).toEqual({ id: "sg-1", text: suggestion.text }));
		expect(lastProps().composerModes?.mode).toBe("agent");
		expect(screen.getByRole("region", { name: "Editing Chen Wei's suggestion" })).toBeInTheDocument();
		// It goes as text alone, so the composer takes no files meanwhile.
		expect(lastProps().onStageAttachments).toBeUndefined();
		await act(async () => { await lastProps().onSend("Also cover an expired cookie", undefined, "m-2"); });
		expect(mocks.sendSessionSuggestion).toHaveBeenCalledWith("org-1", "session-1", "sg-1", { text: "Also cover an expired cookie", mode: "standard", composerEpoch: 7 }, { idempotencyKey: "m-2" });
		expect(mocks.sendSessionMessage).not.toHaveBeenCalled();
		// Done editing: the next message is an ordinary one.
		await waitFor(() => expect(screen.queryByRole("region", { name: "Editing Chen Wei's suggestion" })).toBeNull());
		expect(lastProps().onStageAttachments).toBeTypeOf("function");
		await act(async () => { await lastProps().onSend("Thanks", undefined, "m-3"); });
		expect(mocks.sendSessionMessage).toHaveBeenCalledWith("org-1", "session-1", expect.objectContaining({ text: "Thanks" }), { idempotencyKey: "m-3" });
	});

	it("Cancel stops editing and the suggestion waits again", async () => {
		renderSurface();
		await screen.findByRole("region", { name: "Chen Wei suggests" });
		fireEvent.click(screen.getByRole("button", { name: "Edit" }));
		fireEvent.click(await screen.findByRole("button", { name: "Cancel" }));
		expect(await screen.findByRole("region", { name: "Chen Wei suggests" })).toBeInTheDocument();
		await act(async () => { await lastProps().onSend("Something else", undefined, "m-9"); });
		expect(mocks.sendSessionSuggestion).not.toHaveBeenCalled();
	});

	it("an edited send that finds it already handled goes back to the composer, calmly", async () => {
		mocks.sendSessionSuggestion.mockRejectedValue(new CloudCpError("Already resolved.", { status: 409, code: "suggestion_resolved" }));
		renderSurface();
		await screen.findByRole("region", { name: "Chen Wei suggests" });
		fireEvent.click(screen.getByRole("button", { name: "Edit" }));
		let refusal: unknown;
		await act(async () => { await lastProps().onSend("Edited", undefined, "m-4").catch((error: unknown) => { refusal = error; }); });
		expect(refusal).toMatchObject({ message: "Already sent or dismissed.", code: "suggestion_resolved", calm: true });
		await waitFor(() => expect(screen.queryByRole("region", { name: "Editing Chen Wei's suggestion" })).toBeNull());
		// The text, sent again, is an ordinary message.
		await act(async () => { await lastProps().onSend("Edited", undefined, "m-5"); });
		expect(mocks.sendSessionMessage).toHaveBeenCalledWith("org-1", "session-1", expect.objectContaining({ text: "Edited" }), { idempotencyKey: "m-5" });
	});

	it("an edited send refused because someone else drives keeps the edit, calmly", async () => {
		mocks.sendSessionSuggestion.mockRejectedValue(new CloudCpError("Locked.", { status: 409, code: "COMPOSER_LOCKED" }));
		renderSurface();
		await screen.findByRole("region", { name: "Chen Wei suggests" });
		fireEvent.click(screen.getByRole("button", { name: "Edit" }));
		let refusal: unknown;
		await act(async () => { await lastProps().onSend("Edited", undefined, "m-6").catch((error: unknown) => { refusal = error; }); });
		expect(refusal).toMatchObject({ message: "Someone else is driving now.", code: "COMPOSER_LOCKED", calm: true });
		expect(screen.getByRole("region", { name: "Editing Chen Wei's suggestion" })).toBeInTheDocument();
	});

	it("follow the session's suggestion events as they arrive", async () => {
		mocks.listSessionSuggestions.mockResolvedValue({ items: [] });
		mocks.listChatEvents.mockResolvedValue({ events: [asideEvent(1, "a-2", "Also cover it")], hasMore: false, nextAfter: 1 });
		renderSurface();
		await waitFor(() => expect(lastProps().snapshot.items).toHaveLength(1));
		await waitFor(() => expect(mocks.listSessionSuggestions).toHaveBeenCalledTimes(1));
		expect(screen.queryByRole("region", { name: "Chen Wei suggests" })).toBeNull();
		appendEvents(event(2, "chat.suggestion_created", suggestion));
		expect(await screen.findByRole("region", { name: "Chen Wei suggests" })).toBeInTheDocument();
		expect(screen.getByText("Suggested")).toBeInTheDocument();
		appendEvents(event(3, "chat.suggestion_resolved", { ...suggestion, status: "dismissed" }));
		await waitFor(() => expect(screen.queryByRole("region", { name: "Chen Wei suggests" })).toBeNull());
		expect(screen.queryByText("Suggested")).toBeNull();
		// The events did it; the list was read once.
		expect(mocks.listSessionSuggestions).toHaveBeenCalledTimes(1);
	});

	it("stop editing when someone else settles the suggestion meanwhile", async () => {
		renderSurface();
		await screen.findByRole("region", { name: "Chen Wei suggests" });
		await waitFor(() => expect(lastProps().snapshot).toBeDefined());
		fireEvent.click(screen.getByRole("button", { name: "Edit" }));
		await screen.findByRole("region", { name: "Editing Chen Wei's suggestion" });
		appendEvents(event(5, "chat.suggestion_resolved", { ...suggestion, status: "sent" }));
		expect(await screen.findByText("Already sent or dismissed.")).toHaveAttribute("role", "status");
		expect(screen.queryByRole("region", { name: "Editing Chen Wei's suggestion" })).toBeNull();
	});

	it("never show to someone else while another person holds the agent", async () => {
		mocks.live = liveState({ holder: { userId: "user-ben", name: "Ben Ortiz" }, epoch: 3 });
		renderSurface();
		await waitFor(() => expect(mocks.listSessionSuggestions).toHaveBeenCalled());
		await act(async () => undefined);
		expect(screen.queryByRole("region", { name: "Chen Wei suggests" })).toBeNull();
	});

	it("never show to a viewer", async () => {
		mocks.live = liveState({}, [{ ...me, role: "viewer" }, ben]);
		renderSurface();
		await waitFor(() => expect(mocks.listSessionSuggestions).toHaveBeenCalled());
		await act(async () => undefined);
		expect(screen.queryByRole("region", { name: "Chen Wei suggests" })).toBeNull();
	});

	it("mark an aside already waiting as Suggested", async () => {
		mocks.listChatEvents.mockResolvedValue({ events: [asideEvent(1, "a-2", "Also cover it")], hasMore: false, nextAfter: 1 });
		renderSurface();
		expect(await screen.findByText("Suggested")).toBeInTheDocument();
		// Chen made it, not this person: nothing to withdraw.
		expect(screen.queryByRole("button", { name: "Withdraw" })).toBeNull();
	});

	it("let the person who made one withdraw it, holding no grant", async () => {
		const mine: CloudCpSuggestion = { ...suggestion, suggestedBy: { userId: "user-me", name: "Asha Rao" } };
		mocks.live = liveState({}, [{ ...me, role: "viewer" }, ben]);
		mocks.listSessionSuggestions.mockResolvedValue({ items: [mine] });
		mocks.listChatEvents.mockResolvedValue({ events: [asideEvent(1, "a-2", "Also cover it")], hasMore: false, nextAfter: 1 });
		renderSurface();
		await act(async () => fireEvent.click(await screen.findByRole("button", { name: "Withdraw" })));
		expect(mocks.dismissSessionSuggestion).toHaveBeenCalledWith("org-1", "session-1", "sg-1", {});
		await waitFor(() => expect(screen.queryByText("Suggested")).toBeNull());
	});

});

describe("mentions in a steer", () => {
	it("report a refused mention as a declined steer, calmly", async () => {
		recordTeamCollabSupport(BASE, "supported");
		mocks.listChatEvents.mockResolvedValue({ events: [
			event(1, "chat.user_message", { text: "Fix it", turnId: "t1" }),
			event(2, "chat.turn_started", { turnId: "t1" }),
			event(3, "chat.turn_capabilities", { turnId: "t1", steering: true }),
		], hasMore: false, nextAfter: 3 });
		mocks.steerTurn.mockRejectedValue(new CloudCpError("Not allowed.", { status: 422, code: "mention_not_allowed" }));
		renderSurface();
		await waitFor(() => expect(lastProps().onSteer).toBeTypeOf("function"));
		let outcome: unknown;
		await act(async () => { outcome = await lastProps().onSteer!("@Cara look", undefined, "steer-2", false, [{ kind: "person", id: "user-cara", name: "Cara", canOpen: true }]); });
		expect(outcome).toEqual({ status: "not-accepted", reason: "You can mention only people in this session." });
	});

	it("go to the steer route as typed tokens", async () => {
		recordTeamCollabSupport(BASE, "supported");
		const running = [
			event(1, "chat.user_message", { text: "Fix it", turnId: "t1" }),
			event(2, "chat.turn_started", { turnId: "t1" }),
			event(3, "chat.turn_capabilities", { turnId: "t1", steering: true }),
		];
		mocks.listChatEvents.mockImplementation(async (_org: string, _session: string, query: { after: number }) => query.after === 10
			? { events: [event(11, "chat.turn_steered", { turnId: "t1", text: "@Chen Wei look", clientMessageId: "steer-1" })], hasMore: false, nextAfter: 11 }
			: { events: running, hasMore: false, nextAfter: 3 });
		mocks.steerTurn.mockResolvedValue({ event: { sequence: 10 } });
		renderSurface();
		await waitFor(() => expect(lastProps().onSteer).toBeTypeOf("function"));
		await act(async () => {
			await lastProps().onSteer!("@Chen Wei look", undefined, "steer-1", false, [{ kind: "person", id: "user-chen", name: "Chen Wei", canOpen: true }]);
		});
		expect(mocks.steerTurn).toHaveBeenCalledWith("org-1", "session-1", "t1", { text: "@Chen Wei look", mentions: [{ kind: "person", userId: "user-chen" }] }, { idempotencyKey: "steer-1" });
	});
});
