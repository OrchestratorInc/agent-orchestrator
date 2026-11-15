import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { captureFileReference, formatCapturedFileReferences } from "../../../shared/chat-file-reference";
import { cloudAttachmentReference } from "../../lib/cloud-attachments";
import { act, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { agentModelsQueryKey } from "../../hooks/useAgentModelsQuery";
import { settingsQueryKey } from "../../hooks/useSettings";
import type { CloudCpClientEvent } from "../../lib/cloud-cp";
import { CloudCpError } from "../../lib/cloud-cp/errors";
import type { WorkspaceSession } from "../../types/workspace";
import type { ChatDraftExcerptReference } from "../../lib/chat-drafts";
import { sessionUiKey } from "../../lib/hosts";
import { useUiStore } from "../../stores/ui-store";
import type { ChatConfigOption, ChatModel, TurnSettings } from "../../types/conversation";
import type { AgentSwitchProps, PickerSource } from "./AgentSwitchPicker";
import { cloudSessionsQueryKey, sharedCloudWorkspacesQueryKey } from "../../hooks/useWorkspaceQuery";
import { QUEUED_STEER_STALE_MS, cloudSteeringQueuedTurns, nextQueuedSteerDeadline, appendCloudEvents, CHAT_EVENTS_CACHE_MS, cloudExcerptText, cloudAnnotationContent, cloudMessageBody, cloudQueuedSteerErrors, CloudSessionChatSurface, cloudStartupChecklist, cloudWorkspaceFilePath, ownMessageTurns, toSnapshot } from "./CloudSessionChatSurface";
import { CLOUD_CHAT_NEWEST, cloudChatHistoryKey, mergeCloudEvents } from "../../lib/cloud-chat-history";
import { clearPreviousAccountState } from "../../hooks/useCloudAccountSwitchReset";
import { TurnSettingsBar } from "./TurnSettingsBar";

const cloudMocks = vi.hoisted(() => ({
	listChatEvents: vi.fn(),
	sendSessionMessage: vi.fn(),
	cancelTurn: vi.fn(),
	steerTurn: vi.fn(),
	editQueuedMessage: vi.fn(),
	deleteQueuedMessage: vi.fn(),
	steerQueuedMessage: vi.fn(),
	decideChatApproval: vi.fn(),
	listChatModels: vi.fn(),
	listUserAgentModels: vi.fn(),
	resumeSession: vi.fn(),
	requestWorkspaceCheckout: vi.fn(),
	listUserProviderConnections: vi.fn(),
	getSession: vi.fn(),
	getInterfaceTransition: vi.fn(),
	startInterfaceTransition: vi.fn(),
	chatProps: vi.fn(),
}));
// The control plane's base URL and attachment limit, for the tests that attach files.
const cloudSetup = vi.hoisted(() => ({ baseUrl: "", maxAttachmentBytes: undefined as number | undefined, features: [] as string[], agentSwitchHarnesses: [] as string[], meUserId: "user-1" }));
vi.mock("../../hooks/useCloudCp", () => ({
	useCloudCp: () => ({ ready: true, client: cloudMocks, baseUrl: cloudSetup.baseUrl, userId: "user-1" }),
}));
vi.mock("../../hooks/useCloudMeUserId", () => ({ useCloudMeUserId: () => cloudSetup.meUserId }));
vi.mock("../../hooks/useCloudOffering", () => ({
	useCloudOffering: () => ({
		features: [...(cloudSetup.maxAttachmentBytes === undefined ? [] : ["attachments"]), ...cloudSetup.features],
		maxAttachmentBytes: cloudSetup.maxAttachmentBytes,
		agentSwitchHarnesses: cloudSetup.agentSwitchHarnesses,
		chatHarnesses: ["claude-code", "codex", "cursor"],
	}),
}));
vi.mock("./ChatWorkspace", () => ({
	ChatWorkspace: (props: unknown) => {
		cloudMocks.chatProps(props);
		return <div data-testid="cloud-chat" />;
	},
}));
// What the composer's agent and model picker is given, for the settings bar drawn from the chat's props.
const composerPicker = vi.hoisted(() => ({ current: undefined as PickerSource | undefined }));
vi.mock("./AgentSwitchPicker", async (original) => ({
	...await original<typeof import("./AgentSwitchPicker")>(),
	AgentModelPicker: ({ current }: { current: PickerSource }) => {
		composerPicker.current = current;
		return null;
	},
}));

/** The chat's settings bar as ChatWorkspace draws it from these props, by what its picker shows. */
function composerFor(props: { models: ChatModel[]; configOptions?: ChatConfigOption[]; snapshot: { settings: TurnSettings }; agentSwitch?: AgentSwitchProps }): PickerSource {
	composerPicker.current = undefined;
	render(<TurnSettingsBar harness="claude-code" models={props.models} settings={props.snapshot.settings} configOptions={props.configOptions}
		onChange={vi.fn()} onChangeConfigOption={vi.fn()} agentSwitch={props.agentSwitch} />);
	return composerPicker.current!;
}

const session = {
	id: "session-1",
	workspaceId: "project-1",
	workspaceName: "project",
	title: "Cloud session",
	provider: "codex",
	kind: "worker",
	mode: "chat",
	status: "working",
	runtimeConnected: true,
	updatedAt: "2026-09-22T00:00:00Z",
	prs: [],
} satisfies WorkspaceSession;

function capturedCloudExcerpt(): ChatDraftExcerptReference {
	return {
		id: "captured", order: 2, conversationId: `cloud:${session.id}`, messageId: "deleted-source", revision: 0, role: "assistant", text: "缓存",
		snapshot: {
			version: 1, referenceFormat: "numbered-v1", rawText: "**缓存**", captureMode: "exact", sourceTurnId: "source-turn",
			sourceRange: { start: 20, end: 26, offsetUnit: "utf-16" }, renderedRange: { start: 4, end: 6, offsetUnit: "utf-16" }, prefix: "the ", suffix: " rest",
			turn: { id: "source-turn", messages: [{ role: "user", text: "FROZEN_QUESTION\n" + "问".repeat(1000) }, { role: "assistant", text: "FROZEN_ANSWER\n" + "答".repeat(1000) }], truncated: true },
		},
	};
}

describe("CloudSessionChatSurface", () => {
	beforeEach(() => {
		localStorage.clear();
		cloudMocks.listUserAgentModels.mockReset().mockResolvedValue({ models: [] });
		cloudSetup.baseUrl = "";
		cloudSetup.maxAttachmentBytes = undefined;
		cloudSetup.features = [];
		cloudSetup.agentSwitchHarnesses = [];
		cloudSetup.meUserId = "user-1";
	});

	describe("switching the session's agent", () => {
		const cloudSession = { ...session, cloud: { orgId: "org-1", createdByUserId: "user-1" } };
		const transition = {
			id: "transition-1", sessionId: session.id, sourceMode: "chat", targetMode: "chat", policy: "drain", phase: "requested",
			sourceHarness: "codex", targetHarness: "claude-code", createdAt: session.updatedAt, updatedAt: session.updatedAt,
		};
		beforeEach(() => {
			cloudSetup.agentSwitchHarnesses = ["claude-code", "codex", "cursor", "opencode"];
			cloudMocks.listChatEvents.mockReset().mockResolvedValue({ events: [], hasMore: false, nextAfter: 0 });
			cloudMocks.listChatModels.mockReset().mockResolvedValue({ models: [] });
			// Signed in to Claude Code and Cursor; not to opencode, which has no Chat anyway.
			cloudMocks.listUserProviderConnections.mockReset().mockResolvedValue({ providerConnections: [
				{ provider: "claude-code", label: "default", validationState: "valid" },
				{ provider: "cursor", label: "default", validationState: "valid" },
			] });
			cloudMocks.getSession.mockReset().mockResolvedValue({ session: { interfaceMode: "chat" } });
			cloudMocks.getInterfaceTransition.mockReset().mockResolvedValue({ supported: true, targetMode: "tui" });
			cloudMocks.startInterfaceTransition.mockReset().mockResolvedValue({ transition });
			cloudMocks.sendSessionMessage.mockReset().mockResolvedValue({ event: {} });
			cloudMocks.chatProps.mockClear();
		});

		function renderSurface() {
			const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
			return render(<QueryClientProvider client={queryClient}><CloudSessionChatSurface session={cloudSession} /></QueryClientProvider>);
		}

		it("offers the agents the control plane can switch to that the person is signed in to", async () => {
			renderSurface();
			await waitFor(() => expect(cloudMocks.chatProps.mock.lastCall?.[0].agentSwitch.agents.map((agent: { id: string }) => agent.id))
				.toEqual(["claude-code", "codex", "cursor"]));
			expect(cloudMocks.chatProps.mock.lastCall?.[0].agentSwitch).toMatchObject({ disabled: false, cloudOrgId: "org-1", harnessView: "cloud" });
		});

		it("keeps the session's agent where the control plane cannot switch it", async () => {
			cloudSetup.agentSwitchHarnesses = [];
			renderSurface();
			await waitFor(() => expect(cloudMocks.chatProps).toHaveBeenCalled());
			expect(cloudMocks.chatProps.mock.lastCall?.[0].agentSwitch).toMatchObject({ agents: [{ id: "codex" }], disabled: true });
		});

		// The new agent runs on the creator's login for it, and the control plane
		// refuses anyone else (403): a member sees the session's agent, no switch.
		it("offers no switch to a member who did not start the session", async () => {
			cloudSetup.meUserId = "user-2";
			renderSurface();
			// Once the person's logins are in, the creator would be offered Claude Code and Cursor.
			await waitFor(() => expect(cloudMocks.listUserProviderConnections).toHaveBeenCalled());
			await act(async () => { await new Promise((resolve) => setTimeout(resolve, 50)); });
			expect(cloudMocks.chatProps.mock.lastCall?.[0].agentSwitch).toMatchObject({ agents: [{ id: "codex" }], disabled: true });
		});

		it("switches with the next message, which goes to the new agent without the old agent's picks", async () => {
			localStorage.setItem(`cloud-chat-settings:org-1:${session.id}:codex`, JSON.stringify({ model: "gpt-5-codex", reasoningEffort: "high" }));
			renderSurface();
			await waitFor(() => expect(cloudMocks.chatProps.mock.lastCall?.[0].agentSwitch.agents.length).toBeGreaterThan(1));
			act(() => cloudMocks.chatProps.mock.lastCall?.[0].agentSwitch.onPendingChange({ harness: "claude-code", model: "claude-opus-4-5", effort: "high" }));
			await waitFor(() => expect(cloudMocks.chatProps.mock.lastCall?.[0].agentSwitch.pending).toMatchObject({ harness: "claude-code" }));
			await act(async () => { await cloudMocks.chatProps.mock.lastCall?.[0].onSend("now with Claude", undefined, "switch-1"); });
			expect(cloudMocks.startInterfaceTransition).toHaveBeenCalledWith("org-1", session.id,
				{ targetMode: "chat", policy: "drain", targetHarness: "claude-code", model: "claude-opus-4-5", reasoningEffort: "high" });
			expect(cloudMocks.sendSessionMessage).toHaveBeenCalledTimes(1);
			expect(cloudMocks.startInterfaceTransition.mock.invocationCallOrder[0]).toBeLessThan(cloudMocks.sendSessionMessage.mock.invocationCallOrder[0]);
			const request = cloudMocks.sendSessionMessage.mock.calls[0][2];
			expect(request.text).toBe("now with Claude");
			expect(request).not.toHaveProperty("model");
			expect(request).not.toHaveProperty("reasoningEffort");
			await waitFor(() => expect(cloudMocks.chatProps.mock.lastCall?.[0].agentSwitch.pending).toBeUndefined());
		});

		it("keeps the switch's model and effort as the new agent's choice, so its next message sends them", async () => {
			const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
			const surface = (provider: WorkspaceSession["provider"]) => <QueryClientProvider client={queryClient}><CloudSessionChatSurface session={{ ...cloudSession, provider }} /></QueryClientProvider>;
			const view = render(surface("codex"));
			await waitFor(() => expect(cloudMocks.chatProps.mock.lastCall?.[0].agentSwitch.agents.length).toBeGreaterThan(1));
			act(() => cloudMocks.chatProps.mock.lastCall?.[0].agentSwitch.onPendingChange({ harness: "claude-code", model: "sonnet", effort: "low" }));
			await waitFor(() => expect(cloudMocks.chatProps.mock.lastCall?.[0].agentSwitch.pending).toMatchObject({ harness: "claude-code" }));
			await act(async () => { await cloudMocks.chatProps.mock.lastCall?.[0].onSend("now with Claude", undefined, "switch-3"); });
			expect(cloudMocks.startInterfaceTransition).toHaveBeenCalledWith("org-1", session.id,
				expect.objectContaining({ targetHarness: "claude-code", model: "sonnet", reasoningEffort: "low" }));
			// The switch committed: the session now runs Claude Code, on the model and effort it was switched with.
			view.rerender(surface("claude-code"));
			await waitFor(() => expect(cloudMocks.chatProps.mock.lastCall?.[0].snapshot.settings).toMatchObject({ model: "sonnet", reasoningEffort: "low" }));
			await act(async () => { await cloudMocks.chatProps.mock.lastCall?.[0].onSend("and again", undefined, "after-switch"); });
			expect(cloudMocks.sendSessionMessage.mock.lastCall?.[2]).toMatchObject({ text: "and again", model: "sonnet", reasoningEffort: "low" });

			// On Claude the effort becomes High; then to Codex and back to Claude, switched on Low again.
			act(() => cloudMocks.chatProps.mock.lastCall?.[0].onChooseSettings({ ...cloudMocks.chatProps.mock.lastCall?.[0].snapshot.settings, reasoningEffort: "high" }));
			await waitFor(() => expect(cloudMocks.chatProps.mock.lastCall?.[0].snapshot.settings).toMatchObject({ reasoningEffort: "high" }));
			const switchTo = async (harness: string, model: string, provider: WorkspaceSession["provider"]) => {
				act(() => cloudMocks.chatProps.mock.lastCall?.[0].agentSwitch.onPendingChange({ harness, model, effort: "low" }));
				await waitFor(() => expect(cloudMocks.chatProps.mock.lastCall?.[0].agentSwitch.pending).toMatchObject({ harness }));
				await act(async () => { await cloudMocks.chatProps.mock.lastCall?.[0].onSend(`now with ${harness}`, undefined, `switch-${harness}`); });
				view.rerender(surface(provider));
			};
			await switchTo("codex", "gpt-5.6-terra", "codex");
			await waitFor(() => expect(cloudMocks.chatProps.mock.lastCall?.[0].snapshot.settings).toMatchObject({ model: "gpt-5.6-terra", reasoningEffort: "low" }));
			await switchTo("claude-code", "sonnet", "claude-code");
			await waitFor(() => expect(cloudMocks.chatProps.mock.lastCall?.[0].snapshot.settings).toMatchObject({ model: "sonnet", reasoningEffort: "low" }));
			await act(async () => { await cloudMocks.chatProps.mock.lastCall?.[0].onSend("back on Claude", undefined, "after-switch-back"); });
			expect(cloudMocks.sendSessionMessage.mock.lastCall?.[2]).toMatchObject({ text: "back on Claude", model: "sonnet", reasoningEffort: "low" });
		});

		it("keeps the draft and says why when the switch is refused", async () => {
			cloudMocks.startInterfaceTransition.mockReset().mockRejectedValue(
				new CloudCpError("The session's owner has no valid cursor login.", { status: 409, code: "AGENT_LOGIN_REQUIRED" }));
			renderSurface();
			await waitFor(() => expect(cloudMocks.chatProps.mock.lastCall?.[0].agentSwitch.agents.length).toBeGreaterThan(1));
			act(() => cloudMocks.chatProps.mock.lastCall?.[0].agentSwitch.onPendingChange({ harness: "cursor" }));
			await waitFor(() => expect(cloudMocks.chatProps.mock.lastCall?.[0].agentSwitch.pending).toMatchObject({ harness: "cursor" }));
			let failure: unknown;
			await act(async () => {
				failure = await cloudMocks.chatProps.mock.lastCall?.[0].onSend("hi", undefined, "switch-2").catch((error: unknown) => error);
			});
			expect(failure).toMatchObject({ code: "AGENT_SWITCH_FAILED" });
			expect(cloudMocks.sendSessionMessage).not.toHaveBeenCalled();
			await waitFor(() => expect(cloudMocks.chatProps.mock.lastCall?.[0].composerNotice).toBeTruthy());
			expect(cloudMocks.chatProps.mock.lastCall?.[0].agentSwitch.pending).toBeUndefined();
		});
	});

	it("lists the session's agent's models from Cloud's list for the account, not this computer's", async () => {
		const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
		render(<QueryClientProvider client={queryClient}><CloudSessionChatSurface session={{ ...session, cloud: { orgId: "org-1" } }} /></QueryClientProvider>);
		await waitFor(() => expect(cloudMocks.chatProps).toHaveBeenCalled());
		// The composer's agent and model picker reads each agent's list through
		// this organization's Cloud catalog (AgentSwitchPicker).
		expect(cloudMocks.chatProps.mock.lastCall?.[0].agentSwitch).toMatchObject({ cloudOrgId: "org-1", harnessView: "cloud" });
	});

	it("holds a chat it hasn't read yet as one with history, and reopens a closed chat from its cache", async () => {
		const events = [
			{ sessionId: session.id, sequence: 1, type: "chat.user_message", payload: { text: "Why?", turnId: "turn-1" }, createdAt: session.updatedAt },
			{ sessionId: session.id, sequence: 2, type: "chat.assistant_delta", payload: { text: "Because of the cache.", turnId: "turn-1", itemId: "reply" }, createdAt: session.updatedAt },
		];
		let answer!: (page: { events: typeof events; hasMore: boolean; nextAfter: number; hasOlder: boolean; nextBefore: number }) => void;
		cloudMocks.listChatEvents.mockReset().mockImplementationOnce(() => new Promise((resolve) => { answer = resolve; }));
		cloudMocks.listChatModels.mockResolvedValue({ models: [] });
		cloudMocks.chatProps.mockClear();
		const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
		const surface = <QueryClientProvider client={queryClient}><CloudSessionChatSurface session={{ ...session, cloud: { orgId: "org-1" } }} /></QueryClientProvider>;
		const view = render(surface);
		// Unread, the history is unknown rather than empty: no new-chat welcome.
		await waitFor(() => expect(cloudMocks.listChatEvents).toHaveBeenCalledTimes(1));
		expect(cloudMocks.chatProps.mock.lastCall?.[0].historyPending).toBe(true);
		// The whole chat fits the newest page: nothing older to read.
		await act(async () => answer({ events, hasMore: false, nextAfter: 2, hasOlder: false, nextBefore: 1 }));
		await waitFor(() => expect(cloudMocks.chatProps.mock.lastCall?.[0].snapshot.items).toHaveLength(2));
		expect(cloudMocks.chatProps.mock.lastCall?.[0].historyPending).toBe(false);
		view.unmount();
		// Closed, the chat's events outlive React Query's 5 minute default.
		expect(queryClient.getQueryCache().find({ queryKey: ["cloud-chat-events", "org-1", session.id] })?.gcTime).toBe(CHAT_EVENTS_CACHE_MS);
		expect(CHAT_EVENTS_CACHE_MS).toBeGreaterThan(5 * 60_000);
		// Reopened, it shows the cached conversation from its first frame and reads only what is newer.
		cloudMocks.chatProps.mockClear();
		cloudMocks.listChatEvents.mockResolvedValue({ events: [], hasMore: false, nextAfter: 2, hasOlder: false, nextBefore: CLOUD_CHAT_NEWEST });
		render(surface);
		expect(cloudMocks.chatProps.mock.calls[0]?.[0].snapshot.items).toHaveLength(2);
		expect(cloudMocks.chatProps.mock.calls[0]?.[0].historyPending).toBe(false);
		await waitFor(() => expect(cloudMocks.listChatEvents).toHaveBeenLastCalledWith("org-1", session.id, { after: 2, before: CLOUD_CHAT_NEWEST, limit: 500 }));
	});

	it("reads the next account's own chat, never the previous account's cached one", async () => {
		const step = (output: Record<string, unknown>): CloudCpClientEvent => ({
			sessionId: session.id, sequence: 1, type: "workspace.script.step", createdAt: "2026-11-08T10:00:00Z",
			payload: { phase: "setup", runId: "setup-1", step: 1, total: 1, status: "succeeded", exitCode: 0, durationMs: 10, command: "make deps", repository: "", startedAt: "2026-11-08T10:00:00Z", ...output },
		});
		const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
		// A member read the chat: Setup's output, which a share guest is not served.
		queryClient.setQueryData(["cloud-chat-events", "org-1", session.id], [step({ output: "DB host db.internal:5432", user: "ao-worker" })]);
		queryClient.setQueryData(cloudChatHistoryKey("org-1", session.id), { hasOlder: true, nextBefore: 1, recentPending: false, loadingOlder: false, generation: 1 });
		// Another account signs in.
		clearPreviousAccountState(queryClient);
		cloudMocks.listChatEvents.mockReset().mockResolvedValue({ events: [step({ output: "", outputHidden: true })], hasMore: false, nextAfter: 1, hasOlder: false, nextBefore: 1 });
		cloudMocks.listChatModels.mockResolvedValue({ models: [] });
		cloudMocks.chatProps.mockClear();
		render(<QueryClientProvider client={queryClient}><CloudSessionChatSurface session={{ ...session, cloud: { orgId: "org-1" } }} /></QueryClientProvider>);
		// The first frame waits for the read rather than showing the cached chat.
		expect(cloudMocks.chatProps.mock.calls[0]?.[0].historyPending).toBe(true);
		await waitFor(() => expect(cloudMocks.chatProps.mock.lastCall?.[0].historyPending).toBe(false));
		// It reads the newest page from scratch, not only what came after the cached list.
		expect(cloudMocks.listChatEvents.mock.calls[0]?.[2]).toEqual({ before: CLOUD_CHAT_NEWEST, limit: 150 });
		const shown = JSON.stringify(cloudMocks.chatProps.mock.calls.map((call) => call[0].snapshot));
		expect(shown).toContain("make deps");
		expect(shown).not.toContain("db.internal");
		expect(shown).not.toContain("ao-worker");
	});

	it("offers to attach files only while the control plane takes them, up to its limit", async () => {
		cloudMocks.listChatEvents.mockResolvedValue({ events: [], hasMore: false, nextAfter: 0 });
		cloudMocks.listChatModels.mockResolvedValue({ models: [] });
		cloudSetup.baseUrl = "https://cp.example.com";
		const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
		const view = render(<QueryClientProvider client={queryClient}><CloudSessionChatSurface session={{ ...session, cloud: { orgId: "org-1" } }} /></QueryClientProvider>);
		// No attachments feature: no attach control, paste or drop.
		expect(cloudMocks.chatProps.mock.lastCall?.[0].onStageAttachments).toBeUndefined();
		expect(cloudMocks.chatProps.mock.lastCall?.[0].cloudAttachments).toBeUndefined();
		cloudSetup.maxAttachmentBytes = 25 * 1024 * 1024;
		view.rerender(<QueryClientProvider client={queryClient}><CloudSessionChatSurface session={{ ...session, cloud: { orgId: "org-1" } }} /></QueryClientProvider>);
		expect(cloudMocks.chatProps.mock.lastCall?.[0].onStageAttachments).toBeTypeOf("function");
		expect(cloudMocks.chatProps.mock.lastCall?.[0].cloudAttachments).toEqual({ maxBytes: 25 * 1024 * 1024 });
		// A deleted session takes none either.
		view.rerender(<QueryClientProvider client={queryClient}><CloudSessionChatSurface session={{ ...session, isTerminated: true, cloud: { orgId: "org-1" } }} /></QueryClientProvider>);
		expect(cloudMocks.chatProps.mock.lastCall?.[0].onStageAttachments).toBeUndefined();
	});
	it("expands cloud captures once at the text boundary with stable retry identity", async () => {
		cloudMocks.listChatEvents.mockResolvedValue({events:[],hasMore:false,nextAfter:0});
		cloudMocks.listChatModels.mockResolvedValue({models:[]});
		cloudMocks.sendSessionMessage.mockReset().mockRejectedValueOnce(new Error("offline")).mockResolvedValue({event:{}});
		const queryClient=new QueryClient({defaultOptions:{queries:{retry:false},mutations:{retry:false}}});
		render(<QueryClientProvider client={queryClient}><CloudSessionChatSurface session={{...session,cloud:{orgId:"org-1"}}}/></QueryClientProvider>);
		const props=cloudMocks.chatProps.mock.lastCall?.[0];
		expect(props.fileReferenceDelivery).toBe("text");
		expect(props.messageByteLimit).toBe(64 * 1024);
		const ref=captureFileReference({path:"cloud.ts",side:"file",line:1,endLine:1,lines:[{kind:"context",oldNo:1,newNo:1,text:"CLOUD_PROVIDER_MARKER"}]},session.id);
		await expect(props.onSend("question",undefined,"capture-retry",undefined,[ref])).rejects.toThrow("offline");
		await props.onSend("question",undefined,"capture-retry",undefined,[ref]);
		const calls=cloudMocks.sendSessionMessage.mock.calls;
		expect(calls[1]).toEqual(calls[0]);
		expect(calls[0][2].text.match(/CLOUD_PROVIDER_MARKER/g)).toHaveLength(1);
		expect(calls[0][2].text).toContain('"cloud.ts"#L1-L1');
		expect(calls[0][3]).toEqual({idempotencyKey:"capture-retry"});
	});
	it.each(["retry", "fallback"])("keeps frozen background in agent text while omitting it from display content on %s", async (failure) => {
		cloudMocks.listChatEvents.mockResolvedValue({ events: [], hasMore: false, nextAfter: 0 });
		cloudMocks.listChatModels.mockResolvedValue({ models: [] });
		cloudMocks.sendSessionMessage.mockReset().mockRejectedValueOnce(failure === "retry"
			? new Error("offline") : new CloudCpError("unsupported display", { status: 400, code: "invalid_request" })).mockResolvedValue({ event: {} });
		const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
		render(<QueryClientProvider client={queryClient}><CloudSessionChatSurface session={{ ...session, cloud: { orgId: "org-1" } }} /></QueryClientProvider>);
		const props = cloudMocks.chatProps.mock.lastCall?.[0];
		const excerpt = capturedCloudExcerpt();
		const original = structuredClone(excerpt);
		const file = { ...captureFileReference({ path: "cache.ts", side: "file", line: 1, endLine: 1, lines: [{ kind: "context", oldNo: 1, newNo: 1, text: "FILE_MARKER" }] }, session.id), order: 1 };
		const send = () => props.onSend("Explain this", undefined, "frozen-receipt", [excerpt], [file], "Explain this");
		if (failure === "retry") {
			await expect(send()).rejects.toThrow("offline");
			await send();
		} else await send();
		const [first, second] = cloudMocks.sendSessionMessage.mock.calls;
		expect(first[2].text).toContain("FROZEN_QUESTION");
		expect(first[2].text).toContain("FROZEN_ANSWER");
		expect(first[2].text).toContain("FILE_MARKER");
		expect(first[2].text).toContain("**缓存**");
		expect(first[2].content.map((block: { type: string }) => block.type)).toEqual(["file_reference", "excerpt"]);
		expect(first[2].content[1].snapshot).not.toHaveProperty("turn");
		expect(first[2].content[1].snapshot).not.toHaveProperty("contextOmitted");
		expect({ ...first[2].content[1].snapshot, turn: original.snapshot!.turn }).toEqual(original.snapshot);
		expect(first[3]).toEqual({ idempotencyKey: "frozen-receipt" });
		expect(second[3]).toEqual(first[3]);
		if (failure === "retry") expect(second).toEqual(first);
		else {
			expect(second[2]).not.toHaveProperty("content");
			expect(second[2].text).toBe(first[2].text);
		}
		expect(excerpt).toEqual(original);
	});
	it("sends quotes and captures inlined for the agent and as content blocks for the transcript", async () => {
		cloudMocks.listChatEvents.mockResolvedValue({ events: [
			{ sessionId: session.id, sequence: 1, type: "chat.user_message", payload: { text: "Why?", turnId: "turn-1" }, createdAt: session.updatedAt },
			{ sessionId: session.id, sequence: 2, type: "chat.assistant_delta", payload: { text: "Because of the cache.", turnId: "turn-1", itemId: "reply" }, createdAt: session.updatedAt },
		], hasMore: false, nextAfter: 2 });
		cloudMocks.listChatModels.mockResolvedValue({ models: [] });
		cloudMocks.sendSessionMessage.mockReset()
			.mockRejectedValueOnce(new CloudCpError("The request body is invalid.", { status: 400, code: "invalid_request" }))
			.mockResolvedValue({ event: {} });
		const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
		render(<QueryClientProvider client={queryClient}><CloudSessionChatSurface session={{ ...session, cloud: { orgId: "org-1" } }} /></QueryClientProvider>);
		await waitFor(() => expect(cloudMocks.chatProps.mock.lastCall?.[0].snapshot.items).toHaveLength(2));
		const props = cloudMocks.chatProps.mock.lastCall?.[0];
		expect(props.excerptsEnabled).toBe(true);
		expect(props.fileReferencesEnabled).toBe(true);
		expect(props.snapshot.supportsFileReferences).toBe(true);
		const excerpt = { id: "ex-1", order: 0, conversationId: `cloud:${session.id}`, messageId: "cloud-assistant-turn-1:reply", revision: 1, text: "the cache", role: "assistant" as const };
		await props.onSend("Explain this", undefined, "quote-1", [excerpt], undefined, "Explain this");
		const [first, second] = cloudMocks.sendSessionMessage.mock.calls;
		expect(first[2].text).toContain("Selected text:\n---\nthe cache");
		expect(first[2].text).toContain("Full paired turn:\nUser:\nWhy?\nAssistant:\nBecause of the cache.");
		expect(first[2].displayText).toBe("Explain this");
		expect(first[2].content).toEqual([expect.objectContaining({ type: "excerpt", text: "the cache", sourceMessageId: excerpt.messageId })]);
		// An older control plane refuses the display; the same text goes alone, with the same key.
		expect(second[2]).not.toHaveProperty("content");
		expect(second[2].text).toBe(first[2].text);
		expect(second[3]).toEqual({ idempotencyKey: "quote-1" });
	});

	it("sends text that holds the typed message with its quotes and captures inlined", async () => {
		cloudMocks.listChatEvents.mockResolvedValue({ events: [
			{ sessionId: session.id, sequence: 1, type: "chat.user_message", payload: { text: "Why?", turnId: "turn-1" }, createdAt: session.updatedAt },
			{ sessionId: session.id, sequence: 2, type: "chat.assistant_delta", payload: { text: "Because of the cache.", turnId: "turn-1", itemId: "reply" }, createdAt: session.updatedAt },
		], hasMore: false, nextAfter: 2 });
		cloudMocks.listChatModels.mockResolvedValue({ models: [] });
		cloudMocks.sendSessionMessage.mockReset().mockResolvedValue({ event: {} });
		const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
		render(<QueryClientProvider client={queryClient}><CloudSessionChatSurface session={{ ...session, cloud: { orgId: "org-1" } }} /></QueryClientProvider>);
		await waitFor(() => expect(cloudMocks.chatProps.mock.lastCall?.[0].snapshot.items).toHaveLength(2));
		const props = cloudMocks.chatProps.mock.lastCall?.[0];
		const excerpt = { id: "ex-1", order: 0, conversationId: `cloud:${session.id}`, messageId: "cloud-assistant-turn-1:reply", revision: 1, text: "the cache", role: "assistant" as const };
		const capture = captureFileReference({ path: "cache.ts", side: "file", line: 3, endLine: 3, lines: [{ kind: "context", oldNo: 3, newNo: 3, text: "const ttl = 60;" }] }, session.id);
		// The composer inlines the captures into the text and hands over the message as typed.
		await props.onSend("Is this right?\n\nUser-supplied captured code ...", undefined, "both-1", [excerpt], [capture], "Is this right?");
		// A caller that hands the captures over separately gets them inlined here.
		await props.onSend("And this?", undefined, "both-2", [excerpt], [capture]);
		for (const [, , body] of cloudMocks.sendSessionMessage.mock.calls) {
			expect(body.text).toContain(body.displayText);
			expect(body.text).toContain("Selected text:\n---\nthe cache");
			expect(body.content).toHaveLength(2);
		}
		expect(cloudMocks.sendSessionMessage.mock.calls[1][2].displayText).toBe("And this?");
		expect(cloudMocks.sendSessionMessage.mock.calls[1][2].text).toContain("const ttl = 60;");
	});

	it("sends a message's files as attachment ids when code captures follow them, with no reference for the agent or the transcript", async () => {
		cloudMocks.listChatEvents.mockResolvedValue({ events: [], hasMore: false, nextAfter: 0 });
		cloudMocks.listChatModels.mockResolvedValue({ models: [] });
		cloudMocks.sendSessionMessage.mockReset().mockResolvedValue({ event: {} });
		const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
		render(<QueryClientProvider client={queryClient}><CloudSessionChatSurface session={{ ...session, cloud: { orgId: "org-1" } }} /></QueryClientProvider>);
		const props = cloudMocks.chatProps.mock.lastCall?.[0];
		const attachmentId = "6f1c2a8e-0b1d-4c3e-9f5a-2d7e8b9c0a11";
		const capture = captureFileReference({ path: "cache.ts", side: "file", line: 3, endLine: 3, lines: [{ kind: "context", oldNo: 3, newNo: 3, text: "const ttl = 60;" }] }, session.id);
		const excerpt = { id: "ex-1", order: 1, conversationId: `cloud:${session.id}`, messageId: "m-1", revision: 1, text: "the cache", role: "assistant" as const };
		// As the composer sends it: the staged files listed under the typed
		// text, the captures inlined after them, and the request text beside.
		const requestText = `Is this right?\n\nAttached files (read these files in the workspace):\n- ${cloudAttachmentReference({ id: attachmentId, fileName: "notes.txt" })}`;
		await props.onSend(formatCapturedFileReferences(requestText, [capture]), undefined, "files-capture-1", [excerpt], [capture], requestText);
		// Files only, with a capture and no typed text.
		const onlyFiles = `Attached files (read these files in the workspace):\n- ${cloudAttachmentReference({ id: attachmentId, fileName: "notes.txt" })}`;
		await props.onSend(formatCapturedFileReferences(onlyFiles, [capture]), undefined, "files-capture-2", undefined, [capture], onlyFiles);
		const [[, , first], [, , second]] = cloudMocks.sendSessionMessage.mock.calls;
		expect(first.attachmentIds).toEqual([attachmentId]);
		expect(first.displayText).toBe("Is this right?");
		expect(first.text.startsWith("Is this right?\n\nReferenced annotations.")).toBe(true);
		expect(first.text).toContain("const ttl = 60;");
		expect(first.text).toContain("Selected text:\n---\nthe cache");
		expect(first.content).toHaveLength(2);
		expect(second.attachmentIds).toEqual([attachmentId]);
		expect(second).not.toHaveProperty("displayText");
		expect(second.text.startsWith("Referenced annotations.")).toBe(true);
		for (const body of [first, second]) {
			expect(JSON.stringify(body)).not.toContain("ao-cloud-attachment:");
			expect(JSON.stringify(body)).not.toContain("Attached files");
		}
	});

	it("goes as text alone when the control plane refuses its display text", async () => {
		cloudMocks.listChatEvents.mockResolvedValue({ events: [], hasMore: false, nextAfter: 0 });
		cloudMocks.listChatModels.mockResolvedValue({ models: [] });
		cloudMocks.sendSessionMessage.mockReset()
			.mockRejectedValueOnce(new CloudCpError("displayText must be part of text.", { status: 400, code: "validation_error" }))
			.mockResolvedValue({ event: {} });
		const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
		render(<QueryClientProvider client={queryClient}><CloudSessionChatSurface session={{ ...session, cloud: { orgId: "org-1" } }} /></QueryClientProvider>);
		const props = cloudMocks.chatProps.mock.lastCall?.[0];
		const capture = captureFileReference({ path: "a.ts", side: "file", line: 1, endLine: 1, lines: [{ kind: "context", oldNo: 1, newNo: 1, text: "x" }] }, session.id);
		await props.onSend("Check", undefined, "refused-1", undefined, [capture]);
		const [first, second] = cloudMocks.sendSessionMessage.mock.calls;
		expect(first[2]).toMatchObject({ displayText: "Check" });
		expect(second[2]).not.toHaveProperty("displayText");
		expect(second[2]).not.toHaveProperty("content");
		expect(second[2].text).toBe(first[2].text);
		expect(second[3]).toEqual({ idempotencyKey: "refused-1" });
	});

	it("never sends a display text its text does not hold", () => {
		const content = [{ type: "excerpt", text: "q" }];
		expect(cloudMessageBody("Explain this\n\nquote", { text: "Explain this", content })).toEqual({ text: "Explain this\n\nquote", displayText: "Explain this", content });
		// Only captures, nothing typed: the transcript shows the captures alone.
		expect(cloudMessageBody("captured", { text: "", content })).toEqual({ text: "captured", content });
		expect(cloudMessageBody("Explain\u00a0this", { text: "Explain this", content })).toEqual({ text: "Explain\u00a0this" });
		expect(cloudMessageBody("plain")).toEqual({ text: "plain" });
	});

	it("shows a message with recorded quotes as typed, with its quotes beside it", () => {
		const snapshot = toSnapshot(session, [{
			sessionId: session.id, sequence: 1, type: "chat.user_message",
			payload: { text: "Explain this\n\nReferenced chat excerpt...", displayText: "Explain this", content: [{ type: "excerpt", text: "the cache", annotationId: "ex-1", sourceMessageId: "m-1" }, { type: "unknown" }] },
			createdAt: session.updatedAt,
		}]);
		expect(snapshot.items[0]).toMatchObject({ text: "Explain this", content: [{ type: "excerpt", text: "the cache", annotationId: "ex-1", sourceMessageId: "m-1" }] });
	});
	it("retains a chart-only annotation when history omits its empty display text", () => {
		const chart = { version: 1, referenceFormat: "numbered-v1", captureMode: "block", containsFlowchart: true, badgeOnly: true, rawText: "```mermaid\nflowchart LR; A-->B\n```" };
		const snapshot = toSnapshot(session, [{ sessionId: session.id, sequence: 1, type: "chat.user_message", payload: { text: "Explain the chart", content: [{ type: "excerpt", annotationId: "chart", snapshot: chart }] }, createdAt: session.updatedAt }]);
		expect(snapshot.items[0]).toMatchObject({ content: [{ type: "excerpt", text: "", snapshot: chart }] });
	});

	it("shows a message of only files, with its files and no text", () => {
		const files = [
			{ id: "a1", fileName: "shot.png", contentType: "image/png", size: 6615, sha256: "0".repeat(64) },
			{ id: "a2", fileName: "spec.pdf", contentType: "application/pdf", size: 13313 },
		];
		const snapshot = toSnapshot(session, [
			{ sessionId: session.id, sequence: 1, type: "chat.user_message", payload: { text: "", turnId: "turn-1", attachments: files }, createdAt: session.updatedAt },
			// A malformed entry is dropped, not shown.
			{ sessionId: session.id, sequence: 2, type: "chat.user_message", payload: { text: "   ", turnId: "turn-2", attachments: [{ id: "bad" }, null] }, createdAt: session.updatedAt },
			{ sessionId: session.id, sequence: 3, type: "chat.user_message", payload: { text: "And this one", turnId: "turn-3", attachments: [files[1]] }, createdAt: session.updatedAt },
		]);
		const messages = snapshot.items.filter((item) => item.kind === "message");
		expect(messages).toHaveLength(2);
		expect(messages[0]).toMatchObject({ role: "user", text: "", turnId: "turn-1", cloudAttachments: [
			{ id: "a1", fileName: "shot.png", contentType: "image/png", size: 6615 },
			{ id: "a2", fileName: "spec.pdf", contentType: "application/pdf", size: 13313 },
		] });
		expect(messages[1]).toMatchObject({ text: "And this one", cloudAttachments: [{ id: "a2", fileName: "spec.pdf" }] });
		// A message with neither text nor files is still not shown.
		expect(toSnapshot(session, [
			{ sessionId: session.id, sequence: 1, type: "chat.user_message", payload: { text: "", turnId: "turn-1" }, createdAt: session.updatedAt },
		]).items.filter((item) => item.kind === "message")).toEqual([]);
	});

	// An edit of a queued message reads in its place: the new text and files,
	// its quotes gone. It opens no turn of its own, and one whose message is on
	// an older page, not loaded, changes nothing here.
	it("shows a queued message as edited", () => {
		const files = [{ id: "a2", fileName: "spec.pdf", contentType: "application/pdf", size: 13313 }];
		const snapshot = toSnapshot(session, [
			{ sessionId: session.id, sequence: 1, type: "chat.user_message", payload: { text: "Explain this\n\nquote", displayText: "Explain this", content: [{ type: "excerpt", text: "q" }], turnId: "turn-1", attachments: [{ id: "a1", fileName: "shot.png", contentType: "image/png", size: 1 }] }, createdAt: session.updatedAt },
			{ sessionId: session.id, sequence: 2, type: "chat.user_message_edited", payload: { turnId: "turn-1", messageSequence: 1, text: "Explain that", attachments: files }, createdAt: session.updatedAt },
			{ sessionId: session.id, sequence: 3, type: "chat.user_message_edited", payload: { turnId: "turn-0", messageSequence: 0, text: "older" }, createdAt: session.updatedAt },
		], undefined, { partial: true });
		const messages = snapshot.items.filter((item) => item.kind === "message");
		expect(messages).toHaveLength(1);
		expect(messages[0]).toMatchObject({ turnId: "turn-1", text: "Explain that", revision: 2, cloudAttachments: files });
		expect(messages[0]).not.toHaveProperty("content");
		expect(snapshot.turns.map((turn) => turn.id)).toEqual(["turn-1"]);
		const withoutFiles = toSnapshot(session, [
			{ sessionId: session.id, sequence: 1, type: "chat.user_message", payload: { text: "first", turnId: "turn-1", attachments: files }, createdAt: session.updatedAt },
			{ sessionId: session.id, sequence: 2, type: "chat.user_message_edited", payload: { turnId: "turn-1", messageSequence: 1, text: "second" }, createdAt: session.updatedAt },
		]).items[0];
		expect(withoutFiles).toMatchObject({ text: "second" });
		expect(withoutFiles).not.toHaveProperty("cloudAttachments");
	});

	// The queue editor lets go of an edit only for a refusal that proves it was
	// not applied; any other failure keeps it pending, to retry, with its
	// Cancel and the message's own Cancel held. A control plane's refusal is
	// such proof, so the edit is handed back with its reason.
	it("hands a refused queued edit back to the editor, and keeps one without an answer pending", async () => {
		cloudSetup.features = ["queued-edit"];
		cloudMocks.listChatEvents.mockReset().mockResolvedValue({ events: [], hasMore: false, nextAfter: 0, hasOlder: false, nextBefore: 0 });
		cloudMocks.listChatModels.mockResolvedValue({ models: [] });
		cloudMocks.chatProps.mockClear();
		const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
		render(<QueryClientProvider client={queryClient}><CloudSessionChatSurface session={{ ...session, cloud: { orgId: "org-1" } }} /></QueryClientProvider>);
		await waitFor(() => expect(cloudMocks.chatProps.mock.lastCall?.[0].onEditQueuedTurn).toEqual(expect.any(Function)));
		const edit = (text: string) => cloudMocks.chatProps.mock.lastCall?.[0].onEditQueuedTurn("turn-1", text) as Promise<unknown>;
		const file = { id: "a1", fileName: "shot.png", contentType: "image/png", size: 1 };

		cloudMocks.editQueuedMessage.mockReset().mockResolvedValue({ event: {} });
		await act(async () => { await edit(`Use the parser\n\nAttached files (read these files in the workspace):\n- ${cloudAttachmentReference(file)}`); });
		expect(cloudMocks.editQueuedMessage).toHaveBeenCalledWith("org-1", session.id, "turn-1", { text: "Use the parser", attachmentIds: ["a1"] });

		const outcome = async (error: Error) => {
			cloudMocks.editQueuedMessage.mockReset().mockRejectedValue(error);
			let thrown: unknown;
			await act(async () => { thrown = await edit("Use the parser").catch((reason: unknown) => reason); });
			return thrown;
		};
		// The control plane's own words, true of a turn the agent took and of one that was stopped.
		expect(await outcome(new CloudCpError("This message is no longer queued: the agent started on it, or it was stopped. It can no longer be edited.", { status: 409, code: "TURN_NOT_QUEUED" })))
			.toMatchObject({ code: "CHAT_TURN_NOT_QUEUED", message: "This message is no longer queued: the agent started on it, or it was stopped. It can no longer be edited." });
		// A session that ended meanwhile lets go of the message the same way, said for an edit.
		expect(await outcome(new CloudCpError("This session was deleted. Restore it to continue.", { status: 409, code: "SESSION_TERMINATED" })))
			.toMatchObject({ code: "CHAT_TURN_NOT_QUEUED", message: "This session has ended, so this message can no longer be edited." });
		expect(await outcome(new CloudCpError("text must be at most 65536 bytes", { status: 422, code: "validation_error" })))
			.toMatchObject({ code: "CHAT_QUEUED_EDIT_REFUSED", message: "text must be at most 65536 bytes" });
		expect(await outcome(new CloudCpError("This session's sandbox runs an older AO worker that cannot receive attached files.", { status: 409, code: "ATTACHMENTS_UNSUPPORTED" })))
			.toMatchObject({ code: "CHAT_QUEUED_EDIT_REFUSED" });
		// A server error or a lost answer says nothing about the edit.
		expect(await outcome(new CloudCpError("The request could not be completed.", { status: 500, code: "internal_error" }))).toMatchObject({ code: "internal_error" });
		expect(await outcome(new CloudCpError("Too many requests.", { status: 429, code: "rate_limited" }))).toMatchObject({ code: "rate_limited" });
		expect(await outcome(new TypeError("Failed to fetch"))).not.toHaveProperty("code");
	});

	// The control plane edits any queued message of its author, and drops the
	// quotes and captures it recorded; the agent got them inlined in its text,
	// which the composer cannot rebuild. So Edit is offered only on the
	// viewer's own messages without them.
	it("offers Edit on the viewer's own queued messages, not on a teammate's or one with quotes", async () => {
		cloudSetup.features = ["queued-edit"];
		const message = (sequence: number, turnId: string, payload: Record<string, unknown>) =>
			({ sessionId: session.id, sequence, type: "chat.user_message", payload: { turnId, ...payload }, createdAt: session.updatedAt });
		const events = [
			{ sessionId: session.id, sequence: 1, type: "chat.turn_started", payload: { turnId: "running" }, createdAt: session.updatedAt },
			message(2, "plain", { text: "mine", authorUserId: "user-1" }),
			message(3, "quoted", { text: "Explain this\n\nquote", displayText: "Explain this", content: [{ type: "excerpt", text: "quote" }], authorUserId: "user-1" }),
			message(4, "theirs", { text: "theirs", authorUserId: "teammate" }),
		];
		cloudMocks.listChatEvents.mockReset().mockResolvedValue({ events, hasMore: false, nextAfter: 4, hasOlder: false, nextBefore: 1 });
		cloudMocks.listChatModels.mockResolvedValue({ models: [] });
		cloudMocks.chatProps.mockClear();
		const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
		render(<QueryClientProvider client={queryClient}><CloudSessionChatSurface session={{ ...session, cloud: { orgId: "org-1" } }} /></QueryClientProvider>);
		await waitFor(() => expect(cloudMocks.chatProps.mock.lastCall?.[0].snapshot.items.filter((item: { kind: string }) => item.kind === "message")).toHaveLength(3));
		const props = cloudMocks.chatProps.mock.lastCall?.[0];
		expect(props.onEditQueuedTurn).toEqual(expect.any(Function));
		const editable = Object.fromEntries(props.snapshot.items
			.filter((item: { kind: string }) => item.kind === "message")
			.map((item: { turnId: string }) => [item.turnId, props.canEditQueuedMessage(item)]));
		expect(editable).toEqual({ plain: true, quoted: false, theirs: false });
	});

	// Delete stops a queued message's turn before the agent starts on it
	// (cancelTurn). The control plane lets its author, the session's creator,
	// the organization's owners and admins and the composer lease holder do
	// that, so Delete shows for those, and a refusal reads for a person.
	it("deletes a queued message by stopping its turn, where the control plane lets this person", async () => {
		const message = (sequence: number, turnId: string, authorUserId: string) =>
			({ sessionId: session.id, sequence, type: "chat.user_message", payload: { turnId, text: turnId, authorUserId }, createdAt: session.updatedAt });
		const events = [
			{ sessionId: session.id, sequence: 1, type: "chat.turn_started", payload: { turnId: "running" }, createdAt: session.updatedAt },
			message(2, "mine", "user-1"),
			message(3, "theirs", "teammate"),
		];
		cloudMocks.listChatEvents.mockReset().mockResolvedValue({ events, hasMore: false, nextAfter: 3, hasOlder: false, nextBefore: 1 });
		cloudMocks.listChatModels.mockResolvedValue({ models: [] });
		const deletable = async (cloud: { orgId: string; createdByUserId?: string }) => {
			cloudMocks.chatProps.mockClear();
			const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
			const view = render(<QueryClientProvider client={queryClient}><CloudSessionChatSurface session={{ ...session, cloud }} /></QueryClientProvider>);
			await waitFor(() => expect(cloudMocks.chatProps.mock.lastCall?.[0].snapshot.items.filter((item: { kind: string }) => item.kind === "message")).toHaveLength(2));
			const props = cloudMocks.chatProps.mock.lastCall?.[0];
			const verdicts = Object.fromEntries(props.snapshot.items
				.filter((item: { kind: string }) => item.kind === "message")
				.map((item: { turnId: string }) => [item.turnId, props.canCancelQueuedMessage(item)]));
			return { props, verdicts, view };
		};
		// A teammate started the session: only the viewer's own message.
		const asMember = await deletable({ orgId: "org-1", createdByUserId: "teammate" });
		expect(asMember.verdicts).toEqual({ mine: true, theirs: false });
		asMember.view.unmount();
		// The viewer started it: every queued message.
		const asCreator = await deletable({ orgId: "org-1", createdByUserId: "user-1" });
		expect(asCreator.verdicts).toEqual({ mine: true, theirs: true });

		const cancel = (turnId: string) => asCreator.props.onCancelQueuedTurn(turnId) as Promise<unknown>;
		cloudMocks.cancelTurn.mockReset().mockResolvedValue({ ok: true });
		cloudMocks.deleteQueuedMessage.mockReset();
		await act(async () => { await cancel("mine"); });
		// A control plane without queued-delete only stops turns.
		expect(cloudMocks.cancelTurn).toHaveBeenCalledWith("org-1", session.id, "mine");
		expect(cloudMocks.deleteQueuedMessage).not.toHaveBeenCalled();
		const outcome = async (error: Error) => {
			cloudMocks.cancelTurn.mockReset().mockRejectedValue(error);
			let thrown: unknown = "resolved";
			await act(async () => { thrown = await cancel("mine").then(() => "resolved", (reason: unknown) => reason); });
			return thrown;
		};
		// It ran meanwhile: nothing is left to delete, and the dock lets it go.
		expect(await outcome(new CloudCpError("The turn is already finished.", { status: 409, code: "TURN_FINISHED" }))).toBe("resolved");
		expect(await outcome(new CloudCpError("This session was deleted. Restore it to continue.", { status: 409, code: "SESSION_TERMINATED" })))
			.toMatchObject({ message: "This session has ended, so this message can no longer be deleted." });
		expect(await outcome(new CloudCpError("You do not have access to this organization.", { status: 403, code: "forbidden" })))
			.toMatchObject({ message: "Only the person who sent this message, whoever started the session, or a workspace admin can delete it." });
		expect(await outcome(new CloudCpError("The request could not be completed.", { status: 500, code: "internal_error" })))
			.toMatchObject({ message: "The request could not be completed." });
	});

	// Where the control plane offers queued-delete, Delete takes the message
	// back only while it is queued (deleteQueuedMessage) and never stops a turn
	// the agent took meanwhile, as a local chat's Delete does: that answers 409
	// TURN_NOT_QUEUED, which reads for a person and lets the row go.
	it("deletes a queued message without stopping a started turn where the control plane offers it", async () => {
		cloudSetup.features = ["queued-delete"];
		cloudMocks.listChatEvents.mockReset().mockResolvedValue({ events: [
			{ sessionId: session.id, sequence: 1, type: "chat.turn_started", payload: { turnId: "running" }, createdAt: session.updatedAt },
			{ sessionId: session.id, sequence: 2, type: "chat.user_message", payload: { turnId: "mine", text: "mine", authorUserId: "user-1" }, createdAt: session.updatedAt },
		], hasMore: false, nextAfter: 2, hasOlder: false, nextBefore: 1 });
		cloudMocks.listChatModels.mockResolvedValue({ models: [] });
		cloudMocks.chatProps.mockClear();
		const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
		render(<QueryClientProvider client={queryClient}><CloudSessionChatSurface session={{ ...session, cloud: { orgId: "org-1" } }} /></QueryClientProvider>);
		await waitFor(() => expect(cloudMocks.chatProps.mock.lastCall?.[0].onCancelQueuedTurn).toEqual(expect.any(Function)));
		const cancel = (turnId: string) => cloudMocks.chatProps.mock.lastCall?.[0].onCancelQueuedTurn(turnId) as Promise<unknown>;
		cloudMocks.cancelTurn.mockReset();
		cloudMocks.deleteQueuedMessage.mockReset().mockResolvedValue({ ok: true });
		await act(async () => { await cancel("mine"); });
		expect(cloudMocks.deleteQueuedMessage).toHaveBeenCalledWith("org-1", session.id, "mine");
		expect(cloudMocks.cancelTurn).not.toHaveBeenCalled();
		const outcome = async (error: Error) => {
			cloudMocks.deleteQueuedMessage.mockReset().mockRejectedValue(error);
			let thrown: unknown = "resolved";
			await act(async () => { thrown = await cancel("mine").then(() => "resolved", (reason: unknown) => reason); });
			return thrown;
		};
		expect(await outcome(new CloudCpError("This message is no longer queued: the agent started on it, or it was stopped. It can no longer be deleted.", { status: 409, code: "TURN_NOT_QUEUED" })))
			.toMatchObject({ message: "This message is no longer queued, so it was not deleted. If the agent started on it, stop that turn instead." });
		expect(await outcome(new CloudCpError("This session was deleted. Restore it to continue.", { status: 409, code: "SESSION_TERMINATED" })))
			.toMatchObject({ message: "This session has ended, so this message can no longer be deleted." });
		expect(cloudMocks.cancelTurn).not.toHaveBeenCalled();
	});

	// A message the person deleted from the queue leaves the conversation, as a
	// local chat drops a queued message deleted from its dock: no bubble, no
	// "Worked for", no "interrupted by you". The control plane ends it with
	// turn_interrupted of reason deleted, so it is known on a page without its
	// message too.
	const queueEvents = (reason?: string) => {
		const at = (sequence: number, type: string, payload: Record<string, unknown>) =>
			({ sessionId: session.id, sequence, type, payload, createdAt: `2026-11-13T09:0${sequence}:00Z` });
		return [
			at(1, "chat.user_message", { text: "long job", turnId: "turn-run" }),
			at(2, "chat.turn_started", { turnId: "turn-run", attempt: 1 }),
			at(3, "chat.user_message", { text: "ended while queued", turnId: "turn-ended" }),
			at(4, "chat.user_message", { text: "still queued", turnId: "turn-next" }),
			at(5, "chat.turn_interrupted", { turnId: "turn-ended", attempt: 0, ...(reason ? { reason } : {}) }),
		];
	};
	it("drops a queued message the person deleted, on a page without it too", () => {
		const events = queueEvents("deleted");
		const full = toSnapshot(session, events);
		expect(full.items.filter((item) => item.kind === "message").map((item) => item.turnId)).toEqual(["turn-run", "turn-next"]);
		expect(full.turns.map((turn) => [turn.id, turn.state])).toEqual([["turn-run", "running"], ["turn-ended", "cancelled"], ["turn-next", "queued"]]);
		const partial = toSnapshot(session, events.slice(4), undefined, { partial: true });
		expect(partial.items).toEqual([]);
		expect(partial.turns.find((turn) => turn.id === "turn-ended")?.state).toBe("cancelled");
	});

	// A queued message steered into the running turn reads from the events, so
	// a remount (a tab switch, a screen change) shows what the control plane
	// holds: steering while the agent has not answered, the steer in the turn
	// once it took it, and the message back in the queue, with the reason,
	// when it did not. It was once kept only in the view, and a remount put a
	// message shown as steered back in the queue.
	const steerEvents = () => {
		const at = (sequence: number, type: string, payload: Record<string, unknown>) =>
			({ sessionId: session.id, sequence, type, payload, createdAt: `2026-11-13T09:0${sequence}:00Z` });
		const requested = { turnId: "turn-run", text: "report progress now", clientMessageId: "key-1", queuedTurnId: "turn-orch" };
		return {
			queued: [
				at(1, "chat.user_message", { text: "long job", turnId: "turn-run" }),
				at(2, "chat.turn_started", { turnId: "turn-run", attempt: 1 }),
				at(3, "chat.turn_capabilities", { turnId: "turn-run", attempt: 1, steering: true }),
				at(4, "chat.user_message", { text: "report progress now", turnId: "turn-orch", origin: "automation", senderLabel: "Orchestrator", displayText: "report progress now" }),
				at(5, "chat.user_message", { text: "mine, later", turnId: "turn-mine", authorUserId: "user-1" }),
				at(6, "chat.turn_steer_requested", requested),
			],
			steered: [
				at(7, "chat.turn_steered", { ...requested, origin: "automation", senderLabel: "Orchestrator" }),
				at(8, "chat.turn_interrupted", { turnId: "turn-orch", attempt: 0, reason: "steered" }),
			],
			failed: [at(7, "chat.turn_steer_failed", { ...requested, origin: "automation", senderLabel: "Orchestrator", error: "The agent finished first." })],
		};
	};
	const queuedTurns = (snapshot: ReturnType<typeof toSnapshot>) =>
		snapshot.turns.filter((turn) => turn.state === "queued" && snapshot.items.some((item) => item.kind === "message" && item.turnId === turn.id)).map((turn) => turn.id);
	const steers = (snapshot: ReturnType<typeof toSnapshot>) =>
		snapshot.items.filter((item) => item.kind === "activity" && item.detail?.event === "steer")
			.map((item) => item.kind === "activity" ? { turnId: item.turnId, text: item.detail?.text, origin: item.detail?.origin, from: item.detail?.senderDisplayName } : undefined);

	it("keeps a queued message being steered in its place in the queue, marked steering, on every read", () => {
		const { queued } = steerEvents();
		const snapshot = toSnapshot(session, queued);
		expect(queuedTurns(snapshot)).toEqual(["turn-orch", "turn-mine"]);
		expect(steers(snapshot)).toEqual([]);
		expect([...cloudSteeringQueuedTurns(queued)]).toEqual(["turn-orch"]);
		expect(cloudQueuedSteerErrors(queued)).toEqual({});
		// A remount reads the same events, and the same chat.
		expect(toSnapshot(session, [...queued])).toEqual(snapshot);
	});

	it("drops a steered message from the queue and keeps its steer in the running turn, as its sender's", () => {
		const { queued, steered } = steerEvents();
		const snapshot = toSnapshot(session, [...queued, ...steered]);
		expect(queuedTurns(snapshot)).toEqual(["turn-mine"]);
		expect(snapshot.items.some((item) => item.kind === "message" && item.turnId === "turn-orch")).toBe(false);
		expect(snapshot.turns.find((turn) => turn.id === "turn-orch")?.state).toBe("cancelled");
		expect(steers(snapshot)).toEqual([{ turnId: "turn-run", text: "report progress now", origin: "automation", from: "Orchestrator" }]);
		// On a page holding only the outcome too.
		const partial = toSnapshot(session, steered, undefined, { partial: true });
		expect(partial.turns.find((turn) => turn.id === "turn-orch")?.state).toBe("cancelled");
	});

	it("puts a message whose steer failed back in the queue, in its place, saying why", () => {
		const { queued, failed } = steerEvents();
		const events = [...queued, ...failed];
		const snapshot = toSnapshot(session, events);
		expect(queuedTurns(snapshot)).toEqual(["turn-orch", "turn-mine"]);
		expect(steers(snapshot)).toEqual([]);
		expect(cloudQueuedSteerErrors(events)).toEqual({ "turn-orch": "Couldn't steer: The agent finished first. It's back in the queue; select Steer to try again." });
		// Steering it again clears the reason.
		expect(cloudQueuedSteerErrors([...events, { ...queued[5], sequence: 8 }])).toEqual({});
	});

	// The queue's Steer is offered only where the control plane records it
	// (queued-steer) and the running turn can take guidance; it asks the
	// control plane, and a refusal comes back as the row's error rather than
	// as a steer that never happened.
	it("steers a queued message through the control plane only where it offers queued-steer", async () => {
		const { queued, failed } = steerEvents();
		const events = queued.slice(0, 5);
		cloudMocks.listChatEvents.mockReset().mockResolvedValue({ events, hasMore: false, nextAfter: 5, hasOlder: false, nextBefore: 1 });
		cloudMocks.listChatModels.mockResolvedValue({ models: [] });
		const mount = async () => {
			cloudMocks.chatProps.mockClear();
			const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
			const view = render(<QueryClientProvider client={queryClient}><CloudSessionChatSurface session={{ ...session, cloud: { orgId: "org-1" } }} /></QueryClientProvider>);
			await waitFor(() => expect(cloudMocks.chatProps.mock.lastCall?.[0].onSteer).toEqual(expect.any(Function)));
			return view;
		};
		const older = await mount();
		expect(cloudMocks.chatProps.mock.lastCall?.[0].onPromoteQueuedTurn).toBeUndefined();
		older.unmount();

		cloudSetup.features = ["queued-steer"];
		await mount();
		const promote = cloudMocks.chatProps.mock.lastCall?.[0].onPromoteQueuedTurn as (turnId: string) => Promise<unknown>;
		expect(promote).toEqual(expect.any(Function));
		cloudMocks.steerQueuedMessage.mockReset().mockResolvedValue({ event: queued[5] });
		cloudMocks.listChatEvents.mockReset().mockResolvedValue({ events: failed, hasMore: false, nextAfter: 7, hasOlder: false, nextBefore: 7 });
		let outcome: unknown;
		await act(async () => { outcome = await promote("turn-orch").then(() => "steered", (error: unknown) => error); });
		expect(cloudMocks.steerQueuedMessage).toHaveBeenCalledWith("org-1", session.id, "turn-orch", {}, { idempotencyKey: expect.any(String) });
		expect(outcome).toMatchObject({ message: "Couldn't steer: The agent finished first. It's back in the queue; select Steer to try again." });

		cloudMocks.listChatEvents.mockReset().mockResolvedValue({ events: steerEvents().steered, hasMore: false, nextAfter: 8, hasOlder: false, nextBefore: 7 });
		await act(async () => { outcome = await promote("turn-orch").then(() => "steered", (error: unknown) => error); });
		expect(outcome).toBe("steered");
	});

	// A steer the agent never answered (the worker went silent before the
	// control plane recorded the expiry) is not read as steering forever: past
	// the time it could be answered in, the message is back in the queue with
	// why.
	it("reads a queued steer left unanswered too long as failed, with the message back in the queue", () => {
		const { queued } = steerEvents();
		const requestedAt = Date.parse(queued[5].createdAt);
		expect([...cloudSteeringQueuedTurns(queued, requestedAt + 1_000)]).toEqual(["turn-orch"]);
		expect(cloudQueuedSteerErrors(queued, requestedAt + 1_000)).toEqual({});
		expect([...cloudSteeringQueuedTurns(queued, requestedAt + QUEUED_STEER_STALE_MS)]).toEqual([]);
		expect(cloudQueuedSteerErrors(queued, requestedAt + QUEUED_STEER_STALE_MS)).toEqual({
			"turn-orch": "Couldn't steer: the agent did not answer in time. It's back in the queue; select Steer to try again.",
		});

		// The chat's clock moves on at each unanswered steer's deadline in turn,
		// so a later one fails too after an earlier one did.
		const later = { ...queued[5], sequence: 7, createdAt: new Date(requestedAt + 30_000).toISOString(), payload: { ...queued[5].payload, queuedTurnId: "turn-mine" } };
		const both = [...queued, later];
		expect(nextQueuedSteerDeadline(both, requestedAt)).toBe(requestedAt + QUEUED_STEER_STALE_MS);
		expect(nextQueuedSteerDeadline(both, requestedAt + QUEUED_STEER_STALE_MS)).toBe(requestedAt + 30_000 + QUEUED_STEER_STALE_MS);
		expect(nextQueuedSteerDeadline(both, requestedAt + 30_000 + QUEUED_STEER_STALE_MS)).toBeUndefined();
	});

	// What the chat shows after a failed or a delivered steer comes from the
	// control plane's events, so it is the same after a remount (a tab or
	// screen switch): queued with the reason when the steer failed, gone from
	// the queue into the running turn when it was delivered.
	it("shows a failed or a delivered queued steer the same after a remount", async () => {
		const { queued, failed, steered } = steerEvents();
		cloudMocks.listChatModels.mockResolvedValue({ models: [] });
		const shown = async (events: CloudCpClientEvent[]) => {
			cloudMocks.listChatEvents.mockReset().mockResolvedValue({ events, hasMore: false, nextAfter: events.at(-1)!.sequence, hasOlder: false, nextBefore: 1 });
			cloudMocks.chatProps.mockClear();
			const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
			const view = render(<QueryClientProvider client={queryClient}><CloudSessionChatSurface session={{ ...session, cloud: { orgId: "org-1" } }} /></QueryClientProvider>);
			await waitFor(() => expect(cloudMocks.chatProps.mock.lastCall?.[0].snapshot.latestSequence).toBe(events.at(-1)!.sequence));
			const props = cloudMocks.chatProps.mock.lastCall?.[0] as { snapshot: ReturnType<typeof toSnapshot>; queuedSteerErrors: Record<string, string> };
			view.unmount();
			return { queue: queuedTurns(props.snapshot), steers: steers(props.snapshot), errors: props.queuedSteerErrors };
		};
		const failedView = { queue: ["turn-orch", "turn-mine"], steers: [], errors: { "turn-orch": "Couldn't steer: The agent finished first. It's back in the queue; select Steer to try again." } };
		expect(await shown([...queued, ...failed])).toEqual(failedView);
		expect(await shown([...queued, ...failed])).toEqual(failedView);
		const steeredView = { queue: ["turn-mine"], steers: [{ turnId: "turn-run", text: "report progress now", origin: "automation", from: "Orchestrator" }], errors: {} };
		expect(await shown([...queued, ...steered])).toEqual(steeredView);
		expect(await shown([...queued, ...steered])).toEqual(steeredView);
	});

	// A steer the control plane refuses, or cannot be asked (offline), comes
	// back as the row's reason, never as a steer that silently did nothing.
	it("turns a refused or unreachable queued steer into the row's reason", async () => {
		cloudSetup.features = ["queued-steer"];
		const { queued } = steerEvents();
		cloudMocks.listChatEvents.mockReset().mockResolvedValue({ events: queued.slice(0, 5), hasMore: false, nextAfter: 5, hasOlder: false, nextBefore: 1 });
		cloudMocks.listChatModels.mockResolvedValue({ models: [] });
		cloudMocks.chatProps.mockClear();
		const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
		render(<QueryClientProvider client={queryClient}><CloudSessionChatSurface session={{ ...session, cloud: { orgId: "org-1" } }} /></QueryClientProvider>);
		await waitFor(() => expect(cloudMocks.chatProps.mock.lastCall?.[0].onPromoteQueuedTurn).toEqual(expect.any(Function)));
		const promote = cloudMocks.chatProps.mock.lastCall?.[0].onPromoteQueuedTurn as (turnId: string) => Promise<unknown>;
		const outcome = async (error: unknown) => {
			cloudMocks.steerQueuedMessage.mockReset().mockRejectedValue(error);
			let thrown: unknown;
			await act(async () => { thrown = await promote("turn-mine").then(() => "steered", (reason: unknown) => reason); });
			return (thrown as Error).message;
		};
		expect(await outcome(new TypeError("Failed to fetch")))
			.toBe("Couldn't steer: AO Cloud could not be reached. It's back in the queue; select Steer to try again.");
		expect(await outcome(new CloudCpError("The agent is not running a turn it can take guidance in.", { status: 409, code: "TURN_NOT_STEERABLE" })))
			.toBe("Couldn't steer: the agent's running turn can't take guidance. It's back in the queue; select Steer to try again.");
		expect(await outcome(new CloudCpError("The request could not be completed.", { status: 500, code: "internal_error" })))
			.toBe("Couldn't steer: The request could not be completed. It's back in the queue; select Steer to try again.");
	});

	// A queued message ended any other way (a stop-now switch to the terminal,
	// an event from before reasons, a reason this desktop does not know) keeps
	// the person's text, shown interrupted as a local chat shows a queued
	// message its Stop or switch ended. It never started, so it has no working
	// time to show, on a page without its message too.
	it.each([["a switch to the terminal", "interface_switch"], ["no reason", undefined], ["an unknown reason", "session_ended"]])(
		"keeps a queued message ended by %s, shown interrupted without working time",
		(_name, reason) => {
			const events = queueEvents(reason);
			const full = toSnapshot(session, events);
			expect(full.items.filter((item) => item.kind === "message").map((item) => item.turnId)).toEqual(["turn-run", "turn-ended", "turn-next"]);
			const ended = full.turns.find((turn) => turn.id === "turn-ended");
			expect(ended).toMatchObject({ state: "interrupted", completedAt: "2026-11-13T09:05:00Z" });
			expect(ended).not.toHaveProperty("startedAt");
			const partial = toSnapshot(session, events.slice(4), undefined, { partial: true });
			const partialEnded = partial.turns.find((turn) => turn.id === "turn-ended");
			expect(partialEnded?.state).toBe("interrupted");
			expect(partialEnded).not.toHaveProperty("startedAt");
		},
	);

	// A queued message the control plane refused when its turn came never ran:
	// it reads as not run, with the reason, not as a run that failed.
	it("shows a queued message refused when its turn came as not run, with its reason", () => {
		const snapshot = toSnapshot(session, [
			{ sessionId: session.id, sequence: 1, type: "chat.user_message", payload: { text: "write a file", turnId: "turn-refused" }, createdAt: "2026-11-13T09:00:00Z" },
			{ sessionId: session.id, sequence: 2, type: "chat.turn_aborted", payload: { turnId: "turn-refused", attempt: 1, error: "Not run: the person who sent this message can no longer write to this session." }, createdAt: "2026-11-13T09:01:00Z" },
		]);
		expect(snapshot.items.filter((item) => item.kind === "message")).toHaveLength(1);
		expect(snapshot.turns[0]).toMatchObject({ id: "turn-refused", state: "failed", notRun: true, errorMessage: "The person who sent this message can no longer write to this session." });
	});

	// The control plane marks a message it did not run (reason
	// author_cannot_write), so a page holding only that end, without the
	// message or its start, still reads it as not run; one without a reason
	// there is a normal failure.
	it("shows a refused message as not run on a page holding only its end", () => {
		const aborted = (reason?: string) => toSnapshot(session, [
			{ sessionId: session.id, sequence: 9, type: "chat.turn_aborted", payload: { turnId: "turn-refused", attempt: 1, error: "Not run: the person who sent this message can no longer write to this session.", ...(reason ? { reason } : {}) }, createdAt: "2026-11-13T09:01:00Z" },
		], undefined, { partial: true }).turns[0];
		const refused = aborted("author_cannot_write");
		expect(refused).toMatchObject({ id: "turn-refused", state: "failed", notRun: true, errorMessage: "The person who sent this message can no longer write to this session." });
		expect(refused).not.toHaveProperty("startedAt");
		expect(aborted()).not.toHaveProperty("notRun");
	});

	// A turn the agent ran and the person stopped reads as it always did.
	it("keeps a running turn the person stopped as interrupted", () => {
		const snapshot = toSnapshot(session, [
			{ sessionId: session.id, sequence: 1, type: "chat.user_message", payload: { text: "long job", turnId: "turn-1" }, createdAt: "2026-11-13T09:00:00Z" },
			{ sessionId: session.id, sequence: 2, type: "chat.turn_started", payload: { turnId: "turn-1", attempt: 1 }, createdAt: "2026-11-13T09:00:01Z" },
			{ sessionId: session.id, sequence: 3, type: "chat.turn_interrupted", payload: { turnId: "turn-1", attempt: 1 }, createdAt: "2026-11-13T09:00:30Z" },
		]);
		expect(snapshot.items.filter((item) => item.kind === "message")).toHaveLength(1);
		expect(snapshot.turns[0]).toMatchObject({ id: "turn-1", state: "interrupted", startedAt: "2026-11-13T09:00:01Z", completedAt: "2026-11-13T09:00:30Z" });
		expect(snapshot.turns[0]).not.toHaveProperty("notRun");
	});

	// The control plane lets only a message's author edit it.
	it("knows the turns of the viewer's own messages", () => {
		const events: CloudCpClientEvent[] = [
			{ sessionId: session.id, sequence: 1, type: "chat.user_message", payload: { text: "mine", turnId: "turn-1", authorUserId: "me" }, createdAt: session.updatedAt },
			{ sessionId: session.id, sequence: 2, type: "chat.user_message", payload: { text: "theirs", turnId: "turn-2", authorUserId: "teammate" }, createdAt: session.updatedAt },
			{ sessionId: session.id, sequence: 3, type: "chat.user_message", payload: { text: "report", turnId: "turn-3", origin: "automation" }, createdAt: session.updatedAt },
		];
		expect([...ownMessageTurns(events, "me")]).toEqual(["turn-1"]);
		expect(ownMessageTurns(events, undefined).size).toBe(0);
	});

	it("attributes delivered worker reports as automation without changing the agent prompt", () => {
		const events: CloudCpClientEvent[] = [{
			sessionId: session.id, sequence: 1, type: "chat.user_message",
			payload: { text: '[from worker a1b2c3d4 "Builder"] Full prompt', origin: "automation", senderLabel: "Worker · Builder", displayText: "Full prompt" },
			createdAt: "2026-10-01T00:00:00Z",
		}];
		expect(toSnapshot(session, events).items[0]).toMatchObject({
			role: "user", origin: "automation", senderLabel: "Worker · Builder", text: "Full prompt",
		});
	});
	it("says once, outside any turn, that the session runs the default because the account lacks the project's model", () => {
		const events: CloudCpClientEvent[] = [
			{ sessionId: session.id, sequence: 1, type: "session.model_unavailable", payload: { model: "claude-opus-5-5", harness: "claude-code" }, createdAt: "2026-10-01T00:00:00Z" },
			// A malformed one shows nothing.
			{ sessionId: session.id, sequence: 2, type: "session.model_unavailable", payload: { harness: "claude-code" }, createdAt: "2026-10-01T00:00:00Z" },
		];
		const items = toSnapshot(session, events).items;
		expect(items).toHaveLength(1);
		expect(items[0]).toMatchObject({
			kind: "activity", activityKind: "system", status: "completed",
			summary: "claude-opus-5-5 isn't available to your Claude Code account on Cloud, so this session uses the default model.",
			detail: { event: "session.model_unavailable" },
		});
		expect(items[0]?.turnId).toBeUndefined();
		// Without a harness in the event, the session's own agent names the account.
		const fallback = toSnapshot({ ...session, provider: "codex" }, [
			{ sessionId: session.id, sequence: 1, type: "session.model_unavailable", payload: { model: "gpt-6.1-sol" }, createdAt: "2026-10-01T00:00:00Z" },
		]).items[0];
		expect(fallback).toMatchObject({ summary: "gpt-6.1-sol isn't available to your Codex account on Cloud, so this session uses the default model." });
	});

	it("shows a project setup outcome as a system row outside any turn", () => {
		const events: CloudCpClientEvent[] = [
			{ sessionId: session.id, sequence: 1, type: "workspace.setup", payload: { status: "succeeded", exitCode: 0, durationMs: 12_400, output: "stack up" }, createdAt: "2026-10-01T00:00:00Z" },
			{ sessionId: session.id, sequence: 2, type: "workspace.setup", payload: { status: "failed", step: 3, exitCode: 2, durationMs: 800 }, createdAt: "2026-10-01T00:01:00Z" },
		];
		const [succeeded, failed] = toSnapshot(session, events).items;
		expect(succeeded).toMatchObject({
			kind: "activity", activityKind: "system", status: "completed",
			detail: { event: "workspace.setup", setupStatus: "succeeded", exitCode: 0, durationMs: 12_400, output: "stack up" },
		});
		expect(failed).toMatchObject({
			status: "failed", detail: { event: "workspace.setup", setupStatus: "failed", setupStep: 3, exitCode: 2 },
		});
		expect(succeeded?.turnId).toBeUndefined();
		expect(failed?.kind === "activity" ? failed.detail?.output : "unexpected").toBeUndefined();
	});
	it("draws one live row per script run and drops the summary it supersedes", () => {
		const step = (sequence: number, payload: Record<string, unknown>): CloudCpClientEvent => ({
			sessionId: session.id, sequence, type: "workspace.script.step", createdAt: "2026-10-01T00:00:00Z",
			payload: { phase: "setup", runId: "run-1", total: 2, command: "npm ci", repository: "", ...payload },
		});
		const running = [
			step(1, { step: 1, status: "running" }),
			step(2, { step: 1, status: "running", output: "added 10" }),
		];
		const [live] = toSnapshot(session, running).items;
		expect(live).toMatchObject({
			kind: "activity", id: "cloud-script-run-run-1", activityKind: "system", status: "running", revision: 2,
			summary: "Setup: Running step 1 of 2",
			detail: { event: "workspace.script.run", scriptRun: { runId: "run-1", status: "running", runningStep: 1 } },
		});
		const finished = [
			// An older worker's summary for an earlier boot has no step events and keeps its row.
			{ sessionId: session.id, sequence: 0, type: "workspace.setup", payload: { status: "succeeded" }, createdAt: "2026-10-01T00:00:00Z" },
			...running,
			step(3, { step: 1, status: "succeeded", exitCode: 0, durationMs: 1000 }),
			step(4, { step: 2, status: "succeeded", exitCode: 0, durationMs: 1000, command: "npm run build" }),
			{ sessionId: session.id, sequence: 5, type: "workspace.setup", payload: { status: "succeeded", durationMs: 2000 }, createdAt: "2026-10-01T00:00:05Z" },
			step(6, { phase: "cleanup", runId: "cleanup-1", total: 1, step: 1, status: "succeeded", exitCode: 0, durationMs: 10 }),
		] satisfies CloudCpClientEvent[];
		const items = toSnapshot(session, finished).items;
		expect(items.map((item) => item.kind === "activity" ? item.detail?.event : item.kind)).toEqual([
			"workspace.setup", "workspace.script.run", "workspace.script.run",
		]);
		expect(items[1]).toMatchObject({ status: "completed", summary: "Setup: Succeeded", detail: { scriptRun: { status: "succeeded" } } });
		expect(items[2]).toMatchObject({ summary: "Cleanup: Succeeded", detail: { scriptRun: { phase: "cleanup" } } });
	});
	it("places the session's start Setup above the first prompt, and a later Setup where it ran", () => {
		const at = "2026-10-01T00:00:00Z";
		const step = (sequence: number, runId: string, status: string): CloudCpClientEvent => ({
			sessionId: session.id, sequence, type: "workspace.script.step", createdAt: at,
			payload: { phase: "setup", runId, total: 1, step: 1, command: "npm ci", repository: "", status },
		});
		const events: CloudCpClientEvent[] = [
			{ sessionId: session.id, sequence: 1, type: "chat.user_message", payload: { text: "Reply OK", turnId: "t1" }, createdAt: at },
			step(2, "start", "running"),
			step(3, "start", "succeeded"),
			{ sessionId: session.id, sequence: 4, type: "chat.turn_started", payload: { turnId: "t1" }, createdAt: at },
			{ sessionId: session.id, sequence: 5, type: "chat.turn_completed", payload: { turnId: "t1" }, createdAt: at },
			step(6, "retry", "succeeded"),
		];
		const runs = toSnapshot(session, events).items.filter((item) => item.kind === "activity" && item.detail?.event === "workspace.script.run");
		expect(runs.map((run) => [run.id, run.sequence])).toEqual([["cloud-script-run-start", 0.5], ["cloud-script-run-retry", 6]]);
		// A first prompt without a turn id shares Setup's turn-less group, so Setup leads it in item order too.
		const untracked = events.map((event) => event.type === "chat.user_message" ? { ...event, payload: { text: "Reply OK" } } : event);
		expect(toSnapshot(session, untracked).items.slice(0, 2).map((item) => item.id)).toEqual(["cloud-script-run-start", "cloud-event-1"]);
	});
	it("carries a failed setup step's command, user, directory, log and hint", () => {
		const events: CloudCpClientEvent[] = [{
			sessionId: session.id, sequence: 1, type: "workspace.setup",
			payload: {
				status: "failed", step: 2, exitCode: 1, output: "denied", command: "docker compose up -d", dir: "/w/lib",
				user: "ao-worker", groups: ["ao-worker", "users"], logPath: "/d/setup-logs/step-2.log", hint: "No Docker.",
			},
			createdAt: "2026-10-01T00:00:00Z",
		}];
		expect(toSnapshot(session, events).items[0]).toMatchObject({
			detail: {
				setupHint: "No Docker.",
				setupFailure: {
					command: "docker compose up -d", user: "ao-worker", groups: ["ao-worker", "users"],
					dir: "/w/lib", logPath: "/d/setup-logs/step-2.log",
				},
			},
		});
	});
	it("preserves structured sender metadata for linked automation rendering", () => {
		const events: CloudCpClientEvent[] = [{
			sessionId: session.id, sequence: 1, type: "chat.user_message",
			payload: {
				text: '[from worker-1] Full prompt', origin: "automation", displayText: "Full prompt",
				senderSessionId: "worker-1", senderProjectId: "project-1", senderDisplayName: "Backend worker",
			},
			createdAt: "2026-10-01T00:00:00Z",
		}];
		expect(toSnapshot(session, events).items[0]).toMatchObject({
			origin: "automation", text: "Full prompt", senderSessionId: "worker-1",
			senderProjectId: "project-1", senderDisplayName: "Backend worker",
		});
	});
	it("keeps human messages human even when their text resembles a worker report", () => {
		const events: CloudCpClientEvent[] = [{
			sessionId: session.id, sequence: 1, type: "chat.user_message",
			payload: { text: '[from worker a1b2c3d4 "Builder"] Please help' },
			createdAt: "2026-10-01T00:00:00Z",
		}];
		expect(toSnapshot(session, events).items[0]).toMatchObject({
			origin: "human", text: '[from worker a1b2c3d4 "Builder"] Please help',
		});
	});

	it("offers native Claude Plan mode and sends read-only turns without changing the permission ceiling", async () => {
		cloudMocks.listChatEvents.mockResolvedValue({ events: [], hasMore: false, nextAfter: 0 });
		cloudMocks.listChatModels.mockResolvedValue({
			models: [{ id: "opus", displayName: "Opus", default: true }],
			modes: ["default", "plan"],
		});
		cloudMocks.sendSessionMessage.mockResolvedValue({ event: {} });
		const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
		const view = render(
			<QueryClientProvider client={queryClient}>
				<CloudSessionChatSurface
					session={{ ...session, provider: "claude-code", cloud: { orgId: "org-1", permissionMode: "trusted" } }}
				/>
			</QueryClientProvider>,
		);
		await waitFor(() =>
			expect(cloudMocks.chatProps.mock.lastCall?.[0].configOptions?.[0].choices).toContainEqual({
				value: "plan",
				name: "Plan",
			}),
		);
		act(() => cloudMocks.chatProps.mock.lastCall?.[0].onChooseConfigOption("mode", { value: "plan" }));
		await cloudMocks.chatProps.mock.lastCall?.[0].onSend("outline only", [], "plan-1");
		expect(cloudMocks.sendSessionMessage).toHaveBeenLastCalledWith(
			"org-1",
			session.id,
			{ text: "outline only", mode: "read-only" },
			{ idempotencyKey: "plan-1" },
		);
		view.unmount();
		render(
			<QueryClientProvider client={queryClient}>
				<CloudSessionChatSurface
					session={{ ...session, provider: "claude-code", cloud: { orgId: "org-1", permissionMode: "trusted" } }}
				/>
			</QueryClientProvider>,
		);
		await waitFor(() => expect(cloudMocks.chatProps.mock.lastCall?.[0].configOptions?.[0].currentValue).toBe("plan"));
		act(() => cloudMocks.chatProps.mock.lastCall?.[0].onChooseConfigOption("mode", { value: "agent" }));
		await cloudMocks.chatProps.mock.lastCall?.[0].onSend("implement", [], "agent-1");
		expect(cloudMocks.sendSessionMessage).toHaveBeenLastCalledWith(
			"org-1",
			session.id,
			expect.objectContaining({ mode: "trusted" }),
			{ idempotencyKey: "agent-1" },
		);
	});

	// Claude asks to leave Plan mode (its ExitPlanMode tool) with one option per
	// mode it can carry on in, and "plan" to keep planning, as claude-agent-acp
	// sends it to local Chat.
	const planApproval: CloudCpClientEvent[] = [
		{ sessionId: session.id, sequence: 1, type: "chat.user_message", payload: { text: "outline only", turnId: "turn-1", mode: "read-only" }, createdAt: session.updatedAt },
		{ sessionId: session.id, sequence: 2, type: "chat.turn_started", payload: { turnId: "turn-1" }, createdAt: session.updatedAt },
		{ sessionId: session.id, sequence: 3, type: "chat.approval_requested", payload: {
			turnId: "turn-1", requestId: "plan-approval", summary: "Ready to code?", toolKind: "switch_mode", subjectKind: "mcp_tool",
			decisions: [
				{ id: "acceptEdits", label: "Yes, and auto-accept edits", kind: "allow_always" },
				{ id: "default", label: "Yes, and manually approve edits", kind: "allow_once" },
				{ id: "plan", label: "No, keep planning", kind: "reject_once" },
			],
		}, createdAt: session.updatedAt },
	];
	const planningSurface = async () => {
		localStorage.setItem("cloud-chat-settings:org-1:session-1:claude-code", JSON.stringify({ executionMode: "plan" }));
		cloudMocks.listChatEvents.mockResolvedValue({ events: planApproval, hasMore: false, nextAfter: 3 });
		cloudMocks.listChatModels.mockResolvedValue({ models: [{ id: "opus", displayName: "Opus", default: true }], modes: ["default", "plan"] });
		cloudMocks.sendSessionMessage.mockResolvedValue({ event: {} });
		cloudMocks.decideChatApproval.mockReset().mockResolvedValue({ ok: true });
		const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
		render(<QueryClientProvider client={queryClient}>
			<CloudSessionChatSurface session={{ ...session, provider: "claude-code", cloud: { orgId: "org-1", permissionMode: "trusted" } }} />
		</QueryClientProvider>);
		await waitFor(() => expect(cloudMocks.chatProps.mock.lastCall?.[0].configOptions?.[0].currentValue).toBe("plan"));
		await waitFor(() => expect(cloudMocks.chatProps.mock.lastCall?.[0].snapshot.items).toContainEqual(expect.objectContaining({
			activityKind: "approval", requestId: "plan-approval", status: "pending",
		})));
	};

	it("leaves Plan mode for the mode the user approved the plan with, as local Chat does", async () => {
		await planningSurface();
		act(() => cloudMocks.chatProps.mock.lastCall?.[0].onDecide("plan-approval", "acceptEdits"));
		await waitFor(() => expect(cloudMocks.decideChatApproval).toHaveBeenCalledWith("org-1", session.id, "plan-approval", "acceptEdits"));
		await waitFor(() => expect(cloudMocks.chatProps.mock.lastCall?.[0].configOptions?.[0].currentValue).toBe("agent"));
		await cloudMocks.chatProps.mock.lastCall?.[0].onSend("next step", [], "after-plan");
		expect(cloudMocks.sendSessionMessage).toHaveBeenLastCalledWith(
			"org-1",
			session.id,
			{ text: "next step", mode: "trusted", approvalMode: "accept-edits" },
			{ idempotencyKey: "after-plan" },
		);
	});

	it("keeps planning when the user rejects the plan, or the approval is refused", async () => {
		await planningSurface();
		act(() => cloudMocks.chatProps.mock.lastCall?.[0].onDecide("plan-approval", "plan"));
		await waitFor(() => expect(cloudMocks.decideChatApproval).toHaveBeenCalledWith("org-1", session.id, "plan-approval", "plan"));
		// The control plane refuses an approval whose turn already ended: the plan was not approved.
		cloudMocks.decideChatApproval.mockRejectedValueOnce(new CloudCpError("The turn has finished.", { status: 409, code: "turn_finished" }));
		act(() => cloudMocks.chatProps.mock.lastCall?.[0].onDecide("plan-approval", "default"));
		await waitFor(() => expect(cloudMocks.decideChatApproval).toHaveBeenCalledTimes(2));
		await waitFor(() => expect(cloudMocks.chatProps.mock.lastCall?.[0].commandError).toBeTruthy());
		expect(cloudMocks.chatProps.mock.lastCall?.[0].configOptions?.[0].currentValue).toBe("plan");
		await cloudMocks.chatProps.mock.lastCall?.[0].onSend("revise it", [], "still-planning");
		expect(cloudMocks.sendSessionMessage).toHaveBeenLastCalledWith(
			"org-1",
			session.id,
			{ text: "revise it", mode: "read-only" },
			{ idempotencyKey: "still-planning" },
		);
	});

	it("offers Claude models the way local Chat does: the session's list, model and effort options", async () => {
		cloudMocks.listChatEvents.mockResolvedValue({ events: [], hasMore: false, nextAfter: 0 });
		cloudMocks.listChatModels.mockResolvedValue({ model: "opus", models: [
			{ id: "opus", displayName: "Opus", description: "Opus 5.5 · Best for complex tasks", default: true, efforts: ["low", "high"], defaultEffort: "high" },
			{ id: "claude-opus-5", displayName: "Claude Opus 5", default: false },
		] });
		cloudMocks.sendSessionMessage.mockResolvedValue({ event: {} });
		const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
		queryClient.setQueryData(agentModelsQueryKey("claude-code", ""), { models: [
			{ id: "claude-opus-5", label: "Claude Opus 5" },
			{ id: "claude-opus-5-5", label: "Claude Opus 5.5" },
		] });
		render(
			<QueryClientProvider client={queryClient}>
				<CloudSessionChatSurface session={{ ...session, provider: "claude-code", cloud: { orgId: "org-1", permissionMode: "trusted" } }} />
			</QueryClientProvider>,
		);
		const props = () => cloudMocks.chatProps.mock.lastCall?.[0];
		await waitFor(() => expect(props().configOptions?.find((option: { id: string }) => option.id === "model")?.currentValue).toBe("opus"));
		// The session's own list, named by it: this computer's catalog is not the worker's.
		expect(props().models.map((model: ChatModel) => [model.id, model.displayName])).toEqual([["opus", "Opus 5.5"], ["claude-opus-5", "Claude Opus 5"]]);
		expect(props().configOptions.find((option: { id: string }) => option.id === "effort")).toMatchObject({ currentValue: "high", choices: [{ value: "low" }, { value: "high" }] });
		act(() => props().onChooseConfigOption("model", { value: "claude-opus-5" }));
		await props().onSend("go", [], "model-1");
		expect(cloudMocks.sendSessionMessage).toHaveBeenLastCalledWith("org-1", session.id, expect.objectContaining({ model: "claude-opus-5" }), { idempotencyKey: "model-1" });
	});

	it("has the account's Cloud model lists read again once the session lists the account's own models", async () => {
		cloudMocks.listChatEvents.mockResolvedValue({ events: [], hasMore: false, nextAfter: 0 });
		cloudMocks.listChatModels.mockResolvedValue({ accountListed: true, cliVersion: "2.1.287", models: [
			{ id: "opus", displayName: "Opus", default: true },
		] });
		const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
		const invalidate = vi.spyOn(queryClient, "invalidateQueries");
		render(
			<QueryClientProvider client={queryClient}>
				<CloudSessionChatSurface session={{ ...session, provider: "claude-code", cloud: { orgId: "org-1", permissionMode: "trusted" } }} />
			</QueryClientProvider>,
		);
		await waitFor(() => expect(invalidate).toHaveBeenCalledWith({ queryKey: ["cloud-agent-models", cloudSetup.baseUrl, "user-1", "org-1", "claude-code"] }));
	});

	it("leaves the Cloud model lists alone when the session's list is the CLI's built-in one", async () => {
		cloudMocks.listChatEvents.mockResolvedValue({ events: [], hasMore: false, nextAfter: 0 });
		cloudMocks.listChatModels.mockResolvedValue({ accountListed: false, models: [{ id: "gpt-6.1-sol", displayName: "GPT-6.1-Sol", default: true }] });
		const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
		const invalidate = vi.spyOn(queryClient, "invalidateQueries");
		render(
			<QueryClientProvider client={queryClient}>
				<CloudSessionChatSurface session={{ ...session, provider: "codex", cloud: { orgId: "org-1", permissionMode: "trusted" } }} />
			</QueryClientProvider>,
		);
		await waitFor(() => expect(cloudMocks.listChatModels).toHaveBeenCalled());
		await new Promise((resolve) => setTimeout(resolve, 50));
		expect(invalidate).not.toHaveBeenCalledWith(expect.objectContaining({ queryKey: expect.arrayContaining(["cloud-agent-models"]) }));
	});

	it("names a Claude alias by the model the session's own list describes, never this computer's", async () => {
		cloudMocks.listChatEvents.mockResolvedValue({ events: [], hasMore: false, nextAfter: 0 });
		// The worker's CLI runs Haiku 4.5 for "haiku".
		cloudMocks.listChatModels.mockReset().mockResolvedValue({ model: "haiku", models: [
			{ id: "opus", displayName: "Opus", description: "Opus 5.5 · Most capable for complex work", default: true },
			{ id: "haiku", displayName: "Haiku", description: "Haiku 4.5 · Fastest for quick answers", default: false },
		] });
		const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
		// This computer's newer CLI runs Haiku 5.5 for it.
		queryClient.setQueryData(agentModelsQueryKey("claude-code", ""), { models: [
			{ id: "claude-opus-5-5", label: "Claude Opus 5.5" },
			{ id: "claude-haiku-5-5", label: "Claude Haiku 5.5" },
			{ id: "claude-haiku-4-5", label: "Claude Haiku 4.5" },
		] });
		cloudMocks.chatProps.mockClear();
		render(
			<QueryClientProvider client={queryClient}>
				<CloudSessionChatSurface session={{ ...session, provider: "claude-code", cloud: { orgId: "org-1", permissionMode: "trusted" } }} />
			</QueryClientProvider>,
		);
		const props = () => cloudMocks.chatProps.mock.lastCall?.[0];
		await waitFor(() => expect(props().configOptions?.find((option: { id: string }) => option.id === "model")?.currentValue).toBe("haiku"));
		const composer = composerFor(props());
		expect(composer.modelLabel).toBe("Haiku 4.5");
		expect(composer.models.map((model) => [model.id, model.label])).toEqual([["opus", "Opus 5.5"], ["haiku", "Haiku 4.5"]]);
	});

	it("filters a Claude chat by the session's own ids, as New Task filters Cloud's catalog", async () => {
		cloudMocks.listChatEvents.mockResolvedValue({ events: [], hasMore: false, nextAfter: 0 });
		cloudMocks.listChatModels.mockReset().mockResolvedValue({ model: "opus", models: [
			{ id: "opus", displayName: "Opus", description: "Opus 5.5 · Most capable for complex work", default: true },
			{ id: "haiku", displayName: "Haiku", description: "Haiku 4.5 · Fastest for quick answers", default: false },
			{ id: "claude-opus-5", displayName: "Claude Opus 5", default: false },
		] });
		const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
		queryClient.setQueryData(agentModelsQueryKey("claude-code", ""), { models: [
			{ id: "claude-opus-5-5", label: "Claude Opus 5.5" },
			{ id: "claude-opus-5", label: "Claude Opus 5" },
			{ id: "claude-haiku-5-5", label: "Claude Haiku 5.5" },
		] });
		// This computer's Haiku 5.5 is switched off, which is not the session's Haiku 4.5; Opus 5 is the same model in both.
		queryClient.setQueryData(settingsQueryKey, { defaultSessionMode: "chat", chatHarnesses: [], harnessDefaults: { "claude-code": { disabledModels: ["claude-haiku-5-5", "claude-opus-5"] } } });
		cloudMocks.chatProps.mockClear();
		render(
			<QueryClientProvider client={queryClient}>
				<CloudSessionChatSurface session={{ ...session, provider: "claude-code", cloud: { orgId: "org-1", permissionMode: "trusted" } }} />
			</QueryClientProvider>,
		);
		const props = () => cloudMocks.chatProps.mock.lastCall?.[0];
		const modelChoices = () => props().configOptions?.find((option: { id: string }) => option.id === "model")?.choices.map((choice: { value: string }) => choice.value);
		await waitFor(() => expect(modelChoices()).toEqual(["opus", "haiku"]));
		expect(props().models.map((model: ChatModel) => model.id)).toEqual(["opus", "haiku"]);
	});

	it("names a Claude model's effort levels, so the composer shows the effort a turn runs with", async () => {
		cloudMocks.listChatEvents.mockResolvedValue({ events: [], hasMore: false, nextAfter: 0 });
		cloudMocks.listChatModels.mockReset().mockResolvedValue({ model: "opus", models: [
			{ id: "opus", displayName: "Opus", description: "Opus 5.5 · Most capable for complex work", default: true, efforts: ["low", "medium", "high", "xhigh", "max"], defaultEffort: "high" },
		] });
		cloudMocks.chatProps.mockClear();
		render(
			<QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
				<CloudSessionChatSurface session={{ ...session, provider: "claude-code", cloud: { orgId: "org-1", permissionMode: "trusted" } }} />
			</QueryClientProvider>,
		);
		const props = () => cloudMocks.chatProps.mock.lastCall?.[0];
		const effort = () => props().configOptions?.find((option: { id: string }) => option.id === "effort");
		await waitFor(() => expect(effort()?.currentValue).toBe("high"));
		expect(effort().choices).toEqual([
			{ value: "low", name: "Low" }, { value: "medium", name: "Medium" }, { value: "high", name: "High" },
			{ value: "xhigh", name: "Extra high" }, { value: "max", name: "Max" },
		]);
		const composer = composerFor(props());
		expect(composer.efforts.find((choice) => choice.value === composer.effort)?.label).toBe("High");
	});

	it("shows the agent's catalog while the worker cannot list its models yet", async () => {
		cloudMocks.listChatEvents.mockResolvedValue({ events: [], hasMore: false, nextAfter: 0 });
		cloudMocks.listChatModels.mockReset().mockResolvedValue({ models: [] });
		cloudMocks.resumeSession.mockReset().mockResolvedValue({});
		cloudMocks.listUserAgentModels.mockResolvedValue({ source: "worker", models: [
			{ id: "sonnet", displayName: "Sonnet", default: true, efforts: ["low", "high"], defaultEffort: "high" },
			{ id: "opus", displayName: "Opus", default: false },
		] });
		cloudMocks.chatProps.mockClear();
		render(
			<QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
				<CloudSessionChatSurface session={{ ...session, provider: "claude-code", runtimeConnected: false, cloud: { orgId: "org-1", permissionMode: "trusted" } }} />
			</QueryClientProvider>,
		);
		const props = () => cloudMocks.chatProps.mock.lastCall?.[0];
		await waitFor(() => expect(props().configOptions?.find((option: { id: string }) => option.id === "model")).toMatchObject({
			currentValue: "sonnet", choices: [{ value: "sonnet" }, { value: "opus" }],
		}));
		expect(cloudMocks.listUserAgentModels).toHaveBeenCalledWith("org-1", "claude-code", expect.anything());
		expect(cloudMocks.listChatModels).not.toHaveBeenCalled();
	});

	it("names the session's default model while the catalog stands in, as the worker will", async () => {
		cloudMocks.listChatEvents.mockResolvedValue({ events: [], hasMore: false, nextAfter: 0 });
		cloudMocks.listChatModels.mockReset().mockResolvedValue({ models: [] });
		cloudMocks.resumeSession.mockReset().mockResolvedValue({});
		cloudMocks.listUserAgentModels.mockResolvedValue({ models: [
			{ id: "opus", displayName: "Opus", default: false },
			{ id: "default", displayName: "Sonnet", default: true },
		] });
		cloudMocks.sendSessionMessage.mockReset().mockResolvedValue({ event: {} });
		cloudMocks.chatProps.mockClear();
		render(
			<QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
				<CloudSessionChatSurface session={{ ...session, provider: "claude-code", runtimeConnected: false, cloud: { orgId: "org-1", permissionMode: "trusted" } }} />
			</QueryClientProvider>,
		);
		const props = () => cloudMocks.chatProps.mock.lastCall?.[0];
		await waitFor(() => expect(props().snapshot.settings.model).toBe("default"));
		// Shown, not chosen: the turn still runs the session's default.
		await props().onSend("go", [], "default-1");
		expect(cloudMocks.sendSessionMessage.mock.lastCall?.[2]).not.toHaveProperty("model");
	});

	it("names the model the session's messages last ran with while the catalog stands in, without choosing it", async () => {
		const message = (sequence: number, payload: Record<string, unknown>): CloudCpClientEvent => ({
			sessionId: session.id, sequence, type: "chat.user_message", payload: { text: `message ${sequence}`, ...payload }, createdAt: "2026-10-31T07:00:00Z",
		});
		// The session launched on the default; a later message picked Terra at low effort, and the last one named no model.
		cloudMocks.listChatEvents.mockReset().mockResolvedValue({
			events: [message(1, {}), message(2, { model: "gpt-5.6-terra", reasoningEffort: "low" }), message(3, {})], hasMore: false, nextAfter: 3,
		});
		cloudMocks.listChatModels.mockReset().mockResolvedValue({ models: [] });
		cloudMocks.resumeSession.mockReset().mockResolvedValue({});
		cloudMocks.listUserAgentModels.mockResolvedValue({ models: [
			{ id: "gpt-6-luna", displayName: "GPT-6-Luna", default: true, efforts: ["low", "medium"], defaultEffort: "medium" },
			{ id: "gpt-5.6-terra", displayName: "GPT-5.6-Terra", default: false, efforts: ["low", "medium"], defaultEffort: "medium" },
		] });
		cloudMocks.sendSessionMessage.mockReset().mockResolvedValue({ event: {} });
		cloudMocks.chatProps.mockClear();
		render(
			<QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
				<CloudSessionChatSurface session={{ ...session, provider: "codex", runtimeConnected: false, cloud: { orgId: "org-1", permissionMode: "trusted" } }} />
			</QueryClientProvider>,
		);
		const props = () => cloudMocks.chatProps.mock.lastCall?.[0];
		// What a turn sent now runs, as the worker will report: not the catalog's default.
		await waitFor(() => expect(props().snapshot.settings).toMatchObject({ model: "gpt-5.6-terra", reasoningEffort: "low" }));
		// The bar sends back what it shows with a new effort: the effort is picked, the shown model is not.
		act(() => props().onChooseSettings({ ...props().snapshot.settings, reasoningEffort: "medium" }));
		await props().onSend("go", [], "terra-1");
		expect(cloudMocks.sendSessionMessage.mock.lastCall?.[2]).toMatchObject({ reasoningEffort: "medium" });
		expect(cloudMocks.sendSessionMessage.mock.lastCall?.[2]).not.toHaveProperty("model");
	});

	it("names no model while the catalog stands in until the session's messages have loaded", async () => {
		cloudMocks.listChatEvents.mockReset().mockReturnValue(new Promise(() => {}));
		cloudMocks.listChatModels.mockReset().mockResolvedValue({ models: [] });
		cloudMocks.resumeSession.mockReset().mockResolvedValue({});
		cloudMocks.listUserAgentModels.mockResolvedValue({ models: [
			{ id: "gpt-6-luna", displayName: "GPT-6-Luna", default: true },
			{ id: "gpt-5.6-terra", displayName: "GPT-5.6-Terra", default: false },
		] });
		cloudMocks.chatProps.mockClear();
		render(
			<QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
				<CloudSessionChatSurface session={{ ...session, provider: "codex", runtimeConnected: false, cloud: { orgId: "org-1", permissionMode: "trusted" } }} />
			</QueryClientProvider>,
		);
		const props = () => cloudMocks.chatProps.mock.lastCall?.[0];
		await waitFor(() => expect(props().models).toHaveLength(2));
		// The catalog is there, but the session may have picked another model: none is named yet,
		// neither as the setting nor as the list's default the composer would fall back to.
		expect(props().snapshot.settings.model).toBeUndefined();
		expect(props().models.filter((model: { default?: boolean }) => model.default)).toEqual([]);
	});

	it("names no Claude model while the catalog stands in until the session's messages have loaded", async () => {
		cloudMocks.listChatEvents.mockReset().mockReturnValue(new Promise(() => {}));
		cloudMocks.listChatModels.mockReset().mockResolvedValue({ models: [] });
		cloudMocks.resumeSession.mockReset().mockResolvedValue({});
		cloudMocks.listUserAgentModels.mockResolvedValue({ models: [
			{ id: "sonnet", displayName: "Sonnet", default: true, efforts: ["low", "high"], defaultEffort: "high" },
			{ id: "haiku", displayName: "Haiku", default: false },
		] });
		cloudMocks.chatProps.mockClear();
		render(
			<QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
				<CloudSessionChatSurface session={{ ...session, provider: "claude-code", runtimeConnected: false, cloud: { orgId: "org-1", permissionMode: "trusted" } }} />
			</QueryClientProvider>,
		);
		const props = () => cloudMocks.chatProps.mock.lastCall?.[0];
		const option = (id: string) => props().configOptions?.find((entry: { id: string }) => entry.id === id);
		await waitFor(() => expect(option("model")?.choices).toHaveLength(2));
		// The session may have last run Haiku: neither the catalog's default model nor its effort is named.
		expect(option("model")?.currentValue).toBeUndefined();
		expect(option("effort")).toBeUndefined();
		expect(props().snapshot.settings.model).toBeUndefined();
	});

	it("names no Claude model from the device's own catalog before the session's messages load", async () => {
		cloudMocks.listChatEvents.mockReset().mockReturnValue(new Promise(() => {}));
		cloudMocks.listChatModels.mockReset().mockResolvedValue({ models: [] });
		cloudMocks.resumeSession.mockReset().mockResolvedValue({});
		// The control plane's catalog has not answered either.
		cloudMocks.listUserAgentModels.mockReset().mockReturnValue(new Promise(() => {}));
		const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
		queryClient.setQueryData(agentModelsQueryKey("claude-code", ""), { models: [
			{ id: "claude-opus-5-5", label: "Claude Opus 5.5", isDefault: true },
			{ id: "claude-haiku-5-5", label: "Claude Haiku 5.5" },
		] });
		cloudMocks.chatProps.mockClear();
		render(
			<QueryClientProvider client={queryClient}>
				<CloudSessionChatSurface session={{ ...session, provider: "claude-code", runtimeConnected: false, cloud: { orgId: "org-1", permissionMode: "trusted" } }} />
			</QueryClientProvider>,
		);
		const props = () => cloudMocks.chatProps.mock.lastCall?.[0];
		await waitFor(() => expect(props().models.length).toBeGreaterThan(0));
		// The device's default (Opus) is not the session's: the composer has nothing to fall back to.
		expect(props().models.filter((model: { default?: boolean }) => model.default)).toEqual([]);
		expect(props().snapshot.settings.model).toBeUndefined();
	});

	it("names the control plane's default, not the device's, when the session's messages name no model", async () => {
		cloudMocks.listChatEvents.mockReset().mockResolvedValue({ events: [], hasMore: false, nextAfter: 0 });
		cloudMocks.listChatModels.mockReset().mockResolvedValue({ models: [] });
		cloudMocks.resumeSession.mockReset().mockResolvedValue({});
		let answer: (value: unknown) => void = () => {};
		cloudMocks.listUserAgentModels.mockReset().mockReturnValue(new Promise((resolve) => { answer = resolve; }));
		const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
		queryClient.setQueryData(agentModelsQueryKey("claude-code", ""), { models: [
			{ id: "claude-opus-5-5", label: "Claude Opus 5.5", isDefault: true },
			{ id: "claude-sonnet-5-5", label: "Claude Sonnet 5.5" },
		] });
		cloudMocks.chatProps.mockClear();
		render(
			<QueryClientProvider client={queryClient}>
				<CloudSessionChatSurface session={{ ...session, provider: "claude-code", runtimeConnected: false, cloud: { orgId: "org-1", permissionMode: "trusted" } }} />
			</QueryClientProvider>,
		);
		const props = () => cloudMocks.chatProps.mock.lastCall?.[0];
		await waitFor(() => expect(cloudMocks.listChatEvents).toHaveBeenCalled());
		await waitFor(() => expect(props().models.length).toBeGreaterThan(0));
		// Messages loaded, no model named, the catalog still on its way: the device's Opus is not named.
		expect(props().models.filter((model: { default?: boolean }) => model.default)).toEqual([]);
		answer({ models: [{ id: "sonnet", displayName: "Sonnet", default: true }, { id: "opus", displayName: "Opus" }] });
		await waitFor(() => expect(props().snapshot.settings.model).toBe("sonnet"));
	});

	it("names the Claude model the session's messages last ran with once they load", async () => {
		const message = (sequence: number, payload: Record<string, unknown>): CloudCpClientEvent => ({
			sessionId: session.id, sequence, type: "chat.user_message", payload: { text: `message ${sequence}`, ...payload }, createdAt: "2026-10-31T07:00:00Z",
		});
		cloudMocks.listChatEvents.mockReset().mockResolvedValue({ events: [message(1, {}), message(2, { model: "haiku" })], hasMore: false, nextAfter: 2 });
		cloudMocks.listChatModels.mockReset().mockResolvedValue({ models: [] });
		cloudMocks.resumeSession.mockReset().mockResolvedValue({});
		cloudMocks.listUserAgentModels.mockResolvedValue({ models: [
			{ id: "sonnet", displayName: "Sonnet", default: true, efforts: ["low", "high"], defaultEffort: "high" },
			{ id: "haiku", displayName: "Haiku", default: false },
		] });
		cloudMocks.chatProps.mockClear();
		render(
			<QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
				<CloudSessionChatSurface session={{ ...session, provider: "claude-code", runtimeConnected: false, cloud: { orgId: "org-1", permissionMode: "trusted" } }} />
			</QueryClientProvider>,
		);
		const props = () => cloudMocks.chatProps.mock.lastCall?.[0];
		const option = (id: string) => props().configOptions?.find((entry: { id: string }) => entry.id === id);
		await waitFor(() => expect(option("model")?.currentValue).toBe("haiku"));
		// Haiku has no effort levels: Sonnet's effort is not offered for it.
		expect(option("effort")).toBeUndefined();
	});

	it("replaces the catalog with the worker's own list once it answers", async () => {
		cloudMocks.listChatEvents.mockResolvedValue({ events: [], hasMore: false, nextAfter: 0 });
		cloudMocks.listUserAgentModels.mockResolvedValue({ models: [{ id: "sonnet", displayName: "Sonnet", default: true }] });
		let answer: (value: unknown) => void = () => {};
		cloudMocks.listChatModels.mockReset().mockReturnValue(new Promise((resolve) => { answer = resolve; }));
		cloudMocks.chatProps.mockClear();
		render(
			<QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
				<CloudSessionChatSurface session={{ ...session, provider: "claude-code", cloud: { orgId: "org-1", permissionMode: "trusted" } }} />
			</QueryClientProvider>,
		);
		const props = () => cloudMocks.chatProps.mock.lastCall?.[0];
		const modelOption = () => props().configOptions?.find((option: { id: string }) => option.id === "model");
		await waitFor(() => expect(modelOption()?.currentValue).toBe("sonnet"));
		answer({ model: "opus", models: [{ id: "opus", displayName: "Opus", default: true }] });
		await waitFor(() => expect(modelOption()).toMatchObject({ currentValue: "opus", choices: [{ value: "opus" }] }));
	});

	it("refreshes a waking session as soon as its worker connects in the chat events", async () => {
		cloudMocks.resumeSession.mockReset().mockResolvedValue({});
		cloudMocks.listChatModels.mockReset().mockResolvedValue({ models: [] });
		const connected = (sequence: number): CloudCpClientEvent => ({ sessionId: session.id, sequence, type: "worker.connected", payload: {}, createdAt: "2026-10-31T07:00:00Z" });
		// The first page holds an earlier worker's connect; the next poll brings this one's.
		cloudMocks.listChatEvents.mockReset()
			.mockResolvedValueOnce({ events: [connected(1)], hasMore: false, nextAfter: 1 })
			.mockResolvedValue({ events: [connected(2)], hasMore: false, nextAfter: 2 });
		const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
		const invalidate = vi.spyOn(queryClient, "invalidateQueries");
		const refreshes = () => invalidate.mock.calls.filter(([filters]) => JSON.stringify(filters?.queryKey) === JSON.stringify(["cloud-sessions"])).length;
		render(
			<QueryClientProvider client={queryClient}>
				<CloudSessionChatSurface session={{ ...session, runtimeConnected: false, cloud: { orgId: "org-1" } }} />
			</QueryClientProvider>,
		);
		await waitFor(() => expect(cloudMocks.listChatEvents).toHaveBeenCalledTimes(1));
		expect(refreshes()).toBe(0);
		await waitFor(() => expect(refreshes()).toBe(1), { timeout: 3_000 });
		expect(invalidate).toHaveBeenCalledWith({ queryKey: ["cloud-session"] });
	});

	it("offers only the approval modes the selected model runs, and shows and sends the mode that runs after a model switch", async () => {
		cloudMocks.listChatEvents.mockResolvedValue({ events: [], hasMore: false, nextAfter: 0 });
		cloudMocks.listChatModels.mockResolvedValue({ model: "opus", models: [
			{ id: "opus", displayName: "Opus", default: true, approvalModes: ["default", "accept-edits", "auto", "bypass-permissions"] },
			{ id: "haiku", displayName: "Haiku", default: false, approvalModes: ["default", "accept-edits", "bypass-permissions"] },
		] });
		cloudMocks.sendSessionMessage.mockResolvedValue({ event: {} });
		const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
		render(
			<QueryClientProvider client={queryClient}>
				<CloudSessionChatSurface session={{ ...session, provider: "claude-code", cloud: { orgId: "org-1", permissionMode: "trusted" } }} />
			</QueryClientProvider>,
		);
		const props = () => cloudMocks.chatProps.mock.lastCall?.[0];
		await waitFor(() => expect(props().configOptions?.find((option: { id: string }) => option.id === "model")?.currentValue).toBe("opus"));
		act(() => props().onChooseSettings({ approvalMode: "auto" }, "approvalMode"));
		expect(props().approvalModes).toEqual(["default", "accept-edits", "auto", "bypass-permissions"]);
		expect(props().snapshot.settings.approvalMode).toBe("auto");
		expect(props().approvalModeLimits).toBeUndefined();

		// Haiku cannot run Auto-approve: it is not offered, and the composer
		// shows the mode the turn runs instead, never a more permissive one.
		act(() => props().onChooseConfigOption("model", { value: "haiku" }));
		expect(props().approvalModes).toEqual(["default", "accept-edits", "bypass-permissions"]);
		expect(props().snapshot.settings.approvalMode).toBe("accept-edits");
		expect(props().approvalModeLimits).toEqual({ model: "Haiku", unavailable: ["auto"], requested: "auto" });
		await props().onSend("haiku turn", [], "haiku-1");
		expect(cloudMocks.sendSessionMessage).toHaveBeenLastCalledWith("org-1", session.id, {
			text: "haiku turn", model: "haiku", mode: "trusted", approvalMode: "accept-edits",
		}, { idempotencyKey: "haiku-1" });

		// The pick is kept, so a model that runs it runs it again.
		act(() => props().onChooseConfigOption("model", { value: "opus" }));
		expect(props().snapshot.settings.approvalMode).toBe("auto");
		expect(props().approvalModeLimits).toBeUndefined();
		await props().onSend("opus turn", [], "opus-1");
		expect(cloudMocks.sendSessionMessage).toHaveBeenLastCalledWith("org-1", session.id, {
			text: "opus turn", model: "opus", mode: "trusted", approvalMode: "auto",
		}, { idempotencyKey: "opus-1" });
	});

	it("shows the worker's notice of an approval mode the turn's model does not offer", () => {
		const turnId = "11111111-1111-4111-8111-111111111111";
		const activity = (sequence: number, value: Record<string, unknown>): CloudCpClientEvent => ({
			sessionId: session.id, sequence, type: "chat.activity", payload: { turnId, attempt: 1, activity: value }, createdAt: "2026-10-01T00:00:00Z",
		});
		const items = toSnapshot({ ...session, provider: "claude-code" }, [
			{ sessionId: session.id, sequence: 1, type: "chat.turn_started", payload: { turnId, attempt: 1 }, createdAt: "2026-10-01T00:00:00Z" },
			activity(2, { id: "approval-mode-substituted", kind: "system", status: "completed",
				summary: "Auto-approve isn't available for Haiku; using Accept edits.",
				detail: { event: "approval_mode.substituted", requestedApprovalMode: "auto", approvalMode: "accept-edits" } }),
			// Any other worker system row is not shown: it could name a page to open.
			activity(3, { id: "page", kind: "system", status: "completed", summary: "page", detail: { event: "render", render: { id: "x" } } }),
		]).items;
		expect(items).toHaveLength(1);
		expect(items[0]).toMatchObject({
			kind: "activity", turnId, activityKind: "system", status: "completed",
			summary: "Auto-approve isn't available for Haiku; using Accept edits.", detail: { event: "approval_mode.substituted" },
		});
	});

	it("shows a failed Setup the way a failed local Chat start does, and Retry asks the worker again", async () => {
		cloudMocks.listChatEvents.mockResolvedValue({ events: [], hasMore: false, nextAfter: 0 });
		cloudMocks.listChatModels.mockResolvedValue({ models: [] });
		cloudMocks.requestWorkspaceCheckout.mockResolvedValue({});
		const startupError = { code: "project_setup_failed", message: "Setup step 1 failed: exit status 1.", detail: "could not reach registry", at: "2026-10-20T10:00:00Z" };
		render(
			<QueryClientProvider client={new QueryClient()}>
				<CloudSessionChatSurface session={{ ...session, runtimeConnected: true, cloud: { orgId: "org-1", startupError } }} />
			</QueryClientProvider>,
		);
		const props = () => cloudMocks.chatProps.mock.lastCall?.[0];
		expect(props().session).toMatchObject({
			provisionState: "failed",
			provisionSteps: [{ id: "worktree", status: "done" }, { id: "setup", status: "running" }, { id: "agent", status: "pending" }],
			provisionError: "Setup step 1 failed: exit status 1. Output: could not reach registry",
		});
		// No agent runs until a retry prepares the workspace, so the composer waits as local's does.
		expect(props().snapshot.controller.state).toBe("stopped");
		expect(props().settingsReady).toBe(false);
		act(() => props().onResumeAgent());
		await waitFor(() => expect(cloudMocks.requestWorkspaceCheckout).toHaveBeenCalledWith("org-1", session.id));
		await waitFor(() => expect(props().session.provisionState).toBe("provisioning"));
		expect(props().snapshot.controller.state).not.toBe("stopped");
	});

	it("keeps the first prompt queued under a failed Setup's checklist, and Retry waits for a waking worker", async () => {
		vi.useFakeTimers({ shouldAdvanceTime: true });
		try {
			const prompt: CloudCpClientEvent = { sessionId: session.id, sequence: 1, type: "chat.user_message", payload: { text: "Run", turnId: "turn-1" }, createdAt: "2026-10-20T16:01:57Z" };
			const failedRun: CloudCpClientEvent = {
				sessionId: session.id, sequence: 2, type: "workspace.script.step", createdAt: "2026-10-20T16:02:01Z",
				payload: { phase: "setup", runId: "run-1", step: 1, total: 1, status: "failed", exitCode: 1, command: "./setup.sh", repository: "", startedAt: "2026-10-20T16:02:01Z" },
			};
			const startupError = { code: "project_setup_failed", message: "The project's Setup script failed at step 1 (exit status 1).", at: "2026-10-20T16:02:02Z" };
			const failedSession = { ...session, runtimeConnected: true, cloud: { orgId: "org-1", startupError } };
			// Nothing works on the prompt until a retry passes, as while Setup runs.
			expect(toSnapshot(failedSession, [prompt, failedRun]).turns).toEqual([expect.objectContaining({ id: "turn-1", state: "queued" })]);
			cloudMocks.listChatEvents.mockReset().mockResolvedValue({ events: [prompt, failedRun], hasMore: false, nextAfter: 2 });
			cloudMocks.listChatModels.mockReset().mockResolvedValue({ models: [] });
			// An idle-paused workspace refuses the request until its worker is back.
			cloudMocks.requestWorkspaceCheckout.mockReset()
				.mockRejectedValueOnce(new CloudCpError("The session worker is not connected.", { status: 409, code: "WORKER_UNAVAILABLE" }))
				.mockResolvedValueOnce({});
			cloudMocks.chatProps.mockClear();
			render(
				<QueryClientProvider client={new QueryClient()}>
					<CloudSessionChatSurface session={failedSession} />
				</QueryClientProvider>,
			);
			const props = () => cloudMocks.chatProps.mock.lastCall?.[0];
			await waitFor(() => expect(props().snapshot.turns).toEqual([expect.objectContaining({ id: "turn-1", state: "queued" })]));
			// The checklist reports the failure, so the failed run is not also a row.
			expect(props().snapshot.items.filter((item: { detail?: { event?: string } }) => item.detail?.event === "workspace.script.run")).toEqual([]);
			expect(props().session).toMatchObject({ provisionState: "failed", provisionSteps: [{ id: "worktree", status: "done" }, { id: "setup", status: "running" }, { id: "agent", status: "pending" }] });
			act(() => props().onResumeAgent());
			await act(async () => { await vi.advanceTimersByTimeAsync(1_500); });
			await waitFor(() => expect(cloudMocks.requestWorkspaceCheckout).toHaveBeenCalledTimes(2));
			await waitFor(() => expect(props().session.provisionState).toBe("provisioning"));
			expect(props().resumeError).toBeUndefined();
		} finally {
			vi.useRealTimers();
		}
	});

	it("keeps only the Setup run that passed once a retry recovers, from step events too", async () => {
		const run = (sequence: number, runId: string, status: string): CloudCpClientEvent => ({
			sessionId: session.id, sequence, type: "workspace.script.step", createdAt: "2026-10-20T16:02:01Z",
			payload: { phase: "setup", runId, step: 1, total: 1, status, ...(status === "running" ? {} : { exitCode: status === "succeeded" ? 0 : 1, durationMs: 10 }), command: "./setup.sh", repository: "", startedAt: "2026-10-20T16:02:01Z" },
		});
		const prompt: CloudCpClientEvent = { sessionId: session.id, sequence: 1, type: "chat.user_message", payload: { text: "Run", turnId: "turn-1" }, createdAt: "2026-10-20T16:01:57Z" };
		const runs = (items: { kind: string; detail?: { event?: string; scriptRun?: { runId: string } } }[]) =>
			items.filter((item) => item.detail?.event === "workspace.script.run").map((item) => item.detail?.scriptRun?.runId);
		cloudMocks.listChatEvents.mockReset().mockResolvedValue({ events: [prompt, run(2, "run-1", "failed"), run(3, "run-2", "succeeded")], hasMore: false, nextAfter: 3 });
		cloudMocks.listChatModels.mockReset().mockResolvedValue({ models: [] });
		cloudMocks.chatProps.mockClear();
		render(
			<QueryClientProvider client={new QueryClient()}>
				<CloudSessionChatSurface session={{ ...session, runtimeConnected: true, cloud: { orgId: "org-1" } }} />
			</QueryClientProvider>,
		);
		await waitFor(() => expect(runs(cloudMocks.chatProps.mock.lastCall?.[0].snapshot.items ?? [])).toEqual(["run-2"]));
		// The start Setup that passed still leads the first prompt.
		const items = cloudMocks.chatProps.mock.lastCall?.[0].snapshot.items as { kind: string; role?: string; detail?: { event?: string } }[];
		expect(items.findIndex((item) => item.detail?.event === "workspace.script.run")).toBeLessThan(items.findIndex((item) => item.kind === "message" && item.role === "user"));
	});

	it("shows a terminal agent that could not start after Setup as the checklist's agent step", () => {
		const startupError = { code: "agent_start_failed", message: "The coding agent did not start.", at: "2026-10-20T16:02:02Z" };
		render(
			<QueryClientProvider client={new QueryClient()}>
				<CloudSessionChatSurface session={{ ...session, runtimeConnected: true, cloud: { orgId: "org-1", startupError } }} />
			</QueryClientProvider>,
		);
		expect(cloudMocks.chatProps.mock.lastCall?.[0].session).toMatchObject({
			provisionState: "failed",
			provisionSteps: [{ id: "worktree", status: "done" }, { id: "setup", status: "done" }, { id: "agent", status: "running" }],
		});
	});

	it("keeps only the Setup that passed once a retry recovers, as a local start keeps no failed attempts", async () => {
		cloudMocks.listChatEvents.mockResolvedValue({ events: [
			{ sessionId: session.id, sequence: 1, type: "workspace.setup", payload: { status: "failed", step: 1, exitCode: 1 }, createdAt: "2026-10-01T00:00:00Z" },
			{ sessionId: session.id, sequence: 2, type: "workspace.setup", payload: { status: "succeeded", exitCode: 0, durationMs: 400 }, createdAt: "2026-10-01T00:01:00Z" },
		], hasMore: false, nextAfter: 2 });
		cloudMocks.listChatModels.mockResolvedValue({ models: [] });
		render(
			<QueryClientProvider client={new QueryClient()}>
				<CloudSessionChatSurface session={{ ...session, runtimeConnected: true, cloud: { orgId: "org-1" } }} />
			</QueryClientProvider>,
		);
		const setupRows = () => cloudMocks.chatProps.mock.lastCall?.[0].snapshot.items
			.filter((item: { detail?: { event?: string } }) => item.detail?.event === "workspace.setup");
		await waitFor(() => expect(setupRows()).toHaveLength(1));
		expect(setupRows()[0]).toMatchObject({ detail: { setupStatus: "succeeded" } });
	});

	it("keeps a failed Setup row when no startup failure reports it", async () => {
		cloudMocks.listChatEvents.mockResolvedValue({ events: [
			{ sessionId: session.id, sequence: 1, type: "workspace.setup", payload: { status: "failed", step: 1, exitCode: 1 }, createdAt: "2026-10-01T00:00:00Z" },
		], hasMore: false, nextAfter: 1 });
		cloudMocks.listChatModels.mockResolvedValue({ models: [] });
		render(
			<QueryClientProvider client={new QueryClient()}>
				<CloudSessionChatSurface session={{ ...session, runtimeConnected: true, cloud: { orgId: "org-1" } }} />
			</QueryClientProvider>,
		);
		await waitFor(() => expect(cloudMocks.chatProps.mock.lastCall?.[0].snapshot.items).toEqual([
			expect.objectContaining({ detail: expect.objectContaining({ event: "workspace.setup", setupStatus: "failed" }) }),
		]));
	});

	it("keeps a read-only Claude session in Plan mode and cannot elevate it to Agent", async () => {
		cloudMocks.listChatEvents.mockResolvedValue({ events: [], hasMore: false, nextAfter: 0 });
		cloudMocks.listChatModels.mockResolvedValue({ models: [], modes: ["default", "plan"] });
		cloudMocks.sendSessionMessage.mockResolvedValue({ event: {} });
		const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
		render(
			<QueryClientProvider client={queryClient}>
				<CloudSessionChatSurface
					session={{ ...session, provider: "claude-code", cloud: { orgId: "org-1", permissionMode: "read-only" } }}
				/>
			</QueryClientProvider>,
		);
		await waitFor(() =>
			expect(cloudMocks.chatProps.mock.lastCall?.[0].configOptions?.[0].choices).toEqual([
				{ value: "plan", name: "Plan" },
			]),
		);
		act(() => cloudMocks.chatProps.mock.lastCall?.[0].onChooseConfigOption("mode", { value: "agent" }));
		await cloudMocks.chatProps.mock.lastCall?.[0].onSend("hello", [], "readonly-1");
		expect(cloudMocks.sendSessionMessage).toHaveBeenLastCalledWith(
			"org-1",
			session.id,
			{ text: "hello", mode: "read-only" },
			{ idempotencyKey: "readonly-1" },
		);
	});

	it("waits for the worker to connect before loading model choices, without an error", async () => {
		cloudMocks.listChatEvents.mockResolvedValue({ events: [], hasMore: false, nextAfter: 0 });
		cloudMocks.listChatModels.mockReset().mockResolvedValue({ models: [{ id: "codex-test", displayName: "Codex Test", default: true }] });
		cloudMocks.resumeSession.mockReset().mockResolvedValue({});
		const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
		// A sandbox still starting (or an idle-paused one waking) has no worker
		// yet, for as long as that takes.
		const starting = { ...session, runtimeConnected: false, cloud: { orgId: "org-1" } };
		const view = render(
			<QueryClientProvider client={queryClient}>
				<CloudSessionChatSurface session={starting} />
			</QueryClientProvider>,
		);
		await waitFor(() => expect(cloudMocks.resumeSession).toHaveBeenCalledWith("org-1", session.id));
		expect(cloudMocks.listChatModels).not.toHaveBeenCalled();
		expect(cloudMocks.chatProps.mock.lastCall?.[0].commandError).toBeUndefined();
		// Still not connected on the next session poll: nothing is asked again.
		view.rerender(
			<QueryClientProvider client={queryClient}>
				<CloudSessionChatSurface session={{ ...starting, updatedAt: "2026-09-22T00:00:05Z" }} />
			</QueryClientProvider>,
		);
		expect(cloudMocks.resumeSession).toHaveBeenCalledTimes(1);
		expect(cloudMocks.listChatModels).not.toHaveBeenCalled();

		view.rerender(
			<QueryClientProvider client={queryClient}>
				<CloudSessionChatSurface session={{ ...starting, runtimeConnected: true, terminalGeneration: "1" }} />
			</QueryClientProvider>,
		);
		await waitFor(() => expect(cloudMocks.chatProps.mock.lastCall?.[0].models).toEqual([
			{ id: "codex-test", displayName: "Codex Test", default: true },
		]));
		expect(cloudMocks.listChatModels).toHaveBeenCalledTimes(1);
		expect(cloudMocks.resumeSession).toHaveBeenCalledTimes(1);
		expect(cloudMocks.chatProps.mock.lastCall?.[0].commandError).toBeUndefined();
	});

	it("keeps a model chosen while the worker was still starting over the worker's native model", async () => {
		cloudMocks.listChatEvents.mockResolvedValue({ events: [], hasMore: false, nextAfter: 0 });
		cloudMocks.listChatModels.mockReset().mockResolvedValue({ model: "opus", models: [
			{ id: "opus", displayName: "Opus", default: true },
			{ id: "claude-opus-5", displayName: "Claude Opus 5", default: false },
		] });
		cloudMocks.resumeSession.mockReset().mockResolvedValue({});
		cloudMocks.sendSessionMessage.mockResolvedValue({ event: {} });
		const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
		queryClient.setQueryData(agentModelsQueryKey("claude-code", ""), { models: [
			{ id: "claude-opus-5", label: "Claude Opus 5" },
			{ id: "claude-opus-5-5", label: "Claude Opus 5.5" },
		] });
		const starting = { ...session, provider: "claude-code" as const, runtimeConnected: false, cloud: { orgId: "org-1", permissionMode: "trusted" as const } };
		const view = render(
			<QueryClientProvider client={queryClient}>
				<CloudSessionChatSurface session={starting} />
			</QueryClientProvider>,
		);
		const props = () => cloudMocks.chatProps.mock.lastCall?.[0];
		// Before the catalog, the composer offers the local model list.
		await waitFor(() => expect(props().models.map((model: { id: string }) => model.id)).toContain("claude-opus-5"));
		act(() => props().onChooseSettings({ model: "claude-opus-5" }, "model"));
		view.rerender(
			<QueryClientProvider client={queryClient}>
				<CloudSessionChatSurface session={{ ...starting, runtimeConnected: true, terminalGeneration: "1" }} />
			</QueryClientProvider>,
		);
		await waitFor(() => expect(cloudMocks.listChatModels).toHaveBeenCalledTimes(1));
		await waitFor(() => expect(props().configOptions?.find((option: { id: string }) => option.id === "model")?.currentValue).toBe("claude-opus-5"));
		await props().onSend("go", [], "model-boot");
		expect(cloudMocks.sendSessionMessage).toHaveBeenLastCalledWith("org-1", session.id, expect.objectContaining({ model: "claude-opus-5" }), { idempotencyKey: "model-boot" });
	});

	it("replaces a model picked while the worker was starting when the worker cannot run it", async () => {
		cloudMocks.listChatEvents.mockResolvedValue({ events: [], hasMore: false, nextAfter: 0 });
		cloudMocks.listChatModels.mockReset().mockResolvedValue({ model: "opus", reasoningEffort: "medium", models: [
			{ id: "opus", displayName: "Opus", default: true, efforts: ["low", "medium"] },
		] });
		cloudMocks.resumeSession.mockReset().mockResolvedValue({});
		cloudMocks.sendSessionMessage.mockResolvedValue({ event: {} });
		const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
		queryClient.setQueryData(agentModelsQueryKey("claude-code", ""), { models: [
			{ id: "claude-haiku-local", label: "Haiku (local)" },
		] });
		const starting = { ...session, provider: "claude-code" as const, runtimeConnected: false, cloud: { orgId: "org-1", permissionMode: "trusted" as const } };
		const view = render(
			<QueryClientProvider client={queryClient}>
				<CloudSessionChatSurface session={starting} />
			</QueryClientProvider>,
		);
		const props = () => cloudMocks.chatProps.mock.lastCall?.[0];
		await waitFor(() => expect(props().models.map((model: { id: string }) => model.id)).toContain("claude-haiku-local"));
		act(() => props().onChooseSettings({ model: "claude-haiku-local", reasoningEffort: "high" }, "model"));
		view.rerender(
			<QueryClientProvider client={queryClient}>
				<CloudSessionChatSurface session={{ ...starting, runtimeConnected: true }} />
			</QueryClientProvider>,
		);
		await waitFor(() => expect(props().configOptions?.find((option: { id: string }) => option.id === "model")?.currentValue).toBe("opus"));
		await props().onSend("go", [], "model-unrunnable");
		expect(cloudMocks.sendSessionMessage).toHaveBeenLastCalledWith("org-1", session.id, expect.objectContaining({ model: "opus", reasoningEffort: "medium" }), { idempotencyKey: "model-unrunnable" });
	});

	it("does not wake a connected session, nor one that pauses while Chat is open", async () => {
		cloudMocks.listChatEvents.mockResolvedValue({ events: [], hasMore: false, nextAfter: 0 });
		cloudMocks.listChatModels.mockReset().mockResolvedValue({ models: [{ id: "codex-test", displayName: "Codex Test", default: true }] });
		cloudMocks.resumeSession.mockReset().mockResolvedValue({});
		const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
		const connected = { ...session, cloud: { orgId: "org-1" }, terminalGeneration: "3" };
		const view = render(
			<QueryClientProvider client={queryClient}>
				<CloudSessionChatSurface session={connected} />
			</QueryClientProvider>,
		);
		await waitFor(() => expect(cloudMocks.chatProps.mock.lastCall?.[0].models).toHaveLength(1));
		// Idle pause takes the worker away; the open tab keeps the choices it has.
		view.rerender(
			<QueryClientProvider client={queryClient}>
				<CloudSessionChatSurface session={{ ...connected, runtimeConnected: false }} />
			</QueryClientProvider>,
		);
		expect(cloudMocks.resumeSession).not.toHaveBeenCalled();
		expect(cloudMocks.listChatModels).toHaveBeenCalledTimes(1);
		expect(cloudMocks.chatProps.mock.lastCall?.[0].models).toHaveLength(1);
		expect(cloudMocks.chatProps.mock.lastCall?.[0].commandError).toBeUndefined();
		// A message wakes it; the new worker's catalog is read once it connects.
		view.rerender(
			<QueryClientProvider client={queryClient}>
				<CloudSessionChatSurface session={{ ...connected, runtimeConnected: true, terminalGeneration: "4" }} />
			</QueryClientProvider>,
		);
		await waitFor(() => expect(cloudMocks.listChatModels).toHaveBeenCalledTimes(2));
		expect(cloudMocks.resumeSession).not.toHaveBeenCalled();
	});

	it("asks again while a worker that left mid-request is still reported connected, and stops once it is not", async () => {
		vi.useFakeTimers({ shouldAdvanceTime: true });
		try {
			const unavailable = () => new CloudCpError("The session worker is not connected.", { status: 409, code: "WORKER_UNAVAILABLE" });
			cloudMocks.listChatEvents.mockResolvedValue({ events: [], hasMore: false, nextAfter: 0 });
			// The session list lags a quick pause and resume by a poll.
			cloudMocks.listChatModels.mockReset()
				.mockRejectedValueOnce(unavailable())
				.mockResolvedValue({ models: [{ id: "codex-test", displayName: "Codex Test", default: true }] });
			cloudMocks.resumeSession.mockReset().mockResolvedValue({});
			const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
			const view = render(
				<QueryClientProvider client={queryClient}>
					<CloudSessionChatSurface session={{ ...session, cloud: { orgId: "org-1" } }} />
				</QueryClientProvider>,
			);
			await waitFor(() => expect(cloudMocks.listChatModels).toHaveBeenCalledTimes(1));
			expect(cloudMocks.chatProps.mock.lastCall?.[0].commandError).toBeUndefined();
			await act(async () => { await vi.advanceTimersByTimeAsync(1_500); });
			await waitFor(() => expect(cloudMocks.chatProps.mock.lastCall?.[0].models).toHaveLength(1));
			expect(cloudMocks.listChatModels).toHaveBeenCalledTimes(2);
			view.unmount();

			// A worker that is really gone: the list reports it, and asking stops.
			cloudMocks.listChatModels.mockReset().mockRejectedValue(unavailable());
			const goneClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
			const gone = render(
				<QueryClientProvider client={goneClient}>
					<CloudSessionChatSurface session={{ ...session, id: "session-2", cloud: { orgId: "org-1" } }} />
				</QueryClientProvider>,
			);
			await waitFor(() => expect(cloudMocks.listChatModels).toHaveBeenCalledTimes(1));
			gone.rerender(
				<QueryClientProvider client={goneClient}>
					<CloudSessionChatSurface session={{ ...session, id: "session-2", runtimeConnected: false, cloud: { orgId: "org-1" } }} />
				</QueryClientProvider>,
			);
			await act(async () => { await vi.advanceTimersByTimeAsync(60_000); });
			// The retry already scheduled when the list caught up runs once; the
			// next failure sees the worker gone and stops.
			expect(cloudMocks.listChatModels).toHaveBeenCalledTimes(2);
			expect(cloudMocks.chatProps.mock.lastCall?.[0].commandError).toBeUndefined();
		} finally {
			vi.useRealTimers();
		}
	});

	it("asks again when a worker's first catalog read outlasts the request", async () => {
		vi.useFakeTimers({ shouldAdvanceTime: true });
		try {
			cloudMocks.listChatEvents.mockResolvedValue({ events: [], hasMore: false, nextAfter: 0 });
			cloudMocks.listChatModels.mockReset()
				.mockRejectedValueOnce(new CloudCpError("The worker did not answer in time.", { status: 504, code: "TRANSPORT_TIMEOUT" }))
				.mockRejectedValueOnce(new CloudCpError("The worker did not complete the request in time.", { status: 504, code: "WORKER_TIMEOUT" }))
				.mockResolvedValue({ models: [{ id: "codex-test", displayName: "Codex Test", default: true }] });
			const first = render(
				<QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
					<CloudSessionChatSurface session={{ ...session, cloud: { orgId: "org-1" } }} />
				</QueryClientProvider>,
			);
			await waitFor(() => expect(cloudMocks.listChatModels).toHaveBeenCalledTimes(1));
			expect(cloudMocks.chatProps.mock.lastCall?.[0].commandError).toBeUndefined();
			// Both timeout codes (the queued request expired, or the control plane's
			// own wait ran out) are asked again.
			await act(async () => { await vi.advanceTimersByTimeAsync(4_000); });
			await waitFor(() => expect(cloudMocks.chatProps.mock.lastCall?.[0].models).toHaveLength(1));
			expect(cloudMocks.listChatModels).toHaveBeenCalledTimes(3);
			expect(cloudMocks.chatProps.mock.lastCall?.[0].commandError).toBeUndefined();
			first.unmount();

			// A worker that never answers is reported once its retries run out.
			cloudMocks.listChatModels.mockReset().mockRejectedValue(new CloudCpError("The worker did not answer in time.", { status: 504, code: "TRANSPORT_TIMEOUT" }));
			cloudMocks.chatProps.mockClear();
			render(
				<QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
					<CloudSessionChatSurface session={{ ...session, id: "session-2", cloud: { orgId: "org-1" } }} />
				</QueryClientProvider>,
			);
			await act(async () => { await vi.advanceTimersByTimeAsync(30_000); });
			await waitFor(() => expect(cloudMocks.chatProps.mock.lastCall?.[0].commandError).toBe("Model choices unavailable: The worker did not answer in time."));
			expect(cloudMocks.listChatModels).toHaveBeenCalledTimes(4);
		} finally {
			vi.useRealTimers();
		}
	});

	it("asks again when a just-connected worker fails its first catalog reads", async () => {
		vi.useFakeTimers({ shouldAdvanceTime: true });
		try {
			cloudMocks.listChatEvents.mockResolvedValue({ events: [], hasMore: false, nextAfter: 0 });
			const failed = () => new CloudCpError("The worker could not complete the operation.", { status: 422, code: "WORKER_OPERATION_FAILED" });
			cloudMocks.listChatModels.mockReset()
				.mockRejectedValueOnce(failed())
				.mockRejectedValueOnce(failed())
				.mockResolvedValue({ models: [{ id: "opus", displayName: "Opus", default: true }] });
			render(
				<QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
					<CloudSessionChatSurface session={{ ...session, cloud: { orgId: "org-1" } }} />
				</QueryClientProvider>,
			);
			await waitFor(() => expect(cloudMocks.listChatModels).toHaveBeenCalledTimes(1));
			expect(cloudMocks.chatProps.mock.lastCall?.[0].commandError).toBeUndefined();
			await act(async () => { await vi.advanceTimersByTimeAsync(10_000); });
			await waitFor(() => expect(cloudMocks.chatProps.mock.lastCall?.[0].models).toHaveLength(1));
			expect(cloudMocks.listChatModels).toHaveBeenCalledTimes(3);
			expect(cloudMocks.chatProps.mock.lastCall?.[0].commandError).toBeUndefined();
		} finally {
			vi.useRealTimers();
		}
	});

	it("keeps asking quietly while the worker is still setting up the coding agent", async () => {
		vi.useFakeTimers({ shouldAdvanceTime: true });
		try {
			cloudMocks.listChatEvents.mockResolvedValue({ events: [], hasMore: false, nextAfter: 0 });
			cloudMocks.listChatModels.mockReset()
				.mockRejectedValueOnce(new CloudCpError("Setting up the coding agent on this workspace. Try again in a minute.", { status: 422, code: "CHAT_AGENT_INSTALLING" }))
				.mockRejectedValueOnce(new CloudCpError("Setting up Chat UI on this workspace. Try again in a minute.", { status: 422, code: "CHAT_ADAPTER_INSTALLING" }))
				.mockResolvedValue({ models: [{ id: "codex-test", displayName: "Codex Test", default: true }] });
			render(
				<QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
					<CloudSessionChatSurface session={{ ...session, cloud: { orgId: "org-1" } }} />
				</QueryClientProvider>,
			);
			await waitFor(() => expect(cloudMocks.listChatModels).toHaveBeenCalledTimes(1));
			expect(cloudMocks.chatProps.mock.lastCall?.[0].commandError).toBeUndefined();
			await act(async () => { await vi.advanceTimersByTimeAsync(5_000); });
			expect(cloudMocks.chatProps.mock.lastCall?.[0].commandError).toBeUndefined();
			await act(async () => { await vi.advanceTimersByTimeAsync(5_000); });
			await waitFor(() => expect(cloudMocks.chatProps.mock.lastCall?.[0].models).toHaveLength(1));
			expect(cloudMocks.listChatModels).toHaveBeenCalledTimes(3);
			expect(cloudMocks.chatProps.mock.lastCall?.[0].commandError).toBeUndefined();
		} finally {
			vi.useRealTimers();
		}
	});

	it("does not show a model error kept from a worker that is gone", async () => {
		vi.useFakeTimers({ shouldAdvanceTime: true });
		try {
			cloudMocks.listChatEvents.mockResolvedValue({ events: [], hasMore: false, nextAfter: 0 });
			cloudMocks.listChatModels.mockReset().mockRejectedValue(new CloudCpError("The worker could not complete the operation.", { status: 422, code: "WORKER_OPERATION_FAILED" }));
			cloudMocks.resumeSession.mockReset().mockResolvedValue({});
			const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
			const view = render(
				<QueryClientProvider client={queryClient}>
					<CloudSessionChatSurface session={{ ...session, cloud: { orgId: "org-1" } }} />
				</QueryClientProvider>,
			);
			// The failure shows once its retries (about a minute) run out.
			await act(async () => { await vi.advanceTimersByTimeAsync(120_000); });
			await waitFor(() => expect(cloudMocks.chatProps.mock.lastCall?.[0].commandError).toMatch(/^Model choices unavailable/));
			expect(cloudMocks.listChatModels).toHaveBeenCalledTimes(7);
			view.rerender(
				<QueryClientProvider client={queryClient}>
					<CloudSessionChatSurface session={{ ...session, runtimeConnected: false, cloud: { orgId: "org-1" } }} />
				</QueryClientProvider>,
			);
			await waitFor(() => expect(cloudMocks.chatProps.mock.lastCall?.[0].commandError).toBeUndefined());
		} finally {
			vi.useRealTimers();
		}
	});

	it("does not ask a deleted session for models and loads them once its restored worker connects", async () => {
		cloudMocks.listChatEvents.mockResolvedValue({ events: [], hasMore: false, nextAfter: 0 });
		cloudMocks.listChatModels.mockReset().mockResolvedValue({ models: [{ id: "codex-test", displayName: "Codex Test", default: true }] });
		cloudMocks.resumeSession.mockReset().mockResolvedValue({});
		const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
		const deleted = { ...session, cloud: { orgId: "org-1" }, isTerminated: true, runtimeConnected: false, terminalGeneration: "15702" };
		const view = render(
			<QueryClientProvider client={queryClient}>
				<CloudSessionChatSurface session={deleted} />
			</QueryClientProvider>,
		);
		await waitFor(() => expect(cloudMocks.chatProps).toHaveBeenCalled());
		expect(cloudMocks.listChatModels).not.toHaveBeenCalled();
		expect(cloudMocks.resumeSession).not.toHaveBeenCalled();
		expect(cloudMocks.chatProps.mock.lastCall?.[0].commandError).toBeUndefined();

		// Restore clears the terminated flag before the new worker connects:
		// nothing is asked, and nothing reads as an error, until it does.
		view.rerender(
			<QueryClientProvider client={queryClient}>
				<CloudSessionChatSurface session={{ ...deleted, isTerminated: false }} />
			</QueryClientProvider>,
		);
		expect(cloudMocks.listChatModels).not.toHaveBeenCalled();
		expect(cloudMocks.chatProps.mock.lastCall?.[0].commandError).toBeUndefined();
		view.rerender(
			<QueryClientProvider client={queryClient}>
				<CloudSessionChatSurface session={{ ...deleted, isTerminated: false, runtimeConnected: true, terminalGeneration: "15704" }} />
			</QueryClientProvider>,
		);
		await waitFor(() => expect(cloudMocks.chatProps.mock.lastCall?.[0].models).toEqual([
			{ id: "codex-test", displayName: "Codex Test", default: true },
		]));
		expect(cloudMocks.listChatModels).toHaveBeenCalledTimes(1);
		expect(cloudMocks.chatProps.mock.lastCall?.[0].commandError).toBeUndefined();
	});

	it("says a session deleted under the composer must be restored", async () => {
		cloudMocks.listChatEvents.mockResolvedValue({ events: [], hasMore: false, nextAfter: 0 });
		cloudMocks.listChatModels.mockReset();
		cloudMocks.resumeSession.mockReset().mockRejectedValue(
			new CloudCpError("This session was deleted. Restore it to continue.", { status: 409, code: "SESSION_TERMINATED" }),
		);
		const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
		// The session list has not caught up with the delete yet; its worker is gone.
		render(
			<QueryClientProvider client={queryClient}>
				<CloudSessionChatSurface session={{ ...session, runtimeConnected: false, cloud: { orgId: "org-1" } }} />
			</QueryClientProvider>,
		);
		await waitFor(() => expect(cloudMocks.chatProps.mock.lastCall?.[0].commandError)
			.toBe("This session was deleted. Restore it to continue."));
		expect(cloudMocks.listChatModels).not.toHaveBeenCalled();
	});

	it("shows provider models and sends the selected model and effort with the next turn", async () => {
		cloudMocks.listChatEvents.mockResolvedValue({ events: [], hasMore: false, nextAfter: 0 });
		cloudMocks.listChatModels.mockResolvedValue({ models: [{ id: "codex-test", displayName: "Codex Test", default: true, efforts: ["low", "high"] }] });
		cloudMocks.sendSessionMessage.mockResolvedValue({ event: {} });
		const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
		render(
			<QueryClientProvider client={queryClient}>
				<CloudSessionChatSurface session={{ ...session, cloud: { orgId: "org-1" } }} />
			</QueryClientProvider>,
		);
		await waitFor(() => expect(cloudMocks.chatProps.mock.lastCall?.[0].models).toEqual([
			{ id: "codex-test", displayName: "Codex Test", default: true, efforts: ["low", "high"] },
		]));
		expect(cloudMocks.chatProps.mock.lastCall?.[0].onChooseSettings).toBeTypeOf("function");
		cloudMocks.chatProps.mock.lastCall?.[0].onChooseSettings({ model: "codex-test", reasoningEffort: "high" });
		await cloudMocks.chatProps.mock.lastCall?.[0].onSend("hello", [], "message-2");
		expect(cloudMocks.sendSessionMessage).toHaveBeenCalledWith("org-1", session.id, {
			text: "hello", model: "codex-test", reasoningEffort: "high",
		}, { idempotencyKey: "message-2" });
	});

	it("uses the Codex model and effort last selected in the TUI", async () => {
		cloudMocks.listChatEvents.mockResolvedValue({ events: [], hasMore: false, nextAfter: 0 });
		cloudMocks.listChatModels.mockResolvedValue({
			models: [{ id: "tui-model", displayName: "TUI Model", default: false, efforts: ["low", "high"] }],
			model: "tui-model", reasoningEffort: "high",
		});
		cloudMocks.sendSessionMessage.mockResolvedValue({ event: {} });
		localStorage.setItem("cloud-chat-settings:org-1:session-1:codex", JSON.stringify({ model: "old-chat-model", reasoningEffort: "low" }));
		const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
		render(<QueryClientProvider client={queryClient}>
			<CloudSessionChatSurface session={{ ...session, cloud: { orgId: "org-1" } }} />
		</QueryClientProvider>);
		await waitFor(() => expect(cloudMocks.chatProps.mock.lastCall?.[0].snapshot.settings).toMatchObject({ model: "tui-model", reasoningEffort: "high" }));
		await cloudMocks.chatProps.mock.lastCall?.[0].onSend("continue", [], "native-settings");
		expect(cloudMocks.sendSessionMessage).toHaveBeenLastCalledWith("org-1", session.id, {
			text: "continue", model: "tui-model", reasoningEffort: "high",
		}, { idempotencyKey: "native-settings" });
	});

	it("keeps a Chat selector change made while the native model request is in flight", async () => {
		cloudMocks.listChatEvents.mockResolvedValue({ events: [], hasMore: false, nextAfter: 0 });
		let resolveModels!: (value: { models: { id: string; displayName: string; default: boolean; efforts: string[] }[]; model: string; reasoningEffort: string }) => void;
		cloudMocks.listChatModels.mockReturnValue(new Promise((resolve) => { resolveModels = resolve; }));
		const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
		render(<QueryClientProvider client={queryClient}>
			<CloudSessionChatSurface session={{ ...session, cloud: { orgId: "org-1" } }} />
		</QueryClientProvider>);
		await waitFor(() => expect(cloudMocks.listChatModels).toHaveBeenCalled());
		act(() => cloudMocks.chatProps.mock.lastCall?.[0].onChooseSettings({ model: "new-chat-model", reasoningEffort: "xhigh" }));
		await act(async () => resolveModels({
			models: [{ id: "old-tui-model", displayName: "Old TUI Model", default: false, efforts: ["low"] }],
			model: "old-tui-model", reasoningEffort: "low",
		}));
		expect(cloudMocks.chatProps.mock.lastCall?.[0].snapshot.settings).toMatchObject({ model: "new-chat-model", reasoningEffort: "xhigh" });
	});

	it.each(["worker", "orchestrator"] as const)("shows Claude models for a %s and restores its native selection", async (kind) => {
		cloudMocks.listChatEvents.mockResolvedValue({ events: [], hasMore: false, nextAfter: 0 });
		cloudMocks.listChatModels.mockReset().mockResolvedValue({
			models: [{ id: "claude-sonnet", displayName: "Claude Sonnet", default: true, efforts: ["medium", "high"] }],
			model: "claude-sonnet", reasoningEffort: "high",
		});
		cloudMocks.sendSessionMessage.mockResolvedValue({ event: {} });
		const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
		render(
			<QueryClientProvider client={queryClient}>
				<CloudSessionChatSurface session={{ ...session, kind, provider: "claude-code", cloud: { orgId: "org-1" } }} />
			</QueryClientProvider>,
		);
		await waitFor(() => expect(cloudMocks.chatProps.mock.lastCall?.[0].models).toEqual([
			{ id: "claude-sonnet", displayName: "Claude Sonnet", default: true, efforts: ["medium", "high"] },
		]));
		await waitFor(() => expect(cloudMocks.chatProps.mock.lastCall?.[0].snapshot.settings).toMatchObject({ model: "claude-sonnet", reasoningEffort: "high" }));
		await cloudMocks.chatProps.mock.lastCall?.[0].onSend("continue", [], `claude-${kind}`);
		expect(cloudMocks.sendSessionMessage).toHaveBeenLastCalledWith("org-1", session.id, {
			text: "continue", model: "claude-sonnet", reasoningEffort: "high",
		}, { idempotencyKey: `claude-${kind}` });
	});

	it.each([
		["claude-code", "trusted", ["default", "accept-edits", "auto", "bypass-permissions"], "accept-edits"],
		["cursor", "trusted", ["default", "accept-edits", "auto", "bypass-permissions"], "auto"],
		["codex", "trusted", ["default", "accept-edits", "auto", "bypass-permissions"], "accept-edits"],
	] as const)("offers Cloud %s approval modes and sends the selected policy", async (provider, ceiling, modes, chosen) => {
		cloudMocks.listChatEvents.mockResolvedValue({ events: [], hasMore: false, nextAfter: 0 });
		cloudMocks.listChatModels.mockResolvedValue({ models: [] });
		cloudMocks.sendSessionMessage.mockResolvedValue({ event: {} });
		const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
		render(
			<QueryClientProvider client={queryClient}>
				<CloudSessionChatSurface session={{ ...session, provider, cloud: { orgId: "org-1", permissionMode: ceiling } }} />
			</QueryClientProvider>,
		);
		const props = cloudMocks.chatProps.mock.lastCall?.[0];
		expect(props.configOptions).toBeUndefined();
		expect(props.approvalModes).toEqual(modes);
		act(() => props.onChooseSettings({ approvalMode: chosen }, "approvalMode"));
		await props.onSend("hello", [], `mode-${provider}`);
		expect(cloudMocks.sendSessionMessage).toHaveBeenLastCalledWith("org-1", session.id, {
			text: "hello", mode: ceiling, approvalMode: chosen,
		}, { idempotencyKey: `mode-${provider}` });
	});

	it("does not offer a mode above the Cloud session ceiling", async () => {
		cloudMocks.listChatEvents.mockResolvedValue({ events: [], hasMore: false, nextAfter: 0 });
		cloudMocks.sendSessionMessage.mockResolvedValue({ event: {} });
		const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
		render(
			<QueryClientProvider client={queryClient}>
				<CloudSessionChatSurface session={{ ...session, cloud: { orgId: "org-1", permissionMode: "standard" } }} />
			</QueryClientProvider>,
		);
		const props = cloudMocks.chatProps.mock.lastCall?.[0];
		expect(props.approvalModes).toEqual(["accept-edits", "auto"]);
		expect(props.snapshot.settings?.approvalMode).toBe("auto");
		act(() => props.onChooseSettings({ approvalMode: "bypass-permissions" }, "approvalMode"));
		await props.onSend("hello", [], "capped-message");
		expect(cloudMocks.sendSessionMessage).toHaveBeenLastCalledWith("org-1", session.id, {
			text: "hello", mode: "standard",
		}, { idempotencyKey: "capped-message" });
	});

	it("does not carry one Cloud session's model selection into another session", async () => {
		cloudMocks.listChatEvents.mockResolvedValue({ events: [], hasMore: false, nextAfter: 0 });
		cloudMocks.listChatModels.mockResolvedValue({ models: [{ id: "codex-test", displayName: "Codex Test", default: true }] });
		cloudMocks.sendSessionMessage.mockResolvedValue({ event: {} });
		const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
		const view = render(
			<QueryClientProvider client={queryClient}>
				<CloudSessionChatSurface session={{ ...session, cloud: { orgId: "org-1" } }} />
			</QueryClientProvider>,
		);
		await waitFor(() => expect(cloudMocks.chatProps.mock.lastCall?.[0].onChooseSettings).toBeTypeOf("function"));
		cloudMocks.chatProps.mock.lastCall?.[0].onChooseSettings({ model: "codex-test" });
		view.rerender(
			<QueryClientProvider client={queryClient}>
				<CloudSessionChatSurface session={{ ...session, id: "session-2", cloud: { orgId: "org-1" } }} />
			</QueryClientProvider>,
		);
		await cloudMocks.chatProps.mock.lastCall?.[0].onSend("next", [], "message-3");
		expect(cloudMocks.sendSessionMessage).toHaveBeenLastCalledWith("org-1", "session-2", { text: "next" }, { idempotencyKey: "message-3" });
	});

	it("does not carry Codex selections into another harness in the same session", async () => {
		cloudMocks.listChatEvents.mockResolvedValue({ events: [], hasMore: false, nextAfter: 0 });
		cloudMocks.listChatModels.mockResolvedValue({ models: [{ id: "codex-test", displayName: "Codex Test", default: true }] });
		cloudMocks.sendSessionMessage.mockResolvedValue({ event: {} });
		const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
		const view = render(
			<QueryClientProvider client={queryClient}>
				<CloudSessionChatSurface session={{ ...session, cloud: { orgId: "org-1", permissionMode: "trusted" } }} />
			</QueryClientProvider>,
		);
		await waitFor(() => expect(cloudMocks.chatProps.mock.lastCall?.[0].onChooseSettings).toBeTypeOf("function"));
		act(() => cloudMocks.chatProps.mock.lastCall?.[0].onChooseSettings({ model: "codex-test" }));
		view.rerender(
			<QueryClientProvider client={queryClient}>
				<CloudSessionChatSurface session={{ ...session, provider: "claude-code", cloud: { orgId: "org-1", permissionMode: "trusted" } }} />
			</QueryClientProvider>,
		);
		await cloudMocks.chatProps.mock.lastCall?.[0].onSend("next", [], "after-handoff");
		expect(cloudMocks.sendSessionMessage).toHaveBeenLastCalledWith("org-1", session.id, {
			text: "next", mode: "trusted",
		}, { idempotencyKey: "after-handoff" });
	});

	it("shows a trusted Chat's default without sending it and keeps the user's own choice", async () => {
		cloudMocks.listChatEvents.mockResolvedValue({ events: [], hasMore: false, nextAfter: 0 });
		cloudMocks.listChatModels.mockResolvedValue({ models: [] });
		cloudMocks.sendSessionMessage.mockResolvedValue({ event: {} });
		const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
		const view = render(
			<QueryClientProvider client={queryClient}>
				<CloudSessionChatSurface session={{ ...session, provider: "claude-code", cloud: { orgId: "org-1", permissionMode: "trusted" } }} />
			</QueryClientProvider>,
		);
		await waitFor(() => expect(cloudMocks.chatProps.mock.lastCall?.[0].onSend).toBeTypeOf("function"));
		expect(cloudMocks.chatProps.mock.lastCall?.[0].snapshot.settings?.approvalMode).toBe("bypass-permissions");
		await cloudMocks.chatProps.mock.lastCall?.[0].onSend("first", [], "trusted-1");
		// The worker derives the mode from the session's, stepping down when
		// the adapter does not offer it.
		expect(cloudMocks.sendSessionMessage).toHaveBeenLastCalledWith("org-1", session.id, {
			text: "first", mode: "trusted",
		}, { idempotencyKey: "trusted-1" });
		// The settings bar sends the shown settings back with an effort change.
		const shown = cloudMocks.chatProps.mock.lastCall?.[0].snapshot.settings;
		act(() => cloudMocks.chatProps.mock.lastCall?.[0].onChooseSettings({ ...shown, reasoningEffort: "high" }));
		await cloudMocks.chatProps.mock.lastCall?.[0].onSend("effort", [], "trusted-effort");
		expect(cloudMocks.sendSessionMessage).toHaveBeenLastCalledWith("org-1", session.id, {
			text: "effort", reasoningEffort: "high", mode: "trusted",
		}, { idempotencyKey: "trusted-effort" });
		expect(JSON.parse(localStorage.getItem("cloud-chat-settings:org-1:session-1:claude-code") ?? "{}")).not.toHaveProperty("approvalMode");
		act(() => cloudMocks.chatProps.mock.lastCall?.[0].onChooseSettings({ approvalMode: "default" }, "approvalMode"));
		await cloudMocks.chatProps.mock.lastCall?.[0].onSend("second", [], "trusted-2");
		expect(cloudMocks.sendSessionMessage).toHaveBeenLastCalledWith("org-1", session.id, {
			text: "second", reasoningEffort: "high", mode: "trusted", approvalMode: "default",
		}, { idempotencyKey: "trusted-2" });
		// The pick is saved as one and survives a remount.
		view.unmount();
		render(
			<QueryClientProvider client={queryClient}>
				<CloudSessionChatSurface session={{ ...session, provider: "claude-code", cloud: { orgId: "org-1", permissionMode: "trusted" } }} />
			</QueryClientProvider>,
		);
		await waitFor(() => expect(cloudMocks.chatProps.mock.lastCall?.[0].snapshot.settings?.approvalMode).toBe("default"));
		await cloudMocks.chatProps.mock.lastCall?.[0].onSend("third", [], "trusted-3");
		expect(cloudMocks.sendSessionMessage).toHaveBeenLastCalledWith("org-1", session.id, {
			text: "third", reasoningEffort: "high", mode: "trusted", approvalMode: "default",
		}, { idempotencyKey: "trusted-3" });
	});

	it("sends the shown default once the user picks it from the approval menu", async () => {
		cloudMocks.listChatEvents.mockResolvedValue({ events: [], hasMore: false, nextAfter: 0 });
		cloudMocks.listChatModels.mockResolvedValue({ models: [] });
		cloudMocks.sendSessionMessage.mockResolvedValue({ event: {} });
		const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
		render(
			<QueryClientProvider client={queryClient}>
				<CloudSessionChatSurface session={{ ...session, provider: "claude-code", cloud: { orgId: "org-1", permissionMode: "trusted" } }} />
			</QueryClientProvider>,
		);
		await waitFor(() => expect(cloudMocks.chatProps.mock.lastCall?.[0].onSend).toBeTypeOf("function"));
		const shown = cloudMocks.chatProps.mock.lastCall?.[0].snapshot.settings;
		expect(shown?.approvalMode).toBe("bypass-permissions");
		act(() => cloudMocks.chatProps.mock.lastCall?.[0].onChooseSettings({ ...shown, approvalMode: "bypass-permissions" }, "approvalMode"));
		await cloudMocks.chatProps.mock.lastCall?.[0].onSend("go", [], "picked-default");
		expect(cloudMocks.sendSessionMessage).toHaveBeenLastCalledWith("org-1", session.id, {
			text: "go", mode: "trusted", approvalMode: "bypass-permissions",
		}, { idempotencyKey: "picked-default" });
		expect(JSON.parse(localStorage.getItem("cloud-chat-settings:org-1:session-1:claude-code") ?? "{}")).toMatchObject({ approvalMode: "bypass-permissions" });
	});

	it("sends the approval mode picked from the chat's own settings bar with the next turn", async () => {
		cloudMocks.listChatEvents.mockResolvedValue({ events: [], hasMore: false, nextAfter: 0 });
		cloudMocks.listChatModels.mockResolvedValue({ models: [] });
		cloudMocks.sendSessionMessage.mockResolvedValue({ event: {} });
		const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
		render(
			<QueryClientProvider client={queryClient}>
				<CloudSessionChatSurface session={{ ...session, provider: "claude-code", cloud: { orgId: "org-1", permissionMode: "trusted" } }} />
			</QueryClientProvider>,
		);
		await waitFor(() => expect(cloudMocks.chatProps.mock.lastCall?.[0].onSend).toBeTypeOf("function"));
		const props = cloudMocks.chatProps.mock.lastCall?.[0];
		expect(props.snapshot.settings?.approvalMode).toBe("bypass-permissions");
		// The bar as ChatWorkspace draws it for this chat: the unified one, with the agent picker.
		const user = userEvent.setup();
		render(
			<QueryClientProvider client={queryClient}>
				<TurnSettingsBar harness={props.snapshot.harness} models={props.models ?? []} settings={props.snapshot.settings}
					showApprovalMode={props.showApprovalMode} approvalModes={props.approvalModes} approvalModeLimits={props.approvalModeLimits}
					onChange={props.onChooseSettings} onRememberPermissions={props.onRememberPermissions} agentSwitch={props.agentSwitch} />
			</QueryClientProvider>,
		);
		await user.click(screen.getByRole("button", { name: "Approval policy for the next turn" }));
		await user.click(screen.getByRole("menuitemradio", { name: /Accept edits/ }));
		await waitFor(() => expect(cloudMocks.chatProps.mock.lastCall?.[0].snapshot.settings?.approvalMode).toBe("accept-edits"));
		await cloudMocks.chatProps.mock.lastCall?.[0].onSend("go", [], "picked-from-bar");
		expect(cloudMocks.sendSessionMessage).toHaveBeenLastCalledWith("org-1", session.id, {
			text: "go", mode: "trusted", approvalMode: "accept-edits",
		}, { idempotencyKey: "picked-from-bar" });
		expect(JSON.parse(localStorage.getItem("cloud-chat-settings:org-1:session-1:claude-code") ?? "{}")).toMatchObject({ approvalMode: "accept-edits" });
	});

	it("stops showing a pick the session's mode no longer allows", async () => {
		cloudMocks.listChatEvents.mockResolvedValue({ events: [], hasMore: false, nextAfter: 0 });
		cloudMocks.listChatModels.mockResolvedValue({ models: [] });
		cloudMocks.sendSessionMessage.mockResolvedValue({ event: {} });
		const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
		const view = render(
			<QueryClientProvider client={queryClient}>
				<CloudSessionChatSurface session={{ ...session, provider: "codex", cloud: { orgId: "org-1", permissionMode: "trusted" } }} />
			</QueryClientProvider>,
		);
		await waitFor(() => expect(cloudMocks.chatProps.mock.lastCall?.[0].onSend).toBeTypeOf("function"));
		act(() => cloudMocks.chatProps.mock.lastCall?.[0].onChooseSettings({ approvalMode: "default" }, "approvalMode"));
		expect(cloudMocks.chatProps.mock.lastCall?.[0].snapshot.settings?.approvalMode).toBe("default");
		view.rerender(
			<QueryClientProvider client={queryClient}>
				<CloudSessionChatSurface session={{ ...session, provider: "codex", cloud: { orgId: "org-1", permissionMode: "standard" } }} />
			</QueryClientProvider>,
		);
		const props = cloudMocks.chatProps.mock.lastCall?.[0];
		expect(props.approvalModes).not.toContain("default");
		expect(props.snapshot.settings?.approvalMode).toBe("auto");
		await props.onSend("go", [], "dropped-mode");
		expect(cloudMocks.sendSessionMessage).toHaveBeenLastCalledWith("org-1", session.id, {
			text: "go", mode: "standard",
		}, { idempotencyKey: "dropped-mode" });
	});

	it("ignores an approval mode an older build saved with the model catalog", async () => {
		cloudMocks.listChatEvents.mockResolvedValue({ events: [], hasMore: false, nextAfter: 0 });
		cloudMocks.listChatModels.mockResolvedValue({
			models: [{ id: "claude-sonnet", displayName: "Claude Sonnet", default: true, efforts: ["medium", "high"] }],
			model: "claude-sonnet", reasoningEffort: "medium",
		});
		cloudMocks.sendSessionMessage.mockResolvedValue({ event: {} });
		const key = "cloud-chat-settings:org-1:session-1:claude-code";
		localStorage.setItem(key, JSON.stringify({ model: "claude-sonnet", reasoningEffort: "medium", approvalMode: "default" }));
		const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
		render(
			<QueryClientProvider client={queryClient}>
				<CloudSessionChatSurface session={{ ...session, provider: "claude-code", cloud: { orgId: "org-1", permissionMode: "trusted" } }} />
			</QueryClientProvider>,
		);
		await waitFor(() => expect(cloudMocks.listChatModels).toHaveBeenCalled());
		// The legacy value is never read as a pick: without approvalModeChosen it is ignored.
		await waitFor(() => expect(cloudMocks.chatProps.mock.lastCall?.[0].models.length).toBeGreaterThan(0));
		expect(cloudMocks.chatProps.mock.lastCall?.[0].snapshot.settings?.approvalMode).toBe("bypass-permissions");
		await cloudMocks.chatProps.mock.lastCall?.[0].onSend("go", [], "legacy-default");
		expect(cloudMocks.sendSessionMessage).toHaveBeenLastCalledWith("org-1", session.id, {
			text: "go", model: "claude-sonnet", reasoningEffort: "medium", mode: "trusted",
		}, { idempotencyKey: "legacy-default" });
	});

	it("keeps a picked model and effort when the worker's catalog is read again", async () => {
		cloudMocks.listChatEvents.mockResolvedValue({ events: [], hasMore: false, nextAfter: 0 });
		cloudMocks.listChatModels.mockReset().mockResolvedValue({
			models: [
				{ id: "gpt-6.1-sol", displayName: "GPT-6.1 Sol", default: true, efforts: ["low", "high"] },
				{ id: "gpt-6-luna", displayName: "GPT-6 Luna", default: false, efforts: ["medium", "high"] },
			],
			model: "gpt-6.1-sol",
		});
		const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
		const codexSession = { ...session, provider: "codex" as const, cloud: { orgId: "org-1", permissionMode: "trusted" as const } };
		const view = render(<QueryClientProvider client={queryClient}><CloudSessionChatSurface session={codexSession} /></QueryClientProvider>);
		await waitFor(() => expect(cloudMocks.chatProps.mock.lastCall?.[0].snapshot.settings?.model).toBe("gpt-6.1-sol"));
		act(() => cloudMocks.chatProps.mock.lastCall?.[0].onChooseSettings({ model: "gpt-6-luna", reasoningEffort: "high" }, "model"));
		await waitFor(() => expect(cloudMocks.chatProps.mock.lastCall?.[0].snapshot.settings).toMatchObject({ model: "gpt-6-luna", reasoningEffort: "high" }));
		// Leaving and coming back reads the catalog again; it still reports the worker's own model.
		view.unmount();
		const calls = cloudMocks.listChatModels.mock.calls.length;
		render(<QueryClientProvider client={queryClient}><CloudSessionChatSurface session={codexSession} /></QueryClientProvider>);
		await waitFor(() => expect(cloudMocks.listChatModels.mock.calls.length).toBeGreaterThan(calls));
		await new Promise((resolve) => setTimeout(resolve, 50));
		expect(cloudMocks.chatProps.mock.lastCall?.[0].snapshot.settings).toMatchObject({ model: "gpt-6-luna", reasoningEffort: "high" });

		// A model changed in the terminal since then is the worker's new one, and applies.
		cloudMocks.listChatModels.mockResolvedValue({
			models: [
				{ id: "gpt-6.1-sol", displayName: "GPT-6.1 Sol", default: true, efforts: ["low", "high"] },
				{ id: "gpt-6-luna", displayName: "GPT-6 Luna", default: false, efforts: ["medium", "high"] },
			],
			model: "gpt-6.1-sol", reasoningEffort: "high",
		});
		await act(async () => { await queryClient.refetchQueries({ queryKey: ["cloud-chat-models"] }); });
		await waitFor(() => expect(cloudMocks.chatProps.mock.lastCall?.[0].snapshot.settings).toMatchObject({ model: "gpt-6.1-sol", reasoningEffort: "high" }));
	});

	it("surfaces send errors and sends cancellation to the active turn", async () => {
		cloudMocks.listChatEvents.mockReset().mockResolvedValue({
			events: [
				{ sessionId: session.id, sequence: 1, type: "chat.user_message", payload: { text: "Run", turnId: "turn-1" }, createdAt: session.updatedAt },
				{ sessionId: session.id, sequence: 2, type: "chat.turn_started", payload: { turnId: "turn-1" }, createdAt: session.updatedAt },
			],
			hasMore: false,
			nextAfter: 2,
		});
		cloudMocks.sendSessionMessage.mockReset().mockRejectedValue(new Error("send failed"));
		cloudMocks.cancelTurn.mockReset().mockRejectedValue(new Error("cancel failed"));
		cloudMocks.chatProps.mockClear();
		const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
		render(
			<QueryClientProvider client={queryClient}>
				<CloudSessionChatSurface session={{ ...session, cloud: { orgId: "org-1" } }} />
			</QueryClientProvider>,
		);
		await waitFor(() => expect(cloudMocks.chatProps.mock.lastCall?.[0].onInterrupt).toBeTypeOf("function"));
		const props = cloudMocks.chatProps.mock.lastCall?.[0];
		expect(props.onSteer).toBeUndefined();
		await expect(props.onSend("hello", [], "message-1")).rejects.toThrow("send failed");
		await waitFor(() => expect(cloudMocks.chatProps.mock.lastCall?.[0].commandError).toBe("send failed"));
		cloudMocks.chatProps.mock.lastCall?.[0].onInterrupt();
		await waitFor(() => expect(cloudMocks.cancelTurn).toHaveBeenCalledWith("org-1", session.id, "turn-1"));
		await waitFor(() => expect(cloudMocks.chatProps.mock.lastCall?.[0].commandError).toBe("cancel failed"));
	});

	it("appends only newer events without duplicating replayed pages", () => {
		const event = (sequence: number): CloudCpClientEvent => ({
			sessionId: session.id,
			sequence,
			type: "chat.assistant_delta",
			payload: { text: String(sequence) },
			createdAt: session.updatedAt,
		});
		const existing = [event(1), event(500)];
		const merged = appendCloudEvents(existing, [event(500), event(501)]);
		expect(merged.map((item) => item.sequence)).toEqual([1, 500, 501]);
		expect(merged.at(-1)?.sequence).toBe(501);
	});

	it("opens a file the agent linked by its sandbox path as the workspace file", async () => {
		const search = vi.fn(async () => ["NOTES.md", "docs/NOTES.md", "src/app/page.tsx"]);
		await expect(cloudWorkspaceFilePath("NOTES.md", search)).resolves.toBe("NOTES.md");
		await expect(cloudWorkspaceFilePath("/workspace/repository/src/app/page.tsx", search)).resolves.toBe("src/app/page.tsx");
		expect(search).not.toHaveBeenCalled();
		// A Coder workspace's own directory: the longest workspace path the link ends with.
		await expect(cloudWorkspaceFilePath("/home/coder/project/docs/NOTES.md", search)).resolves.toBe("docs/NOTES.md");
		expect(search).toHaveBeenLastCalledWith("NOTES.md");
		await expect(cloudWorkspaceFilePath("/etc/hosts", search)).resolves.toBeUndefined();
	});

	it("opens a file the agent just wrote to its artifact directory in Artifacts, before the session lists it", async () => {
		// The file view reads through the checkout and refuses the artifact
		// directory (HTTP 422), so the click never goes there, listed or not.
		cloudMocks.listChatEvents.mockReset().mockResolvedValue({ events: [], hasMore: false, nextAfter: 0, hasOlder: false, nextBefore: CLOUD_CHAT_NEWEST });
		cloudMocks.listChatModels.mockResolvedValue({ models: [] });
		cloudMocks.chatProps.mockClear();
		useUiStore.setState({ workspaceFileOpenRequest: null });
		const onOpenFile = vi.fn();
		const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
		const invalidate = vi.spyOn(queryClient, "invalidateQueries");
		const listed = { kind: "markdown" as const, name: "plan.md", path: "plan.md", size: 1, updatedAt: session.updatedAt };
		const surface = (cloud: { orgId: string; shared?: boolean }) =>
			<QueryClientProvider client={queryClient}><CloudSessionChatSurface session={{ ...session, cloud, artifactFiles: [listed] }} onOpenFile={onOpenFile} /></QueryClientProvider>;
		const view = render(surface({ orgId: "org-1" }));
		await waitFor(() => expect(cloudMocks.chatProps).toHaveBeenCalled());
		const openFile = (path: string) => act(() => cloudMocks.chatProps.mock.lastCall?.[0].onOpenFile(path));

		openFile("/workspace/.ao/worker/artifacts/session-1/Migration Guide.md");
		expect(useUiStore.getState().workspaceFileOpenRequest).toMatchObject({ sessionId: "session-1", path: "Migration Guide.md", source: "artifact" });
		// Not listed yet: the session list is read again now, not at its next poll.
		expect(invalidate).toHaveBeenCalledExactlyOnceWith({ queryKey: cloudSessionsQueryKey });
		expect(onOpenFile).not.toHaveBeenCalled();

		// A listed one opens without a read. (A link's query is cut where the link
		// is read, in ChatMarkdown; this opener gets file paths.)
		openFile("/workspace/.ao/worker/artifacts/session-1/plan.md");
		expect(useUiStore.getState().workspaceFileOpenRequest).toMatchObject({ sessionId: "session-1", path: "plan.md", source: "artifact" });
		expect(invalidate).toHaveBeenCalledTimes(1);

		// "#" and "?" in a file name are part of it.
		openFile("/workspace/.ao/worker/artifacts/session-1/issue #42?.md");
		expect(useUiStore.getState().workspaceFileOpenRequest).toMatchObject({ sessionId: "session-1", path: "issue #42?.md", source: "artifact" });
		expect(invalidate).toHaveBeenCalledTimes(2);

		// A session shared with this person is listed with the shared projects.
		view.rerender(surface({ orgId: "org-1", shared: true }));
		openFile("/workspace/.ao/worker/artifacts/session-1/fresh.md");
		expect(invalidate).toHaveBeenLastCalledWith({ queryKey: sharedCloudWorkspacesQueryKey });

		// A checkout file still opens in the workspace.
		openFile("/workspace/repository/src/app.ts");
		await waitFor(() => expect(onOpenFile).toHaveBeenCalledExactlyOnceWith("src/app.ts"));
	});

	it("merges streamed and polled events in order without dropping either side's", () => {
		const event = (sequence: number): CloudCpClientEvent => ({
			sessionId: session.id, sequence, type: "chat.assistant_delta",
			payload: { text: String(sequence) }, createdAt: session.updatedAt,
		});
		// The stream got further than the page the poll is about to write.
		const streamed = [event(1), event(2), event(3), event(4)];
		const polled = [event(1), event(2), event(3)];
		expect(mergeCloudEvents(streamed, polled)).toBe(streamed);
		expect(mergeCloudEvents(polled, streamed).map((item) => item.sequence)).toEqual([1, 2, 3, 4]);
		expect(mergeCloudEvents([event(1), event(4)], [event(2), event(3)]).map((item) => item.sequence)).toEqual([1, 2, 3, 4]);
		expect(mergeCloudEvents([event(1)], [event(2), event(3)]).map((item) => item.sequence)).toEqual([1, 2, 3]);
	});

	it("carries the worker's plan, context use, subagent parents and approval subject into the snapshot", () => {
		const event = (sequence: number, type: string, payload: unknown): CloudCpClientEvent => ({
			sessionId: session.id, sequence, type, payload, createdAt: session.updatedAt,
		});
		const snapshot = toSnapshot({ ...session, provider: "claude-code" }, [
			event(1, "chat.user_message", { text: "Plan it", turnId: "turn-1" }),
			event(2, "chat.turn_started", { turnId: "turn-1" }),
			event(3, "chat.activity", { turnId: "turn-1", activity: { id: "plan", kind: "turn_plan", status: "completed", detail: { plan: { steps: [{ text: "Read", status: "completed" }, { text: "Write", status: "in_progress" }] } } } }),
			event(4, "chat.activity", { turnId: "turn-1", activity: { id: "plan", kind: "turn_plan", status: "completed", detail: { plan: { explanation: "Two steps", steps: [{ text: "Read", status: "completed" }, { text: "Write", status: "completed" }], spansTurns: true } } } }),
			event(5, "chat.activity", { turnId: "turn-1", activity: { id: "usage", kind: "usage", status: "completed", detail: { usage: { contextUsed: 50_000, contextWindow: 200_000, inputTokens: 10, outputTokens: 5, cachedTokens: 0, totalTokens: 15 } } } }),
			event(6, "chat.activity", { turnId: "turn-1", activity: { id: "task-1", kind: "mcp_tool", status: "running", summary: "Task", detail: { toolName: "Task" } } }),
			event(7, "chat.activity", { turnId: "turn-1", activity: { id: "cmd-1", kind: "command", status: "completed", summary: "ls", detail: { command: "ls", exitCode: 0, parentProviderItemId: "task-1" } } }),
			event(8, "chat.approval_requested", { turnId: "turn-1", requestId: "req-1", summary: "Edit main.go", subjectKind: "file_change", decisions: [] }),
		]);
		expect(snapshot.turns[0].plan).toEqual({ explanation: "Two steps", steps: [{ text: "Read", status: "completed" }, { text: "Write", status: "completed" }], spansTurns: true });
		expect(snapshot.usage).toMatchObject({ contextUsed: 50_000, contextWindow: 200_000 });
		expect(snapshot.items.some((item) => item.kind === "activity" && (item.activityKind as string) === "turn_plan")).toBe(false);
		const command = snapshot.items.find((item) => item.kind === "activity" && item.activityKind === "command");
		expect(command).toMatchObject({ providerItemId: "cmd-1", detail: { exitCode: 0, parentProviderItemId: "task-1" } });
		const approval = snapshot.items.find((item) => item.kind === "activity" && item.activityKind === "approval");
		expect(approval).toMatchObject({ detail: { subjectKind: "file_change" } });
	});

	it("marks a stopped turn's unfinished steps as stopped, not of unknown outcome", () => {
		const event = (sequence: number, type: string, payload: unknown): CloudCpClientEvent => ({
			sessionId: session.id, sequence, type, payload, createdAt: session.updatedAt,
		});
		const steps = [
			event(1, "chat.user_message", { text: "Go", turnId: "turn-1" }),
			event(2, "chat.turn_started", { turnId: "turn-1" }),
			event(3, "chat.activity", { turnId: "turn-1", activity: { id: "cmd-1", kind: "command", status: "running", summary: "sleep", detail: { command: "sleep 100" } } }),
		];
		const stopped = toSnapshot(session, [...steps, event(4, "chat.turn_interrupted", { turnId: "turn-1" })]);
		expect(stopped.items.find((item) => item.kind === "activity")).toMatchObject({ status: "cancelled" });
		const failed = toSnapshot(session, [...steps, event(4, "chat.turn_aborted", { turnId: "turn-1", error: "boom" })]);
		expect(failed.items.find((item) => item.kind === "activity")).toMatchObject({ status: "recovered" });
	});

	it("reads a turn whose start is not loaded as started, and marks older history", () => {
		const event = (sequence: number, type: string, payload: unknown): CloudCpClientEvent => ({
			sessionId: session.id, sequence, type, payload, createdAt: session.updatedAt,
		});
		// The newest events begin inside turn-1: its prompt and start are older.
		const events = [
			event(50, "chat.assistant_delta", { turnId: "turn-1", text: "Still working" }),
			event(51, "chat.user_message", { text: "Next", turnId: "turn-2" }),
		];
		const partial = toSnapshot(session, events, undefined, { partial: true });
		expect(partial.turns.map((turn) => [turn.id, turn.state])).toEqual([["turn-1", "running"], ["turn-2", "queued"]]);
		expect(partial).toMatchObject({ hasMoreBefore: true, oldestSequence: 50, latestSequence: 51 });
		const settled = toSnapshot(session, [...events, event(52, "chat.turn_completed", { turnId: "turn-1" })], undefined, { partial: true });
		expect(settled.turns.map((turn) => [turn.id, turn.state])).toEqual([["turn-1", "completed"], ["turn-2", "running"]]);
		// The whole history from the session's start reads as it always did.
		expect(toSnapshot(session, events)).toMatchObject({ hasMoreBefore: false });
	});

	it("keeps a later Setup where it ran when the session's first prompt is not loaded", () => {
		const step = (sequence: number): CloudCpClientEvent => ({
			sessionId: session.id, sequence, type: "workspace.script.step", createdAt: "2026-10-20T16:02:01Z",
			payload: { phase: "setup", runId: "reopen", step: 1, total: 1, status: "succeeded", exitCode: 0, durationMs: 10, command: "./setup.sh", repository: "", startedAt: "2026-10-20T16:02:01Z" },
		});
		const events: CloudCpClientEvent[] = [
			{ sessionId: session.id, sequence: 60, type: "chat.user_message", payload: { text: "Wake up", turnId: "turn-9" }, createdAt: "2026-10-20T16:02:00Z" },
			step(61),
			{ sessionId: session.id, sequence: 62, type: "chat.turn_started", payload: { turnId: "turn-9" }, createdAt: "2026-10-20T16:02:02Z" },
		];
		const order = (items: { kind: string; detail?: { event?: string } }[]) =>
			items.map((item) => item.detail?.event === "workspace.script.run" ? "setup" : item.kind);
		// From the session's start this is its first prompt, which its Setup leads.
		expect(order(toSnapshot(session, events).items)).toEqual(["setup", "message"]);
		// Here it is only the first one loaded: the Setup stays after it, as it does once older pages load.
		expect(order(toSnapshot(session, events, undefined, { partial: true }).items)).toEqual(["message", "setup"]);
	});

	it("shows the newest page first, then the half hour before it, and older pages on demand", async () => {
		const at = (minutes: number) => new Date(Date.UTC(2026, 10, 8, 10, minutes)).toISOString();
		const event = (sequence: number, type: string, payload: unknown, createdAt: string): CloudCpClientEvent => ({ sessionId: session.id, sequence, type, payload, createdAt });
		const newest = [
			event(90, "chat.user_message", { text: "Latest", turnId: "turn-3" }, at(50)),
			event(91, "chat.turn_started", { turnId: "turn-3" }, at(50)),
			event(92, "chat.turn_completed", { turnId: "turn-3" }, at(51)),
		];
		const recent = [
			event(70, "chat.user_message", { text: "Earlier", turnId: "turn-2" }, at(30)),
			event(71, "chat.turn_completed", { turnId: "turn-2" }, at(31)),
		];
		const oldest = [event(1, "chat.user_message", { text: "First", turnId: "turn-1" }, at(0)), event(2, "chat.turn_completed", { turnId: "turn-1" }, at(1))];
		let releaseRecent!: () => void;
		const recentHeld = new Promise<void>((resolve) => { releaseRecent = resolve; });
		cloudMocks.listChatEvents.mockReset().mockImplementation(async (_org: string, _session: string, query: { before?: number; after?: number; since?: string }) => {
			if (query.before === 9007199254740991 && query.after === undefined) return { events: newest, hasMore: false, nextAfter: 92, hasOlder: true, nextBefore: 90 };
			if (query.before === 90) {
				await recentHeld;
				return { events: recent, hasMore: false, nextAfter: 71, hasOlder: true, nextBefore: 70 };
			}
			if (query.before === 70) return { events: oldest, hasMore: false, nextAfter: 2, hasOlder: false, nextBefore: 1 };
			return { events: [], hasMore: false, nextAfter: query.after ?? 0, hasOlder: false, nextBefore: query.before };
		});
		cloudMocks.listChatModels.mockReset().mockResolvedValue({ models: [] });
		cloudMocks.chatProps.mockClear();
		const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
		render(<QueryClientProvider client={queryClient}><CloudSessionChatSurface session={{ ...session, cloud: { orgId: "org-1" } }} /></QueryClientProvider>);
		const props = () => cloudMocks.chatProps.mock.lastCall?.[0];
		const texts = () => (props()?.snapshot.items as { kind: string; text?: string }[]).filter((item) => item.kind === "message").map((item) => item.text);
		// The newest page shows while the half hour before it is still being read.
		await waitFor(() => expect(texts()).toEqual(["Latest"]));
		expect(props()).toMatchObject({ hasOlder: true, snapshot: { hasMoreBefore: true } });
		expect(cloudMocks.listChatEvents).toHaveBeenCalledWith("org-1", session.id, { before: 90, limit: 500, since: at(20) });
		releaseRecent();
		await waitFor(() => expect(texts()).toEqual(["Earlier", "Latest"]));
		expect(cloudMocks.listChatEvents).not.toHaveBeenCalledWith("org-1", session.id, expect.objectContaining({ before: 70 }));
		// Older pages only on demand, as the reader scrolls up.
		act(() => props().onLoadOlder());
		await waitFor(() => expect(texts()).toEqual(["First", "Earlier", "Latest"]));
		expect(props()).toMatchObject({ hasOlder: false, loadingOlder: false, snapshot: { hasMoreBefore: false } });
		expect(cloudMocks.listChatEvents).toHaveBeenCalledWith("org-1", session.id, { before: 70, limit: 200, since: undefined });
	});

	it("reads the whole history forward from a control plane that cannot read back", async () => {
		const event = (sequence: number, text: string): CloudCpClientEvent => ({
			sessionId: session.id, sequence, type: "chat.user_message", payload: { text, turnId: `turn-${sequence}` }, createdAt: session.updatedAt,
		});
		cloudMocks.listChatEvents.mockReset()
			.mockResolvedValueOnce({ events: [event(1, "One")], hasMore: true, nextAfter: 1 })
			.mockResolvedValueOnce({ events: [event(2, "Two")], hasMore: false, nextAfter: 2 })
			.mockResolvedValue({ events: [], hasMore: false, nextAfter: 2 });
		cloudMocks.listChatModels.mockReset().mockResolvedValue({ models: [] });
		cloudMocks.chatProps.mockClear();
		render(<QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}><CloudSessionChatSurface session={{ ...session, cloud: { orgId: "org-1" } }} /></QueryClientProvider>);
		const props = () => cloudMocks.chatProps.mock.lastCall?.[0];
		await waitFor(() => expect((props()?.snapshot.items as { text?: string }[]).map((item) => item.text)).toEqual(["One", "Two"]));
		expect(cloudMocks.listChatEvents.mock.calls.slice(0, 2).map((call) => call[2])).toEqual([{ before: 9007199254740991, limit: 150 }, { after: 1, limit: 500 }]);
		expect(props()?.hasOlder).toBeFalsy();
		expect(props()?.snapshot.hasMoreBefore).toBe(false);
	});

	it("projects completed and interrupted turns without leaving output streaming", () => {
		const events: CloudCpClientEvent[] = [
			{ sessionId: session.id, sequence: 1, type: "chat.user_message", payload: { text: "First", turnId: "turn-1" }, createdAt: session.updatedAt },
			{ sessionId: session.id, sequence: 2, type: "chat.turn_started", payload: { turnId: "turn-1" }, createdAt: session.updatedAt },
			{ sessionId: session.id, sequence: 3, type: "chat.assistant_delta", payload: { text: "Done", turnId: "turn-1" }, createdAt: session.updatedAt },
			{ sessionId: session.id, sequence: 4, type: "chat.turn_completed", payload: { turnId: "turn-1" }, createdAt: session.updatedAt },
			{ sessionId: session.id, sequence: 5, type: "chat.user_message", payload: { text: "Second", turnId: "turn-2" }, createdAt: session.updatedAt },
			{ sessionId: session.id, sequence: 6, type: "chat.turn_started", payload: { turnId: "turn-2" }, createdAt: session.updatedAt },
			{ sessionId: session.id, sequence: 7, type: "chat.turn_interrupted", payload: { turnId: "turn-2" }, createdAt: session.updatedAt },
		];
		const snapshot = toSnapshot(session, events);
		expect(snapshot.turns.map((turn) => turn.state)).toEqual(["completed", "interrupted"]);
		expect(snapshot.items).toContainEqual(expect.objectContaining({ role: "assistant", text: "Done", streaming: false }));
		expect(snapshot.controller.state).toBe("ready");
	});

	it("projects Codex tool activity and turn diffs beside the assistant reply", () => {
		const event = (sequence: number, type: string, payload: unknown): CloudCpClientEvent => ({
			sessionId: session.id, sequence, type, payload, createdAt: session.updatedAt,
		});
		const snapshot = toSnapshot(session, [
			event(1, "chat.turn_started", { turnId: "turn-1" }),
			event(2, "chat.activity", { turnId: "turn-1", activity: { id: "cmd-1", kind: "command", status: "running", summary: "go test", detail: { command: "go test" } } }),
			event(3, "chat.activity", { turnId: "turn-1", activity: { id: "cmd-1", kind: "command", status: "completed", summary: "go test", detail: { command: "go test", output: "ok" } } }),
			event(4, "chat.activity", { turnId: "turn-1", activity: { id: "edit-1", kind: "file_change", status: "completed", summary: "Edited files", detail: { files: [{ path: "main.go", status: "modified", additions: 0, deletions: 0 }] } } }),
			event(5, "chat.activity", { turnId: "turn-1", activity: { id: "turn-diff", kind: "turn_diff", status: "completed", summary: "Changed files", detail: { diff: "diff --git a/main.go b/main.go\n--- a/main.go\n+++ b/main.go\n@@ -1 +1 @@\n-old\n+new\n" } } }),
			event(6, "chat.assistant_delta", { turnId: "turn-1", text: "Done" }),
			event(7, "chat.turn_completed", { turnId: "turn-1" }),
		]);
		expect(snapshot.items).toContainEqual(expect.objectContaining({ activityKind: "command", status: "completed", detail: expect.objectContaining({ output: "ok" }) }));
		expect(snapshot.items).toContainEqual(expect.objectContaining({ role: "assistant", text: "Done" }));
		expect(snapshot.items).toContainEqual(expect.objectContaining({ activityKind: "file_change", detail: expect.objectContaining({ files: [expect.objectContaining({ path: "main.go", patch: expect.stringContaining("+new") })] }) }));
		expect(snapshot.turns[0].diff?.files).toContainEqual(expect.objectContaining({ path: "main.go", additions: 1, deletions: 1 }));
	});

	it("omits Codex's already-stored stdin status from the visible reply", () => {
		const events: CloudCpClientEvent[] = [
			{ sessionId: session.id, sequence: 1, type: "chat.user_message", payload: { text: "Hello", turnId: "turn-1" }, createdAt: session.updatedAt },
			{ sessionId: session.id, sequence: 2, type: "chat.assistant_delta", payload: { text: "Reading additional input from stdin...\n", turnId: "turn-1" }, createdAt: session.updatedAt },
			{ sessionId: session.id, sequence: 3, type: "chat.assistant_delta", payload: { text: "Hello!", turnId: "turn-1" }, createdAt: session.updatedAt },
		];
		const snapshot = toSnapshot(session, events);
		expect(snapshot.items.filter((item) => item.kind === "message" && item.role === "assistant"))
			.toEqual([expect.objectContaining({ text: "Hello!" })]);
	});

	it("shows the first Cloud send as active while the worker claims it", () => {
		const events: CloudCpClientEvent[] = [
			{ sessionId: session.id, sequence: 1, type: "chat.user_message", payload: { text: "Next", turnId: "turn-next" }, createdAt: session.updatedAt },
		];
		const snapshot = toSnapshot(session, events);
		expect(snapshot.turns).toEqual([expect.objectContaining({ id: "turn-next", state: "running" })]);
		expect(snapshot.controller.state).toBe("busy");
	});

	it("shows the session starting, not working, while its Setup runs ahead of the first turn", async () => {
		const setupStep = (sequence: number, status: string): CloudCpClientEvent => ({
			sessionId: session.id, sequence, type: "workspace.script.step", createdAt: "2026-10-20T16:02:01Z",
			payload: { phase: "setup", runId: "run-1", step: 1, total: 1, status, command: "./setup.sh", repository: "", startedAt: "2026-10-20T16:02:01Z" },
		});
		const prompt: CloudCpClientEvent = { sessionId: session.id, sequence: 1, type: "chat.user_message", payload: { text: "Run", turnId: "turn-1" }, createdAt: "2026-10-20T16:01:57Z" };
		// Setup running: the first prompt waits on it rather than being worked on.
		const during = toSnapshot(session, [prompt, setupStep(2, "running")]);
		expect(during.turns).toEqual([expect.objectContaining({ id: "turn-1", state: "queued" })]);
		expect(during.controller.state).toBe("ready");
		// Setup done: the turn is the active request again while the worker claims it.
		const after = toSnapshot(session, [prompt, setupStep(2, "running"), { ...setupStep(3, "succeeded"), createdAt: "2026-10-20T16:03:01Z" }]);
		expect(after.turns[0].state).toBe("running");
		// Its working time counts from Setup's end, not from the prompt.
		expect(after.turns[0].startedAt).toBe("2026-10-20T16:03:01Z");

		cloudMocks.listChatEvents.mockReset().mockResolvedValue({ events: [prompt, setupStep(2, "running")], hasMore: false, nextAfter: 2 });
		cloudMocks.listChatModels.mockReset().mockResolvedValue({ models: [] });
		cloudMocks.chatProps.mockClear();
		const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
		render(
			<QueryClientProvider client={queryClient}>
				<CloudSessionChatSurface session={{ ...session, cloud: { orgId: "org-1" } }} />
			</QueryClientProvider>,
		);
		await waitFor(() => expect(cloudMocks.chatProps.mock.lastCall?.[0].session.provisionState).toBe("provisioning"));
		const props = cloudMocks.chatProps.mock.lastCall?.[0];
		// The startup checklist in place of a working timer, and nothing to stop. It is
		// the checklist a failed start shows, with Setup running.
		expect(props.session.provisionSteps).toEqual([
			{ id: "worktree", status: "done" },
			{ id: "setup", status: "running", startedAt: expect.any(String) },
			{ id: "agent", status: "pending" },
		]);
		expect(props.onInterrupt).toBeUndefined();
		expect(props.onSteer).toBeUndefined();
	});

	it("queues only later Cloud sends while the first awaits its worker", () => {
		const events: CloudCpClientEvent[] = [
			{ sessionId: session.id, sequence: 1, type: "chat.user_message", payload: { text: "First", turnId: "turn-1" }, createdAt: session.updatedAt },
			{ sessionId: session.id, sequence: 2, type: "chat.user_message", payload: { text: "Second", turnId: "turn-2" }, createdAt: session.updatedAt },
		];
		const snapshot = toSnapshot(session, events);
		expect(snapshot.turns.map((turn) => turn.state)).toEqual(["running", "queued"]);
	});

	it("shows the checkout error when a queued turn fails", () => {
		const events: CloudCpClientEvent[] = [
			{ sessionId: session.id, sequence: 1, type: "chat.user_message", payload: { text: "Build", turnId: "turn-1" }, createdAt: session.updatedAt },
			{ sessionId: session.id, sequence: 2, type: "chat.turn_started", payload: { turnId: "turn-1" }, createdAt: session.updatedAt },
			{ sessionId: session.id, sequence: 3, type: "chat.turn_aborted", payload: { turnId: "turn-1", error: "Repository checkout failed. Reconnect GitHub." }, createdAt: session.updatedAt },
		];
		const snapshot = toSnapshot(session, events);
		expect(snapshot.turns).toEqual([expect.objectContaining({ state: "failed", errorMessage: "Repository checkout failed. Reconnect GitHub." })]);
		expect(snapshot.controller.state).toBe("ready");
	});

	it("keeps a second Cloud message queued behind an active turn", () => {
		const events: CloudCpClientEvent[] = [
			{ sessionId: session.id, sequence: 1, type: "chat.user_message", payload: { text: "First", turnId: "turn-1" }, createdAt: session.updatedAt },
			{ sessionId: session.id, sequence: 2, type: "chat.turn_started", payload: { turnId: "turn-1" }, createdAt: session.updatedAt },
			{ sessionId: session.id, sequence: 3, type: "chat.user_message", payload: { text: "Second", turnId: "turn-2" }, createdAt: session.updatedAt },
		];
		const snapshot = toSnapshot(session, events);
		expect(snapshot.turns.map((turn) => turn.state)).toEqual(["running", "queued"]);
	});

	it("projects a durable steer as activity on the active turn", () => {
		const events: CloudCpClientEvent[] = [
			{ sessionId: session.id, sequence: 1, type: "chat.user_message", payload: { text: "Build it", turnId: "turn-1" }, createdAt: session.updatedAt },
			{ sessionId: session.id, sequence: 2, type: "chat.turn_started", payload: { turnId: "turn-1" }, createdAt: session.updatedAt },
			{ sessionId: session.id, sequence: 3, type: "chat.turn_steered", payload: { turnId: "turn-1", text: "Prefer tests first", clientMessageId: "message-1" }, createdAt: session.updatedAt },
		];

		const snapshot = toSnapshot(session, events);

		expect(snapshot.items).toContainEqual(expect.objectContaining({
			kind: "activity",
			turnId: "turn-1",
			summary: "Steered: Prefer tests first",
			detail: { event: "steer", text: "Prefer tests first", origin: "human", clientMessageId: "message-1" },
		}));
	});

	it("shows the provider's exact pending approval and resolves it through Cloud", async () => {
		cloudMocks.listChatEvents.mockResolvedValue({ events: [
			{ sessionId: session.id, sequence: 1, type: "chat.user_message", payload: { text: "Edit", turnId: "turn-1" }, createdAt: session.updatedAt },
			{ sessionId: session.id, sequence: 2, type: "chat.turn_started", payload: { turnId: "turn-1" }, createdAt: session.updatedAt },
			{ sessionId: session.id, sequence: 3, type: "chat.approval_requested", payload: {
				turnId: "turn-1", requestId: "approval-1", summary: "Apply file changes",
				decisions: [{ id: "allow-once", label: "Allow once", kind: "allow_once" }, { id: "reject", label: "Reject", kind: "reject_once" }],
			}, createdAt: session.updatedAt },
		], hasMore: false, nextAfter: 3 });
		cloudMocks.decideChatApproval.mockResolvedValue({ ok: true });
		const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
		render(<QueryClientProvider client={queryClient}>
			<CloudSessionChatSurface session={{ ...session, provider: "cursor", cloud: { orgId: "org-1", permissionMode: "standard" } }} />
		</QueryClientProvider>);
		await waitFor(() => expect(cloudMocks.chatProps.mock.lastCall?.[0].snapshot.items).toContainEqual(expect.objectContaining({
			activityKind: "approval", requestId: "approval-1", status: "pending",
			decisions: [
				{ id: "allow-once", label: "Allow once", kind: "allow_once" },
				{ id: "reject", label: "Reject", kind: "reject_once" },
			],
		})));
		cloudMocks.chatProps.mock.lastCall?.[0].onDecide("approval-1", "allow-once");
		await waitFor(() => expect(cloudMocks.decideChatApproval).toHaveBeenCalledWith("org-1", session.id, "approval-1", "allow-once"));
	});

	it("closes an unresolved approval when its turn ends", () => {
		const events: CloudCpClientEvent[] = [
			{ sessionId: session.id, sequence: 1, type: "chat.turn_started", payload: { turnId: "turn-1" }, createdAt: session.updatedAt },
			{ sessionId: session.id, sequence: 2, type: "chat.approval_requested", payload: {
				turnId: "turn-1", requestId: "approval-1", summary: "Run command", decisions: [{ id: "accept", label: "Accept" }],
			}, createdAt: session.updatedAt },
			{ sessionId: session.id, sequence: 3, type: "chat.turn_aborted", payload: { turnId: "turn-1" }, createdAt: session.updatedAt },
		];
		expect(toSnapshot(session, events).items).toContainEqual(expect.objectContaining({
			requestId: "approval-1", status: "cancelled",
		}));
	});

	it("offers steering only after the active provider advertises it", () => {
		const base: CloudCpClientEvent[] = [
			{ sessionId: session.id, sequence: 1, type: "chat.user_message", payload: { text: "Build", turnId: "turn-1" }, createdAt: session.updatedAt },
			{ sessionId: session.id, sequence: 2, type: "chat.turn_started", payload: { turnId: "turn-1" }, createdAt: session.updatedAt },
		];
		expect(toSnapshot(session, base).capabilities).toEqual([]);
		expect(toSnapshot(session, [...base, {
			sessionId: session.id, sequence: 3, type: "chat.turn_capabilities", payload: { turnId: "turn-1", steering: true }, createdAt: session.updatedAt,
		}]).capabilities).toEqual(["steer"]);
	});
});

describe("cloudStartupChecklist", () => {
	it("shows no checklist while a new Freestyle session's worker connects: its composer shimmers instead", () => {
		const starting = { ...session, mode: "chat" as const, runtimeConnected: false, cloud: { orgId: "org-1", sandboxProvider: "freestyle", desiredState: "running", observedState: "bootstrapping" } };
		expect(cloudStartupChecklist(starting, [], undefined, false)).toBeUndefined();
	});
});

describe("CloudSessionChatSurface opening brief", () => {
	const cloudSession = { ...session, runtimeConnected: false, cloud: { orgId: "org-1", permissionMode: "trusted" } } as WorkspaceSession;
	const seeded = () => {
		const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
		queryClient.setQueryData(["cloud-opening", "org-1", "session-1"], { clientMessageId: "req-1", text: "Fix the bug", createdAt: "2026-11-02T00:00:00Z" });
		return queryClient;
	};
	const props = () => cloudMocks.chatProps.mock.lastCall?.[0];
	beforeEach(() => {
		localStorage.clear();
		cloudMocks.chatProps.mockClear();
		cloudMocks.listChatModels.mockReset().mockResolvedValue({ models: [] });
		cloudMocks.listUserAgentModels.mockReset().mockResolvedValue({ models: [] });
		cloudMocks.resumeSession.mockReset().mockResolvedValue({});
	});

	it("shows the brief this device started the session with from the first frame, before the chat read lands", () => {
		cloudMocks.listChatEvents.mockReset().mockReturnValue(new Promise(() => {}));
		render(<QueryClientProvider client={seeded()}><CloudSessionChatSurface session={cloudSession} /></QueryClientProvider>);
		expect(props().localEchos).toEqual([{ clientMessageId: "req-1", text: "Fix the bug", createdAt: "2026-11-02T00:00:00Z", opening: true }]);
		expect(props().openingStartedAt).toBe("2026-11-02T00:00:00Z");
	});

	it("drops the brief once the control plane's chat holds the first message, so it shows once", async () => {
		cloudMocks.listChatEvents.mockReset().mockResolvedValue({ events: [
			{ sequence: 1, type: "chat.user_message", sessionId: "session-1", createdAt: "2026-11-02T00:00:01Z", payload: { text: "Fix the bug" } },
		], hasMore: false, nextAfter: 1 });
		render(<QueryClientProvider client={seeded()}><CloudSessionChatSurface session={cloudSession} /></QueryClientProvider>);
		await waitFor(() => expect(props().snapshot.items.filter((item: { kind: string; role?: string }) => item.kind === "message" && item.role === "user")).toHaveLength(1));
		expect(props().localEchos).toBeUndefined();
		// The start is still timed from the send until the first turn settles.
		expect(props().openingStartedAt).toBe("2026-11-02T00:00:00Z");
	});

	it("forgets the brief once the first turn settled", async () => {
		const queryClient = seeded();
		cloudMocks.listChatEvents.mockReset().mockResolvedValue({ events: [
			{ sequence: 1, type: "chat.user_message", sessionId: "session-1", createdAt: "2026-11-02T00:00:01Z", payload: { text: "Fix the bug" } },
			{ sequence: 2, type: "chat.turn_started", sessionId: "session-1", createdAt: "2026-11-02T00:00:05Z", payload: { turnId: "turn-1" } },
			{ sequence: 3, type: "chat.turn_completed", sessionId: "session-1", createdAt: "2026-11-02T00:00:09Z", payload: { turnId: "turn-1" } },
		], hasMore: false, nextAfter: 3 });
		render(<QueryClientProvider client={queryClient}><CloudSessionChatSurface session={{ ...cloudSession, runtimeConnected: true }} /></QueryClientProvider>);
		await waitFor(() => expect(queryClient.getQueryData(["cloud-opening", "org-1", "session-1"])).toBeUndefined());
	});
});


describe("CloudSessionChatSurface palette jump", () => {
	const props = () => cloudMocks.chatProps.mock.lastCall?.[0];
	const sessionA = { ...session, id: "session-a", cloud: { orgId: "org-1" } } satisfies WorkspaceSession;
	const sessionB = { ...session, id: "session-b", cloud: { orgId: "org-1" } } satisfies WorkspaceSession;
	beforeEach(() => {
		useUiStore.setState({ chatMessageFocusRequest: null });
		cloudMocks.chatProps.mockReset();
		cloudMocks.listChatModels.mockResolvedValue({ models: [] });
		cloudMocks.listChatEvents.mockReset().mockImplementation(async (_orgId: string, sessionId: string) => ({ events: [
			{ sessionId, sequence: 1, type: "chat.user_message", payload: { text: `hello from ${sessionId}`, turnId: "turn-1" }, createdAt: session.updatedAt },
		], hasMore: false, nextAfter: 1 }));
	});
	function jumpToB() {
		const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
		const view = render(<QueryClientProvider client={queryClient}><CloudSessionChatSurface session={sessionB} /></QueryClientProvider>);
		act(() => useUiStore.getState().requestChatMessageFocus(sessionUiKey(sessionB.id), "cloud-user-turn-1", "hello"));
		return { ...view, queryClient };
	}

	it("never hands one session's palette target to the next session the surface shows", async () => {
		const { rerender, queryClient } = jumpToB();
		await waitFor(() => expect(props().annotationNavigationRequest).toMatchObject({ messageId: "cloud-user-turn-1", text: "hello" }));
		// The surface stays mounted from session to session; A must not get B's target.
		rerender(<QueryClientProvider client={queryClient}><CloudSessionChatSurface session={sessionA} /></QueryClientProvider>);
		expect(props().annotationNavigationRequest).toBeUndefined();
		rerender(<QueryClientProvider client={queryClient}><CloudSessionChatSurface session={sessionA} /></QueryClientProvider>);
		expect(props().annotationNavigationRequest).toBeUndefined();
	});

	it("drops a palette target once the chat applied it, so a later mount never replays it", async () => {
		const { rerender, queryClient } = jumpToB();
		await waitFor(() => expect(props().annotationNavigationRequest).toBeDefined());
		const { id } = props().annotationNavigationRequest;
		act(() => props().onAnnotationNavigationHandled?.(id));
		expect(props().annotationNavigationRequest).toBeUndefined();
		rerender(<QueryClientProvider client={queryClient}><CloudSessionChatSurface session={sessionA} /></QueryClientProvider>);
		rerender(<QueryClientProvider client={queryClient}><CloudSessionChatSurface session={sessionB} /></QueryClientProvider>);
		expect(props().annotationNavigationRequest).toBeUndefined();
	});
});

 it("delivers frozen excerpts independently of Cloud source history", () => {
	const ref = { id: "quote", conversationId: "cloud", messageId: "deleted", revision: 9, role: "assistant" as const, text: "selected", snapshot: { version: 1, rawText: "**selected**", captureMode: "exact" as const } };
	const text = cloudExcerptText([ref], { items: [] });
	expect(text).toContain("**selected**");
	expect(text).toContain("Selected text:\n---\nselected");
	expect(cloudAnnotationContent([ref])[0].snapshot).toEqual(ref.snapshot);
 });

describe("Cloud excerpt display snapshots", () => {
	it("omits repeated background turns from eight excerpts without changing the captures", () => {
		const excerpt = capturedCloudExcerpt();
		const original = structuredClone(excerpt);
		const refs = Array.from({ length: 8 }, (_, index) => ({ ...excerpt, id: `excerpt-${index}`, order: index }));
		const content = cloudAnnotationContent(refs);
		for (const [index, block] of content.entries()) {
			expect(block.annotationId).toBe(`excerpt-${index}`);
			expect(block.sourceRevision).toBe(0);
			expect(block.snapshot).not.toHaveProperty("turn");
			expect({ ...block.snapshot, turn: original.snapshot!.turn }).toEqual(original.snapshot);
		}
		expect(excerpt).toEqual(original);
		const bytes = (value: unknown) => new TextEncoder().encode(JSON.stringify(value)).length;
		const full = content.map(block => ({ ...block, snapshot: excerpt.snapshot }));
		const removedBytes = bytes(full) - bytes(content);
		// The only JSON removed per reference is the comma and the turn property.
		expect(removedBytes).toBe(8 * (bytes({ turn: excerpt.snapshot!.turn }) - 1));
		expect(removedBytes).toBeGreaterThan(48_000);
	});

	it.each(["legacy", "no-turn", "omitted", "diagram"])("preserves %s metadata and supports old transcript events", (kind) => {
		const excerpt = capturedCloudExcerpt();
		if (kind === "legacy") delete excerpt.snapshot;
		else if (kind === "no-turn") delete excerpt.snapshot!.turn;
		else if (kind === "omitted") {
			delete excerpt.snapshot!.turn;
			excerpt.snapshot!.contextOmitted = true;
		} else {
			excerpt.text = "";
			const rawText = "```mermaid\nflowchart LR; A-->B\n```";
			excerpt.snapshot = { ...excerpt.snapshot!, rawText, captureMode: "block", containsFlowchart: true, badgeOnly: true,
				sourceRange: { start: 0, end: rawText.length, offsetUnit: "utf-16" }, diagramRange: { start: 0, end: rawText.length, offsetUnit: "utf-16" } };
		}
		const original = structuredClone(excerpt);
		const display = cloudAnnotationContent([excerpt])[0];
		if (display.snapshot) expect(display.snapshot).not.toHaveProperty("turn");
		if (original.snapshot?.turn) expect({ ...display.snapshot, turn: original.snapshot.turn }).toEqual(original.snapshot);
		else expect(display.snapshot).toEqual(original.snapshot);
		const projected = toSnapshot(session, [{ sessionId: session.id, sequence: 1, type: "chat.user_message",
			payload: { text: "Explain", content: [{ ...display, snapshot: original.snapshot }] }, createdAt: session.updatedAt }]);
		expect(projected.items[0]).toMatchObject({ content: [{ text: excerpt.text, snapshot: original.snapshot }] });
		expect(excerpt).toEqual(original);
	});
});

describe("toSnapshot agent switches", () => {
	it("draws chat.agent_switched as the Switched line a local switch draws", () => {
		const events = [
			{ sessionId: session.id, sequence: 1, type: "chat.user_message", payload: { text: "hi", turnId: "turn-1" }, createdAt: session.updatedAt },
			{ sessionId: session.id, sequence: 2, type: "chat.turn_started", payload: { turnId: "turn-1" }, createdAt: session.updatedAt },
			{ sessionId: session.id, sequence: 3, type: "chat.turn_completed", payload: { turnId: "turn-1" }, createdAt: session.updatedAt },
			{ sessionId: session.id, sequence: 4, type: "chat.agent_switched", createdAt: session.updatedAt,
				payload: { transitionId: "t-1", sourceHarness: "codex", targetHarness: "claude-code", sourceModel: "gpt-5", targetModel: "claude-opus-4-5", targetReasoningEffort: "high", resumed: false } },
		] as CloudCpClientEvent[];
		const marker = toSnapshot(session, events).items.find((item) => item.kind === "activity" && item.detail?.event === "provider_switch");
		expect(marker).toMatchObject({
			sequence: 4,
			detail: { event: "provider_switch", sourceHarness: "codex", targetHarness: "claude-code", sourceModel: "gpt-5", targetModel: "claude-opus-4-5", targetEffort: "high" },
		});
	});

	it("draws the Switched line above the message the switch held for the new agent", () => {
		const events = [
			{ sessionId: session.id, sequence: 1, type: "chat.user_message", payload: { text: "first", turnId: "turn-1" }, createdAt: session.updatedAt },
			{ sessionId: session.id, sequence: 2, type: "chat.turn_started", payload: { turnId: "turn-1" }, createdAt: session.updatedAt },
			{ sessionId: session.id, sequence: 3, type: "chat.turn_completed", payload: { turnId: "turn-1" }, createdAt: session.updatedAt },
			// Sent while the switch ran: recorded before the switch commits, answered after.
			{ sessionId: session.id, sequence: 4, type: "chat.user_message", payload: { text: "held", turnId: "turn-2" }, createdAt: session.updatedAt },
			{ sessionId: session.id, sequence: 5, type: "chat.agent_switched", createdAt: session.updatedAt,
				payload: { transitionId: "t-1", sourceHarness: "claude-code", targetHarness: "codex", resumed: false } },
			{ sessionId: session.id, sequence: 6, type: "chat.turn_started", payload: { turnId: "turn-2" }, createdAt: session.updatedAt },
			{ sessionId: session.id, sequence: 7, type: "chat.assistant_delta", payload: { turnId: "turn-2", text: "answer" }, createdAt: session.updatedAt },
			{ sessionId: session.id, sequence: 8, type: "chat.turn_completed", payload: { turnId: "turn-2" }, createdAt: session.updatedAt },
		] as CloudCpClientEvent[];
		const items = toSnapshot(session, events).items;
		const marker = items.findIndex((item) => item.kind === "activity" && item.detail?.event === "provider_switch");
		const held = items.findIndex((item) => item.kind === "message" && item.role === "user" && item.text === "held");
		const first = items.findIndex((item) => item.kind === "message" && item.role === "user" && item.text === "first");
		expect(first).toBeLessThan(marker);
		expect(marker).toBeLessThan(held);
		expect(items[marker].sequence).toBeLessThan(items[held].sequence);
	});
});
