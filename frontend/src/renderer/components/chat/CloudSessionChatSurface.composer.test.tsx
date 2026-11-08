import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { workspaceQueryKey } from "../../hooks/useWorkspaceQuery";
import type { CloudCpSessionLive, CloudCpSessionViewer, CloudCpSuggestion } from "../../lib/cloud-cp";
import { recordTeamCollabSupport, resetTeamCollabSupport } from "../../lib/cloud-team-collab";
import { useUiStore } from "../../stores/ui-store";
import { lexicalEditorText, typeInLexicalEditor } from "../../test/lexical";
import type { WorkspaceSession } from "../../types/workspace";
import { TooltipProvider } from "../ui/tooltip";
import { CloudSessionChatSurface } from "./CloudSessionChatSurface";

// The whole chat, composer included: nothing between the surface and the
// editor is mocked, so these say what a person typing actually gets.

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
	listSessionFiles: vi.fn(),
}));
vi.mock("../../hooks/useCloudCp", () => ({
	useCloudCp: () => ({ ready: true, client: mocks, userId: "user-me", baseUrl: "https://cp.test", signedOut: false }),
}));
vi.mock("../../hooks/useCloudOffering", () => ({
	useCloudOffering: () => ({ features: [], maxAttachmentBytes: undefined }),
}));
vi.mock("../../hooks/useCloudSessionLive", () => ({
	useCloudSessionLive: () => mocks.live,
}));
vi.mock("../NotificationCenter", () => ({ NotificationCenter: () => <button type="button" aria-label="Notifications" /> }));

const session = {
	id: "session-real",
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
const live = (holder: { userId: string; name: string } | null, epoch = 3): CloudCpSessionLive => ({
	type: "session.live", viewers: [me, ben], composer: { holder, epoch, waitlist: [] }, canTakeOver: false, you: "user-me", chatSettings: {}, canConfigure: false,
});
const suggestion: CloudCpSuggestion = {
	id: "sg-1", orgId: "org-1", sessionId: "session-real", kind: "aside", asideId: "a-1", text: "Also cover the expired cookie", mentions: [],
	suggestedBy: { userId: "user-chen", name: "Chen Wei" }, author: { userId: "user-chen", name: "Chen Wei" }, status: "pending",
	createdAt: "2026-11-07T10:00:00Z", updatedAt: "2026-11-07T10:00:00Z",
};

function renderSurface() {
	const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
	client.setQueryData(workspaceQueryKey, []);
	return render(
		<QueryClientProvider client={client}>
			<TooltipProvider>
				<CloudSessionChatSurface session={session} />
			</TooltipProvider>
		</QueryClientProvider>,
	);
}

beforeEach(() => {
	vi.clearAllMocks();
	localStorage.clear();
	useUiStore.setState({ hideAsides: false, chatSendKeyMode: "enter" });
	resetTeamCollabSupport();
	recordTeamCollabSupport("https://cp.test", "supported");
	mocks.listChatEvents.mockResolvedValue({ events: [], hasMore: false, nextAfter: 0 });
	mocks.listChatModels.mockResolvedValue({ models: [] });
	mocks.listUserAgentModels.mockResolvedValue({ models: [] });
	mocks.resumeSession.mockResolvedValue(undefined);
	mocks.sendSessionMessage.mockResolvedValue({ event: {} });
	mocks.createSessionAside.mockResolvedValue({ event: {} });
	mocks.listSessionSuggestions.mockResolvedValue({ items: [] });
	mocks.sendSessionSuggestion.mockResolvedValue({ event: {}, suggestion: { ...suggestion, status: "sent" } });
	mocks.listMentionCandidates.mockResolvedValue({ people: [], teams: [] });
	mocks.acquireComposer.mockResolvedValue({ composer: { holder: { userId: "user-me", name: "Asha Rao" }, epoch: 4, waitlist: [] } });
	mocks.publishComposerDraft.mockResolvedValue(undefined);
	mocks.listSessionFiles.mockResolvedValue({ paths: [], truncated: false });
	mocks.me.mockResolvedValue({ user: { id: "user-me", email: "asha@example.test", displayName: "Asha Rao", authProvider: "workos" }, organizations: [{ id: "org-1", slug: "northwind", displayName: "Northwind", role: "owner", kind: "team" }] });
	mocks.getMyFeed.mockResolvedValue({ items: [], page: { hasMore: false } });
});
afterEach(() => resetTeamCollabSupport());

describe("the real composer in a shared cloud session", () => {
	it("posts an aside typed in Aside mode, and never takes the agent's composer lease for it", async () => {
		// Ben drives: Agent mode is his, Aside is open to everyone.
		mocks.live = live({ userId: "user-ben", name: "Ben Ortiz" });
		renderSurface();
		fireEvent.click(await screen.findByRole("radio", { name: "Aside" }));
		const aside = await screen.findByRole("combobox", { name: "Message teammates" });
		await typeInLexicalEditor(aside, "Looks right to me");
		fireEvent.keyDown(aside, { key: "Enter" });
		await waitFor(() => expect(mocks.createSessionAside).toHaveBeenCalledWith("org-1", "session-real", { text: "Looks right to me" }, { idempotencyKey: expect.any(String) }));
		expect(mocks.acquireComposer).not.toHaveBeenCalled();
		expect(mocks.requestComposerTurn).not.toHaveBeenCalled();
		expect(mocks.sendSessionMessage).not.toHaveBeenCalled();
	});

	it("edits a suggestion in place of the driver's draft: Cancel leaves the composer clean, Edit again works, and a send gives the draft back", async () => {
		mocks.live = live({ userId: "user-me", name: "Asha Rao" }, 7);
		mocks.listSessionSuggestions.mockResolvedValue({ items: [suggestion] });
		renderSurface();
		const agent = await screen.findByRole("combobox", { name: "Message the agent" });
		await typeInLexicalEditor(agent, "my own draft");
		fireEvent.click(await screen.findByRole("button", { name: "Edit" }));
		await waitFor(() => expect(lexicalEditorText(screen.getByRole("combobox", { name: "Message the agent" }))).toBe("Also cover the expired cookie"));
		fireEvent.click(screen.getByRole("button", { name: "Cancel" }));
		await waitFor(() => expect(lexicalEditorText(screen.getByRole("combobox", { name: "Message the agent" }))).toBe("my own draft"));
		fireEvent.click(await screen.findByRole("button", { name: "Edit" }));
		const editing = screen.getByRole("combobox", { name: "Message the agent" });
		await waitFor(() => expect(lexicalEditorText(editing)).toBe("Also cover the expired cookie"));
		await typeInLexicalEditor(editing, " please");
		fireEvent.keyDown(screen.getByRole("combobox", { name: "Message the agent" }), { key: "Enter" });
		await waitFor(() => expect(mocks.sendSessionSuggestion).toHaveBeenCalledTimes(1));
		expect(mocks.sendSessionSuggestion.mock.calls[0]![3]).toMatchObject({ text: "Also cover the expired cookie please", composerEpoch: 7 });
		expect(mocks.sendSessionMessage).not.toHaveBeenCalled();
		await waitFor(() => expect(lexicalEditorText(screen.getByRole("combobox", { name: "Message the agent" }))).toBe("my own draft"));
	});

	it("gives a guest whose role hasn't arrived the aside composer and no approval to press, then Agent once the role says editor", async () => {
		const pendingApproval = [
			{ sessionId: session.id, sequence: 1, type: "chat.user_message", payload: { text: "Retry the webhooks", turnId: "turn-1" }, createdAt: session.updatedAt },
			{ sessionId: session.id, sequence: 2, type: "chat.turn_started", payload: { turnId: "turn-1" }, createdAt: session.updatedAt },
			{ sessionId: session.id, sequence: 3, type: "chat.approval_requested", payload: {
				turnId: "turn-1", requestId: "approval-1", summary: "Run the migration",
				decisions: [{ id: "allow-once", label: "Allow once", kind: "allow_once" }, { id: "reject", label: "Deny", kind: "reject_once" }],
			}, createdAt: session.updatedAt },
		];
		mocks.listChatEvents.mockResolvedValue({ events: pendingApproval, hasMore: false, nextAfter: 3 });
		mocks.live = undefined;
		const guest = { ...session, cloud: { ...session.cloud, shared: true } };
		const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
		client.setQueryData(workspaceQueryKey, []);
		const ui = () => <QueryClientProvider client={client}><TooltipProvider><CloudSessionChatSurface session={guest} /></TooltipProvider></QueryClientProvider>;
		const view = render(ui());
		expect(await screen.findByRole("combobox", { name: "Message teammates" })).toBeInTheDocument();
		await screen.findByText("Retry the webhooks");
		expect(screen.queryByRole("radiogroup", { name: "Write to" })).toBeNull();
		expect(screen.queryByRole("button", { name: /Allow once/ })).toBeNull();
		expect(screen.queryByRole("button", { name: /Deny/ })).toBeNull();
		// The live stream says Ben may edit: Agent mode and its approval come back.
		mocks.live = { ...live(null), viewers: [{ ...me, role: "editor" }, ben] };
		view.rerender(ui());
		expect(await screen.findByRole("button", { name: /Allow once/ })).toBeInTheDocument();
		expect(screen.getByRole("radiogroup", { name: "Write to" })).toBeInTheDocument();
	});

	it("sends an edited suggestion with the chips it carries, though its aside is older than the loaded history", async () => {
		mocks.live = live({ userId: "user-me", name: "Asha Rao" }, 7);
		mocks.listSessionSuggestions.mockResolvedValue({ items: [{ ...suggestion, text: "@Ben Ortiz also cover the expired cookie", mentions: [{ kind: "person", userId: "user-ben", name: "Ben Ortiz" }] }] });
		mocks.listChatEvents.mockResolvedValue({ events: [], hasMore: false, nextAfter: 0 });
		renderSurface();
		fireEvent.click(await screen.findByRole("button", { name: "Edit" }));
		const editing = await screen.findByRole("combobox", { name: "Message the agent" });
		await waitFor(() => expect(editing.querySelector('[data-composer-token="person"]')).toHaveTextContent("Ben Ortiz"));
		fireEvent.keyDown(editing, { key: "Enter" });
		await waitFor(() => expect(mocks.sendSessionSuggestion).toHaveBeenCalledTimes(1));
		expect(mocks.sendSessionSuggestion.mock.calls[0]![3]).toMatchObject({
			text: "@Ben Ortiz also cover the expired cookie",
			mentions: [{ kind: "person", userId: "user-ben" }],
		});
	});

	it("lets a viewer write asides only, with no switch to a dead end", async () => {
		mocks.live = { ...live(null), viewers: [{ ...me, role: "viewer" }, ben] };
		renderSurface();
		const aside = await screen.findByRole("combobox", { name: "Message teammates" });
		expect(screen.queryByRole("radiogroup", { name: "Write to" })).toBeNull();
		const event = new KeyboardEvent("keydown", { key: "Tab", shiftKey: true, bubbles: true, cancelable: true });
		act(() => { aside.dispatchEvent(event); });
		expect(event.defaultPrevented).toBe(false);
		expect(screen.queryByText("You can view this session but not write to the agent")).toBeNull();
	});
});
