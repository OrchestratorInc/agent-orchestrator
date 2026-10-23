import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import type { ReactNode } from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { ChatConfigOption, ChatModel, ConversationMessage, ConversationSnapshot } from "../../types/conversation";
import type { WorkspaceSession } from "../../types/workspace";
import { useUiStore } from "../../stores/ui-store";
import { workspaceQueryKey } from "../../hooks/useWorkspaceQuery";
import type { AgentSwitchProps } from "./AgentSwitchPicker";
import { useConversationConfigOptions, useConversationModels, useConversationSkills } from "../../hooks/useConversation";

const LINK = "http://localhost:5173";
const REPORT_LINK = "reports/new-report.html";

function snapshotFor(sessionId: string): ConversationSnapshot & { capabilities: string[] } {
	return {
		activeBranchId: "branch-root",
		branchPoints: [],
		capabilities: [],
		conversationId: `conv-${sessionId}`,
		sessionId,
		harness: "codex",
		mode: "chat",
		controller: { state: "ready" },
		items: [],
		turns: [],
		settings: {},
		mcpServers: [],
		oldestSequence: 0,
		latestSequence: 0,
		hasMoreBefore: false,
	};
}

const {
	catalogObserverState,
	clearCatalogsMock,
	getMock,
	invalidateCatalogsMock,
	postMock,
	providerSwitchMock,
	workspacePathsState,
	conversationState,
	conversationCommandState,
} = vi.hoisted(() => ({
	catalogObserverState: { enabled: [] as boolean[] },
	clearCatalogsMock: vi.fn(),
	getMock: vi.fn(),
	invalidateCatalogsMock: vi.fn(),
	postMock: vi.fn(),
	providerSwitchMock: vi.fn(),
	workspacePathsState: { paths: [] as string[] },
	conversationCommandState: {
		busy: false,
		send: vi.fn(),
		chooseSettings: vi.fn(),
		pendingAcceptedTurnId: undefined as string | undefined,
		acknowledgeAcceptedTurn: vi.fn(),
		echoBeforeSend: vi.fn(),
		dropEcho: vi.fn(),
	},
	conversationState: {
		snapshot: { capabilities: [] } as
			| (Partial<ConversationSnapshot> & { capabilities: string[] })
			| undefined,
		isLoading: false,
		unavailable: undefined as { message: string } | undefined,
		error: undefined as string | undefined,
		hasOlder: false,
		isLoadingOlder: false,
		loadOlder: vi.fn(),
	},
}));

const switchFailure = vi.hoisted(() => ({
	ProviderSwitchFailed: class ProviderSwitchFailed extends Error {
		constructor(readonly reason: unknown) {
			super("switch failed");
		}
	},
}));

const configState = vi.hoisted(() => ({
	options: [] as ChatConfigOption[], loaded: false, error: undefined as string | undefined,
}));

const sideMocks = vi.hoisted(() => ({
	create: vi.fn(),
	send: vi.fn(),
	show: vi.fn(),
	addReference: vi.fn(),
	sides: [] as { hasWork: boolean }[],
	agentSwitch: undefined as AgentSwitchProps | undefined,
	workspaceSend: undefined as
		| undefined
		| ((text: string, attachments: undefined, clientMessageId: string) => Promise<void>),
}));
vi.mock("./IndependentSideChats", () => ({
	useIndependentSideChats: () => ({ ...sideMocks, panel: null, activeId: "existing-side" }),
}));

vi.mock("../../lib/api-client", () => ({
	apiClient: { GET: getMock, POST: postMock },
	getApiBaseUrl: () => "",
	hasTrustedApiBaseUrl: () => true,
	subscribeApiBaseUrl: () => () => {},
	apiErrorMessage: (_error: unknown, fallback: string) => fallback,
}));

// Every agent's permission modes are known, as once the daemon has answered, unless a test says not.
const permissionModesState = vi.hoisted(() => ({ known: true }));
vi.mock("../../hooks/useChatPermissionModes", () => ({
	useChatPermissionModes: (_hostId: string | undefined, harness: string | undefined) =>
		harness && permissionModesState.known ? new Map([[harness, ["default", "accept-edits", "bypass-permissions"]]]) : undefined,
}));

vi.mock("../../hooks/useConversation", () => ({
	ProviderSwitchFailed: switchFailure.ProviderSwitchFailed,
	clearConversationProviderCatalogs: clearCatalogsMock,
	conversationQueryKey: (sessionId: string) => ["conversation", sessionId],
	invalidateConversationProviderCatalogs: invalidateCatalogsMock,
	useConversation: (sessionId: string) => ({
		...conversationState,
		snapshot: conversationState.snapshot
			? { ...snapshotFor(sessionId), ...conversationState.snapshot }
			: undefined,
	}),
	useConversationCommands: () => conversationCommandState,
	useProviderSwitchCommand: () => ({
		mutateAsync: providerSwitchMock,
		isPending: false,
		error: null,
		reset: vi.fn(),
	}),
	useConversationConfigOptions: vi.fn((_sessionId: string, enabled: boolean) => {
		catalogObserverState.enabled.push(enabled);
		return configState;
	}),
	useConversationModels: vi.fn(() => ({ models: [], loaded: true })),
	useConversationSkills: vi.fn(() => ({ skills: [] })),
	useStageAttachments: () => undefined,
	useWorkspaceFilePaths: () => ({ paths: workspacePathsState.paths, truncated: false }),
}));

vi.mock("./ChatWorkspace", async () => {
	const { useState } = await vi.importActual<typeof import("react")>("react");
	const { TurnSettingsBar } = await vi.importActual<typeof import("./TurnSettingsBar")>("./TurnSettingsBar");
	return {
		CONTINUE_STOPPED_TURN_PROMPT: "Continue from where you stopped.",
		continuableTurnId: (snapshot: ConversationSnapshot) => snapshot.continueTurnId ?? "",
		ChatWorkspace: ({
			agentSwitch,
			agentInputDisabled,
			headerActions,
			sessionTabAction,
			newWorkDisabled,
			onLinkOpen,
			onRememberPermissions,
			onChooseSettings,
			onContinueTurn,
			configOptionError,
			configOptions,
			models,
			promptSuggestion,
			snapshot,
			shellTarget,
			onSend,
			composerNotice,
		}: {
			onSend: typeof sideMocks.workspaceSend;
			composerNotice?: ReactNode;
			agentSwitch?: AgentSwitchProps;
			agentInputDisabled?: boolean;
			headerActions?: ReactNode;
			sessionTabAction?: ReactNode;
			newWorkDisabled?: boolean;
			onLinkOpen?: (url: string) => void;
			onRememberPermissions?: unknown;
			onChooseSettings?: unknown;
			onContinueTurn?: () => Promise<unknown> | void;
			configOptionError?: string;
			configOptions?: ChatConfigOption[];
			models: ChatModel[];
			promptSuggestion?: { id: string; text: string };
			snapshot: Pick<ConversationSnapshot, "sessionId" | "harness" | "settings">;
			shellTarget?: { handleId: string };
		}) => {
			sideMocks.workspaceSend = onSend;
			sideMocks.agentSwitch = agentSwitch;
			const [mountedSessionId] = useState(snapshot.sessionId);
			return (
				<div>
					<div
						data-testid="chat-agent-input"
						data-disabled={agentInputDisabled ? "true" : "false"}
					/>
					<div
						data-testid="chat-new-work"
						data-disabled={newWorkDisabled ? "true" : "false"}
					/>
					{snapshot.sessionId ? <div>Mounted {mountedSessionId}</div> : null}
					{snapshot.sessionId ? <div>Rendered {snapshot.sessionId}</div> : null}
					{composerNotice}
					<div data-testid="remember-available">{String(Boolean(onRememberPermissions))}</div>
					<div data-testid="turn-settings-available">{String(Boolean(onChooseSettings))}</div>
					<div data-testid="config-option-error">{configOptionError}</div>
					{configOptions?.map((option) => (
						<div key={option.id} data-testid={`config-option-${option.id}`}>
							{option.choices.map((choice) => choice.value).join(",")}
						</div>
					))}
					{snapshot.harness === "claude-code" && <TurnSettingsBar models={models} settings={snapshot.settings} harness={snapshot.harness} configOptions={configOptions} onChangeConfigOption={() => {}} />}
					<div data-testid="prompt-suggestion">{promptSuggestion?.text}</div>
					<button type="button" onClick={() => { void Promise.resolve(onContinueTurn?.()).catch(() => {}); }}>Continue test turn</button>
					{headerActions}
					{sessionTabAction}
					<button type="button" onClick={() => onLinkOpen?.(LINK)}>
						Open chat link
					</button>
					<button type="button" onClick={() => onLinkOpen?.(REPORT_LINK)}>
						Open report link
					</button>
					{shellTarget ? <div data-testid="shell-target">{shellTarget.handleId}</div> : null}
				</div>
			);
		},
	};
});

import { SessionChatSurface } from "./SessionChatSurface";

const session = {
	id: "sess-1",
	workspaceId: "proj-1",
	workspaceName: "my-app",
	title: "chat worker",
	provider: "codex",
	kind: "worker",
	mode: "chat",
	status: "working",
	updatedAt: "2026-08-08T00:00:00Z",
	prs: [],
} satisfies WorkspaceSession;

function Wrapper({ client, children }: { client: QueryClient; children: ReactNode }) {
	return <QueryClientProvider client={client}>{children}</QueryClientProvider>;
}

beforeEach(() => {
	workspacePathsState.paths = [];
	configState.options = [];
	configState.loaded = false;
	configState.error = undefined;
	permissionModesState.known = true;
	getMock.mockReset().mockImplementation(async () => ({
		data: {},
		error: undefined,
		response: { status: 200 },
	}));
	postMock.mockReset().mockResolvedValue({ data: {}, error: undefined });
	providerSwitchMock.mockReset().mockResolvedValue({});
	sideMocks.agentSwitch = undefined;
	clearCatalogsMock.mockReset();
	invalidateCatalogsMock.mockReset();
	conversationState.snapshot = { capabilities: [] };
	conversationState.isLoading = false;
	conversationState.unavailable = undefined;
	conversationState.error = undefined;
	conversationState.hasOlder = false;
	conversationState.isLoadingOlder = false;
	conversationState.loadOlder = vi.fn();
	conversationCommandState.send = vi.fn().mockResolvedValue({});
	conversationCommandState.busy = false;
	conversationCommandState.pendingAcceptedTurnId = undefined;
	conversationCommandState.acknowledgeAcceptedTurn.mockReset();
	conversationCommandState.echoBeforeSend.mockReset();
	conversationCommandState.dropEcho.mockReset();
	catalogObserverState.enabled = [];
	useUiStore.setState({ inspectorSessions: {}, developerMode: true });
});

afterEach(() => {
	vi.useRealTimers();
});

describe("SessionChatSurface pending switch effort", () => {
	it.each([
		{ harness: "codex", effort: undefined, shownEffort: "low", expected: "low" },
		{ harness: "codex", effort: "high", shownEffort: "low", expected: "high" },
		{ harness: "cursor", effort: undefined, shownEffort: "high", expected: undefined },
	])("sends $harness effort $expected from the pending picker", async ({ harness, effort, shownEffort, expected }) => {
		conversationState.snapshot = { ...snapshotFor(session.id), harness: "claude-code" };
		render(<Wrapper client={new QueryClient()}><SessionChatSurface session={session} /></Wrapper>);
		await act(async () => {
			sideMocks.agentSwitch?.onPendingChange({ harness, model: "chosen-model", effort, shownEffort });
		});
		await act(async () => {
			await sideMocks.workspaceSend?.("Recall the code word.", undefined, "switch-message");
		});
		// The switch and the message go out in one request.
		expect(conversationCommandState.send).toHaveBeenCalledWith(expect.objectContaining({
			text: "Recall the code word.",
			clientMessageId: "switch-message",
			providerSwitch: expect.objectContaining({ targetHarness: harness, effort: expected }),
		}));
		expect(providerSwitchMock).not.toHaveBeenCalled();
	});

	it("drops a picked agent when another tab of the session comes to the front", async () => {
		conversationState.snapshot = { ...snapshotFor(session.id), harness: "claude-code" };
		const client = new QueryClient();
		const view = render(<Wrapper client={client}><SessionChatSurface session={session} /></Wrapper>);
		await act(async () => {
			sideMocks.agentSwitch?.onPendingChange({ harness: "codex" });
		});
		expect(sideMocks.agentSwitch?.pending).toEqual({ harness: "codex" });
		view.rerender(<Wrapper client={client}><SessionChatSurface session={session} shellTarget={{ kind: "shell", handleId: "shell-1", generation: "generation-1", title: "Shell" }} /></Wrapper>);
		view.rerender(<Wrapper client={client}><SessionChatSurface session={session} /></Wrapper>);
		expect(sideMocks.agentSwitch?.pending).toBeUndefined();
	});

	it("lets a pick lapse when Developer Mode is turned off", async () => {
		conversationState.snapshot = { ...snapshotFor(session.id), harness: "claude-code" };
		render(<Wrapper client={new QueryClient()}><SessionChatSurface session={session} /></Wrapper>);
		await act(async () => {
			sideMocks.agentSwitch?.onPendingChange({ harness: "codex" });
		});
		expect(sideMocks.agentSwitch?.pending).toEqual({ harness: "codex" });
		await act(async () => {
			useUiStore.setState({ developerMode: false });
		});
		expect(sideMocks.agentSwitch?.pending).toBeUndefined();
	});

	it("sends nothing with a switch until the agent's permission modes are known", async () => {
		permissionModesState.known = false;
		conversationState.snapshot = { ...snapshotFor(session.id), harness: "codex" };
		conversationCommandState.send = vi.fn().mockResolvedValue({});
		render(<Wrapper client={new QueryClient()}><SessionChatSurface session={session} /></Wrapper>);
		await act(async () => {
			sideMocks.agentSwitch?.onPendingChange({ harness: "pi" });
		});
		let failure: unknown;
		await act(async () => {
			await sideMocks.workspaceSend?.("Only edit after approval", undefined, "permission-race").catch((error) => { failure = error; });
		});
		// The bar may still show the chat's mode, which Pi does not run in.
		expect(conversationCommandState.send).not.toHaveBeenCalled();
		expect(failure).toMatchObject({ code: "AGENT_SWITCH_FAILED" });
		expect(sideMocks.agentSwitch?.pending).toEqual({ harness: "pi" });
	});

	it("reports a refused switch as a switch failure, so the draft stays editable", async () => {
		conversationState.snapshot = { ...snapshotFor(session.id), harness: "claude-code" };
		conversationCommandState.send = vi.fn().mockRejectedValue(new switchFailure.ProviderSwitchFailed({ code: "PROVIDER_SWITCH_busy" }));
		render(<Wrapper client={new QueryClient()}><SessionChatSurface session={session} /></Wrapper>);
		await act(async () => {
			sideMocks.agentSwitch?.onPendingChange({ harness: "codex", model: "chosen-model" });
		});
		let failure: unknown;
		await act(async () => {
			await sideMocks.workspaceSend?.("Recall the code word.", undefined, "switch-message").catch((error) => { failure = error; });
		});
		expect(failure).toMatchObject({ code: "AGENT_SWITCH_FAILED" });
		expect(await screen.findByText("Couldn't switch agent")).toBeInTheDocument();
		// The previous agent keeps the chat, so the composer shows it again.
		expect(sideMocks.agentSwitch?.pending).toBeUndefined();
	});

	it("drops the outgoing agent's cached models when the chat's agent changes elsewhere", () => {
		conversationState.snapshot = { ...snapshotFor(session.id), harness: "claude-code" };
		const client = new QueryClient();
		const view = render(<Wrapper client={client}><SessionChatSurface session={session} /></Wrapper>);
		expect(clearCatalogsMock).not.toHaveBeenCalled();
		// Switched from another window: this one only sees the new agent.
		conversationState.snapshot = { ...snapshotFor(session.id), harness: "codex" };
		view.rerender(<Wrapper client={client}><SessionChatSurface session={{ ...session }} /></Wrapper>);
		expect(clearCatalogsMock).toHaveBeenCalledWith(client, session.id, undefined);
	});

	it("keeps the pick when a switch got no clear answer, until the chat shows its agent", async () => {
		conversationState.snapshot = { ...snapshotFor(session.id), harness: "claude-code" };
		conversationCommandState.send = vi.fn().mockRejectedValue(new TypeError("Failed to fetch"));
		const client = new QueryClient();
		const view = render(<Wrapper client={client}><SessionChatSurface session={session} /></Wrapper>);
		await act(async () => {
			sideMocks.agentSwitch?.onPendingChange({ harness: "codex" });
		});
		await act(async () => {
			await sideMocks.workspaceSend?.("Recall the code word.", undefined, "switch-message").catch(() => undefined);
		});
		// The daemon may not have switched, so a retry still asks for Codex.
		expect(sideMocks.agentSwitch?.pending).toEqual({ harness: "codex" });
		// It had: the chat now runs Codex, and the pick is done.
		// A new session object re-renders the memoized surface with the new snapshot.
		conversationState.snapshot = { ...snapshotFor(session.id), harness: "codex" };
		view.rerender(<Wrapper client={client}><SessionChatSurface session={{ ...session }} /></Wrapper>);
		conversationState.snapshot = { ...snapshotFor(session.id), harness: "claude-code" };
		view.rerender(<Wrapper client={client}><SessionChatSurface session={{ ...session }} /></Wrapper>);
		expect(sideMocks.agentSwitch?.pending).toBeUndefined();
	});

	it("sends a message with attachments and its switch in one request", async () => {
		conversationState.snapshot = { ...snapshotFor(session.id), harness: "claude-code" };
		render(<Wrapper client={new QueryClient()}><SessionChatSurface session={session} /></Wrapper>);
		await act(async () => {
			sideMocks.agentSwitch?.onPendingChange({ harness: "codex", model: "chosen-model" });
		});
		const attachments = [{ mimeType: "image/png", data: "AA==" }];
		await act(async () => {
			await (sideMocks.workspaceSend as unknown as (text: string, attachments: unknown[], id: string) => Promise<void>)?.(
				"Inspect this image.", attachments, "switch-message");
		});
		// No other switch can come between them and take the image.
		expect(conversationCommandState.send).toHaveBeenCalledWith(expect.objectContaining({
			text: "Inspect this image.",
			attachments,
			clientMessageId: "switch-message",
			providerSwitch: expect.objectContaining({ targetHarness: "codex" }),
		}));
		expect(providerSwitchMock).not.toHaveBeenCalled();
		expect(conversationCommandState.echoBeforeSend).not.toHaveBeenCalled();
	});
});

describe("SessionChatSurface prompt suggestions", () => {
	function settleConversation() {
		conversationState.snapshot = {
			...snapshotFor(session.id),
			turns: [{ id: "turn-1", state: "completed", requestedAt: "2026-10-12T00:00:00Z" }],
		};
		postMock.mockResolvedValue({ data: { turnId: "turn-1", prompt: "Explain the fix." } });
	}

	it("forwards a settled conversation suggestion and hides it while new work is fenced", async () => {
		settleConversation();
		const client = new QueryClient();
		const tree = (newWorkDisabled = false) =>
			<Wrapper client={client}><SessionChatSurface session={session} newWorkDisabled={newWorkDisabled} /></Wrapper>;
		const view = render(tree());
		await waitFor(() => expect(screen.getByTestId("prompt-suggestion")).toHaveTextContent("Explain the fix."));
		view.rerender(tree(true));
		expect(screen.getByTestId("prompt-suggestion")).toBeEmptyDOMElement();
		view.rerender(tree());
		expect(screen.getByTestId("prompt-suggestion")).toHaveTextContent("Explain the fix.");
		expect(postMock).toHaveBeenCalledOnce();
	});

	it("keeps an in-flight suggestion while a file pane temporarily hides the composer", async () => {
		settleConversation();
		let signal: AbortSignal | undefined;
		let resolve!: (value: unknown) => void;
		postMock.mockImplementation((_path, options: { signal: AbortSignal }) => {
			signal = options.signal;
			return new Promise((complete) => { resolve = complete; });
		});
		const client = new QueryClient();
		const tree = (workspaceFileActive = false) => <Wrapper client={client}><SessionChatSurface session={session} workspaceFileActive={workspaceFileActive} /></Wrapper>;
		const view = render(tree());
		await waitFor(() => expect(postMock).toHaveBeenCalledOnce());
		view.rerender(tree(true));
		expect(signal?.aborted).toBe(false);
		expect(screen.getByTestId("prompt-suggestion")).toBeEmptyDOMElement();
		await act(async () => resolve({ data: { turnId: "turn-1", prompt: "Explain the fix." } }));
		view.rerender(tree());
		await waitFor(() => expect(screen.getByTestId("prompt-suggestion")).toHaveTextContent("Explain the fix."));
		expect(postMock).toHaveBeenCalledOnce();
	});

	it.each([
		{ workspaceFileActive: true },
		{ workspaceActiveTabKey: "file:README.md" },
		{ reviewerChatSelected: true },
		{ reviewerTarget: { kind: "reviewer" as const, handleId: "review-1", harness: "codex", sessionId: session.id } },
		{ shellTarget: { kind: "shell" as const, handleId: "shell-1", generation: "generation-1", title: "Shell" } },
		{ controllerTransitioning: true },
		{ agentResuming: true },
	])("does not generate while the primary chat is inactive or blocked %#", (props) => {
		settleConversation();
		render(<Wrapper client={new QueryClient()}><SessionChatSurface session={session} {...props} /></Wrapper>);
		expect(postMock).not.toHaveBeenCalled();
		expect(screen.getByTestId("prompt-suggestion")).toBeEmptyDOMElement();
	});
});

describe("SessionChatSurface link routing", () => {
	it("keeps orchestrator chat mounted until its conversation and provisioning are ready", () => {
		const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
		const orchestrator = { ...session, kind: "orchestrator" as const, provisionState: "provisioning" as const };
		useUiStore.getState().setProjectProvisioning(session.workspaceId, true);
		conversationState.snapshot = undefined;
		conversationState.isLoading = true;
		const tree = (provisionState: "provisioning" | "ready" = "provisioning") =>
			<Wrapper client={queryClient}><SessionChatSurface session={{ ...orchestrator, provisionState }} /></Wrapper>;
		const view = render(tree());
		const mounted = screen.getByText(`Mounted ${session.id}`);
		expect(screen.queryByText("Loading conversation…")).not.toBeInTheDocument();
		view.rerender(tree("ready"));
		expect(useUiStore.getState().provisioningProjectIds.has(session.workspaceId)).toBe(true);
		conversationState.snapshot = snapshotFor(session.id);
		conversationState.isLoading = false;
		view.rerender(tree());
		expect(useUiStore.getState().provisioningProjectIds.has(session.workspaceId)).toBe(true);
		view.rerender(tree("ready"));
		expect(screen.getByText(`Mounted ${session.id}`)).toBe(mounted);
		expect(useUiStore.getState().provisioningProjectIds.has(session.workspaceId)).toBe(false);
	});

	it("releases project loading to show a failed orchestrator's retry UI", () => {
		const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
		useUiStore.getState().setProjectProvisioning(session.workspaceId, true);
		conversationState.snapshot = snapshotFor(session.id);
		render(<Wrapper client={queryClient}><SessionChatSurface session={{ ...session, kind: "orchestrator", provisionState: "failed", provisionError: "branch already checked out" }} /></Wrapper>);
		expect(useUiStore.getState().provisioningProjectIds.has(session.workspaceId)).toBe(false);
	});

	it("keeps OpenCode approvals writable when its provider supplies Build/Plan mode", () => {
		conversationState.snapshot = { capabilities: ["config_options"], harness: "opencode" };
		configState.options = [{
			id: "mode",
			name: "Mode",
			category: "mode",
			type: "select",
			currentValue: "build",
			choices: [{ value: "build", name: "Build" }, { value: "plan", name: "Plan" }],
		}];
		const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });

		render(<Wrapper client={queryClient}><SessionChatSurface session={{ ...session, provider: "opencode" }} /></Wrapper>);

		expect(screen.getByTestId("turn-settings-available")).toHaveTextContent("true");
	});

	it("leaves switched-off models out of a provider's model option, keeping its current value", async () => {
		conversationState.snapshot = { capabilities: ["config_options"], harness: "opencode" };
		configState.loaded = true;
		configState.options = [
			{
				id: "model", name: "Model", category: "model", type: "select", currentValue: "model-c",
				choices: [{ value: "model-a", name: "A" }, { value: "model-b", name: "B" }, { value: "model-c", name: "C" }],
			},
			{
				id: "mode", name: "Mode", category: "mode", type: "select", currentValue: "build",
				choices: [{ value: "build", name: "Build" }, { value: "plan", name: "Plan" }],
			},
		];
		getMock.mockImplementation(async (path: string) => path === "/api/v1/settings"
			? { data: { defaultSessionMode: "chat", chatHarnesses: [], harnessDefaults: { opencode: { disabledModels: ["model-b", "model-c"] } } }, error: undefined, response: { status: 200 } }
			: { data: {}, error: undefined, response: { status: 200 } });
		const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });

		render(<Wrapper client={queryClient}><SessionChatSurface session={{ ...session, provider: "opencode" }} /></Wrapper>);

		await waitFor(() => expect(screen.getByTestId("config-option-model")).toHaveTextContent("model-a,model-c"));
		expect(screen.getByTestId("config-option-mode")).toHaveTextContent("build,plan");
	});

	it("matches Claude's family aliases to the catalog models switched off in Settings", async () => {
		conversationState.snapshot = { capabilities: ["config_options"], harness: "claude-code" };
		configState.loaded = true;
		configState.options = [{
			id: "model", name: "Model", category: "model", type: "select", currentValue: "default",
			choices: [{ value: "default", name: "Default" }, { value: "opus", name: "Opus" }, { value: "sonnet", name: "Sonnet" }, { value: "haiku", name: "Haiku" }],
		}];
		getMock.mockImplementation(async (path: string) => path === "/api/v1/settings"
			? { data: { defaultSessionMode: "chat", chatHarnesses: [], harnessDefaults: { "claude-code": { disabledModels: ["claude-opus-5-5"] } } }, error: undefined, response: { status: 200 } }
			: path === "/api/v1/agents/{agent}/models"
				? { data: { agentId: "claude-code", models: [
					{ id: "claude-opus-5-5", label: "Claude Opus 5.5" },
					{ id: "claude-sonnet-5-5", label: "Claude Sonnet 5.5" },
					{ id: "claude-haiku-5-5", label: "Claude Haiku 5.5" },
				] }, error: undefined, response: { status: 200 } }
				: { data: {}, error: undefined, response: { status: 200 } });
		const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });

		render(<Wrapper client={queryClient}><SessionChatSurface session={{ ...session, provider: "claude-code" }} /></Wrapper>);

		await waitFor(() => expect(screen.getByTestId("config-option-model")).toHaveTextContent("default,sonnet,haiku"));
	});

	it.each(["opus", undefined])("keeps the current Claude alias label when its concrete model is disabled (snapshot model %s)", async (snapshotModel) => {
		conversationState.snapshot = { capabilities: ["config_options"], harness: "claude-code", settings: { model: snapshotModel } };
		configState.loaded = true;
		configState.options = [{
			id: "model", name: "Model", category: "model", type: "select", currentValue: "opus",
			choices: [{ value: "opus", name: "Opus" }, { value: "sonnet", name: "Sonnet" }],
		}];
		getMock.mockImplementation(async (path: string) => path === "/api/v1/settings"
			? { data: { defaultSessionMode: "chat", chatHarnesses: [], harnessDefaults: { "claude-code": { disabledModels: ["claude-opus-5-5"] } } }, error: undefined, response: { status: 200 } }
			: path === "/api/v1/agents/{agent}/models"
				? { data: { agentId: "claude-code", models: [
					{ id: "claude-opus-5-5", label: "Claude Opus 5.5" },
					{ id: "claude-opus-4-9", label: "Claude Opus 4.9" },
					{ id: "claude-sonnet-5-5", label: "Claude Sonnet 5.5" },
				] }, error: undefined, response: { status: 200 } }
				: { data: {}, error: undefined, response: { status: 200 } });
		const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });

		render(<Wrapper client={queryClient}><SessionChatSurface session={{ ...session, provider: "claude-code" }} /></Wrapper>);

		await waitFor(() => expect(queryClient.isFetching()).toBe(0));
		await waitFor(() => expect(screen.getByRole("button", { name: "Model" })).toHaveTextContent("Opus 5.5"));
		expect(screen.getByTestId("config-option-model")).toHaveTextContent("opus,sonnet");
	});

	it("does not report idle work before the conversation snapshot loads", () => {
		conversationState.snapshot = undefined;
		conversationState.isLoading = true;
		const onConversationWorkChange = vi.fn();
		const queryClient = new QueryClient({
			defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
		});

		render(
			<Wrapper client={queryClient}>
				<SessionChatSurface
					session={session}
					onConversationWorkChange={onConversationWorkChange}
				/>
			</Wrapper>,
		);

		expect(onConversationWorkChange).not.toHaveBeenCalled();
	});

	it("does not attribute a previous session snapshot's work to the destination", () => {
		conversationState.snapshot = {
			...snapshotFor("sess-previous"),
			controller: { state: "busy" },
			turns: [
				{
					id: "turn-previous",
					state: "running",
					requestedAt: "2026-08-25T09:00:00Z",
				},
			],
		};
		const onConversationWorkChange = vi.fn();
		const queryClient = new QueryClient({
			defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
		});

		render(
			<Wrapper client={queryClient}>
				<SessionChatSurface
					session={session}
					onConversationWorkChange={onConversationWorkChange}
				/>
			</Wrapper>,
		);

		expect(onConversationWorkChange).not.toHaveBeenCalled();
	});

	it("reports live and queued Chat work to the interface-switch owner", async () => {
		conversationState.snapshot = {
			capabilities: [],
			controller: { state: "busy" },
			turns: [
				{ id: "turn-running", state: "running", requestedAt: "2026-08-25T09:00:00Z" },
				{ id: "turn-queued", state: "queued", requestedAt: "2026-08-25T09:00:01Z" },
			],
		};
		const onConversationWorkChange = vi.fn();
		const queryClient = new QueryClient({
			defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
		});

		render(
			<Wrapper client={queryClient}>
				<SessionChatSurface
					session={session}
					onConversationWorkChange={onConversationWorkChange}
				/>
			</Wrapper>,
		);

		await waitFor(() => {
			expect(onConversationWorkChange).toHaveBeenLastCalledWith({
				controllerBusy: true,
				hasRunningTurn: true,
				queuedTurnCount: 1,
			});
		});
	});

	it("reports pending local work while the cached conversation snapshot is idle", async () => {
		conversationState.snapshot = {
			capabilities: [],
			controller: { state: "ready" },
			turns: [],
		};
		conversationCommandState.busy = true;
		const onConversationWorkChange = vi.fn();
		const queryClient = new QueryClient({
			defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
		});

		render(
			<Wrapper client={queryClient}>
				<SessionChatSurface
					session={session}
					onConversationWorkChange={onConversationWorkChange}
				/>
			</Wrapper>,
		);

		await waitFor(() => {
			expect(onConversationWorkChange).toHaveBeenLastCalledWith({
				controllerBusy: true,
				hasRunningTurn: false,
				queuedTurnCount: 0,
			});
		});
	});

	it("reports an accepted local turn while the conversation snapshot is still stale", async () => {
		conversationState.snapshot = {
			capabilities: [],
			controller: { state: "ready" },
			turns: [],
		};
		conversationCommandState.pendingAcceptedTurnId = "turn-accepted";
		const onConversationWorkChange = vi.fn();
		const queryClient = new QueryClient({
			defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
		});

		render(
			<Wrapper client={queryClient}>
				<SessionChatSurface
					session={session}
					onConversationWorkChange={onConversationWorkChange}
				/>
			</Wrapper>,
		);

		await waitFor(() => {
			expect(onConversationWorkChange).toHaveBeenLastCalledWith({
				controllerBusy: true,
				hasRunningTurn: false,
				queuedTurnCount: 0,
			});
		});
	});

	it("returns to idle after the accepted turn appears in the conversation snapshot", async () => {
		conversationState.snapshot = {
			capabilities: [],
			controller: { state: "ready" },
			turns: [
				{
					id: "turn-accepted",
					state: "completed",
					requestedAt: "2026-08-25T09:00:00Z",
				},
			],
		};
		conversationCommandState.pendingAcceptedTurnId = "turn-accepted";
		const onConversationWorkChange = vi.fn();
		const queryClient = new QueryClient({
			defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
		});

		render(
			<Wrapper client={queryClient}>
				<SessionChatSurface
					session={session}
					onConversationWorkChange={onConversationWorkChange}
				/>
			</Wrapper>,
		);

		await waitFor(() => {
			expect(conversationCommandState.acknowledgeAcceptedTurn).toHaveBeenCalledWith(
				"turn-accepted",
			);
			expect(onConversationWorkChange).toHaveBeenLastCalledWith({
				controllerBusy: false,
				hasRunningTurn: false,
				queuedTurnCount: 0,
			});
		});
	});

	it("opens a plain Chat link in the active worker AO Browser", async () => {
		const user = userEvent.setup();
		const queryClient = new QueryClient({
			defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
		});
		const invalidate = vi.spyOn(queryClient, "invalidateQueries").mockResolvedValue(undefined);

		render(
			<Wrapper client={queryClient}>
				<SessionChatSurface session={session} />
			</Wrapper>,
		);
		await user.click(screen.getByRole("button", { name: "Open chat link" }));

		expect(useUiStore.getState().inspectorSessions[session.id]).toMatchObject({
			isOpen: true,
			view: "browser",
		});
		expect(postMock).toHaveBeenCalledWith("/api/v1/sessions/{sessionId}/preview", {
			params: { path: { sessionId: session.id } },
			body: { url: LINK },
		});
		await waitFor(() => expect(invalidate).toHaveBeenCalledWith({ queryKey: workspaceQueryKey }));
	});

	it("asks the confined preview resolver to open a report that Files has not indexed yet", async () => {
		const user = userEvent.setup();
		const queryClient = new QueryClient({
			defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
		});

		render(
			<Wrapper client={queryClient}>
				<SessionChatSurface session={session} onOpenLinkInBrowser={vi.fn()} />
			</Wrapper>,
		);
		await user.click(screen.getByRole("button", { name: "Open report link" }));

		expect(postMock).toHaveBeenCalledWith("/api/v1/sessions/{sessionId}/preview", {
			params: { path: { sessionId: session.id } },
			body: { url: REPORT_LINK, requireWorkspaceFile: true },
		});
	});

	it("opens a plain Chat link from an active orchestrator in its Browser panel", async () => {
		const user = userEvent.setup();
		const openInNewTab = vi.fn().mockResolvedValue(undefined);
		const queryClient = new QueryClient({
			defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
		});
		const orchestratorSession = {
			...session,
			id: "proj-1-orchestrator",
			title: "orchestrator",
			kind: "orchestrator",
		} satisfies WorkspaceSession;

		try {
			render(
				<Wrapper client={queryClient}>
					<SessionChatSurface session={orchestratorSession} onOpenLinkInBrowser={openInNewTab} />
				</Wrapper>,
			);
			await user.click(screen.getByRole("button", { name: "Open chat link" }));

			expect(useUiStore.getState().inspectorSessions[orchestratorSession.id]).toMatchObject({ isOpen: true, view: "browser" });
			expect(openInNewTab).toHaveBeenCalledWith(LINK);
			expect(postMock).not.toHaveBeenCalledWith("/api/v1/sessions/{sessionId}/preview", expect.anything());
		} finally {
			queryClient.clear();
		}
	});

	it("automatically opens the first link in a newly completed agent response once", async () => {
		const queryClient = new QueryClient({
			defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
		});
		const openInBrowser = vi.fn().mockResolvedValue(undefined);
		const view = render(
			<Wrapper client={queryClient}>
				<SessionChatSurface session={session} onOpenLinkInBrowser={openInBrowser} />
			</Wrapper>,
		);

		conversationState.snapshot = {
			capabilities: [],
			items: [{
				kind: "message",
				id: "assistant-1",
				sequence: 1,
				revision: 1,
				role: "assistant",
				origin: "provider",
				text: "Done — see `https://example.com/result`.",
				streaming: false,
				createdAt: "2026-08-08T00:00:01Z",
			}],
		};
		view.rerender(
			<Wrapper client={queryClient}>
				<SessionChatSurface session={session} onOpenLinkInBrowser={openInBrowser} />
			</Wrapper>,
		);

		await waitFor(() => expect(openInBrowser).toHaveBeenCalledWith("https://example.com/result"));
		expect(openInBrowser).toHaveBeenCalledTimes(1);
		conversationState.snapshot = {
			capabilities: [],
			items: [{
				kind: "message", id: "assistant-2", sequence: 2, revision: 1,
				role: "assistant", origin: "provider", text: "Also see https://example.com/second",
				streaming: false, createdAt: "2026-08-08T00:00:02Z",
			}],
		};
		view.rerender(
			<Wrapper client={queryClient}>
				<SessionChatSurface session={session} onOpenLinkInBrowser={openInBrowser} />
			</Wrapper>,
		);
		expect(openInBrowser).toHaveBeenCalledTimes(1);
	});

	it("does not reopen an old assistant link when an unrelated snapshot field changes", async () => {
		const localSession = { ...session, id: "session-link-baseline" };
		const queryClient = new QueryClient({
			defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
		});
		const openInBrowser = vi.fn().mockResolvedValue(undefined);
		const oldAssistant = {
			kind: "message",
			id: "assistant-old",
			sequence: 1,
			revision: 1,
			role: "assistant",
			origin: "provider",
			text: "Old result: https://example.com/old",
			streaming: false,
			createdAt: "2026-08-08T00:00:01Z",
		} satisfies ConversationMessage;
		const currentUser = {
			kind: "message",
			id: "user-current",
			sequence: 2,
			revision: 1,
			role: "user",
			origin: "human",
			text: "Create a new result",
			streaming: false,
			createdAt: "2026-08-08T00:00:02Z",
		} satisfies ConversationMessage;
		conversationState.snapshot = {
			capabilities: [],
			items: [oldAssistant, currentUser],
			latestSequence: 2,
		};
		const view = render(
			<Wrapper client={queryClient}>
				<SessionChatSurface session={localSession} onOpenLinkInBrowser={openInBrowser} />
			</Wrapper>,
		);

		conversationState.snapshot = {
			capabilities: ["config_options"],
			items: [oldAssistant, currentUser],
			latestSequence: 2,
		};
		view.rerender(
			<Wrapper client={queryClient}>
				<SessionChatSurface session={localSession} onOpenLinkInBrowser={openInBrowser} />
			</Wrapper>,
		);
		expect(openInBrowser).not.toHaveBeenCalled();

		conversationState.snapshot = {
			capabilities: ["config_options"],
			items: [
				oldAssistant,
				currentUser,
				{
					kind: "message",
					id: "assistant-current",
					sequence: 3,
					revision: 1,
					role: "assistant",
					origin: "provider",
					text: "New result: https://example.com/new",
					streaming: false,
					createdAt: "2026-08-08T00:00:03Z",
				},
			],
			latestSequence: 3,
		};
		view.rerender(
			<Wrapper client={queryClient}>
				<SessionChatSurface session={localSession} onOpenLinkInBrowser={openInBrowser} />
			</Wrapper>,
		);

		await waitFor(() => expect(openInBrowser).toHaveBeenCalledWith("https://example.com/new"));
		expect(openInBrowser).toHaveBeenCalledTimes(1);
	});

	it("automatically previews a newly completed workspace HTML link", async () => {
		workspacePathsState.paths = ["test-ui.html"];
		const localSession = { ...session, id: "session-local-html" };
		const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
		const openInBrowser = vi.fn().mockResolvedValue(undefined);
		// The link is absolute, so the daemon says which root owns it before the
		// page is previewed.
		getMock.mockImplementation(async (path: string) => path === "/api/v1/sessions/{sessionId}/workspace/resolve"
			? { data: { sessionId: localSession.id, kind: "workspace", path: "test-ui.html" }, error: undefined }
			: { data: {}, error: undefined, response: { status: 200 } });
		const view = render(<Wrapper client={queryClient}><SessionChatSurface session={localSession} onOpenLinkInBrowser={openInBrowser} /></Wrapper>);
		conversationState.snapshot = {
			capabilities: [],
			items: [{ kind: "message", id: "assistant-html", sequence: 1, revision: 1, role: "assistant", origin: "provider", text: "Done: [`test-ui.html`](/tmp/worktree/test-ui.html)", streaming: false, createdAt: "2026-08-08T00:00:01Z" }],
		};
		view.rerender(<Wrapper client={queryClient}><SessionChatSurface session={localSession} onOpenLinkInBrowser={openInBrowser} /></Wrapper>);
		await waitFor(() => expect(openInBrowser).toHaveBeenCalledWith(
			expect.stringContaining("/api/v1/sessions/session-local-html/preview/files/test-ui.html"),
		));
		expect(postMock).not.toHaveBeenCalled();
	});

	it("does not automatically preview or open an absolute HTML link outside the session", async () => {
		workspacePathsState.paths = ["test-ui.html"];
		const localSession = { ...session, id: "session-outside-html" };
		const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
		const openInBrowser = vi.fn().mockResolvedValue(undefined);
		const onOpenFile = vi.fn();
		getMock.mockImplementation(async (path: string) => path === "/api/v1/sessions/{sessionId}/workspace/resolve"
			? { data: { sessionId: localSession.id, kind: "outside", path: "" }, error: undefined }
			: { data: {}, error: undefined, response: { status: 200 } });
		const view = render(<Wrapper client={queryClient}><SessionChatSurface session={localSession} onOpenFile={onOpenFile} onOpenLinkInBrowser={openInBrowser} /></Wrapper>);
		conversationState.snapshot = {
			capabilities: [],
			items: [{ kind: "message", id: "assistant-outside-html", sequence: 1, revision: 1, role: "assistant", origin: "provider", text: "Done: [`test-ui.html`](/other-repo/test-ui.html)", streaming: false, createdAt: "2026-08-08T00:00:01Z" }],
		};
		view.rerender(<Wrapper client={queryClient}><SessionChatSurface session={localSession} onOpenFile={onOpenFile} onOpenLinkInBrowser={openInBrowser} /></Wrapper>);
		await waitFor(() => expect(getMock).toHaveBeenCalledWith("/api/v1/sessions/{sessionId}/workspace/resolve", {
			params: { path: { sessionId: localSession.id }, query: { path: "/other-repo/test-ui.html" } },
		}));
		await new Promise((resolve) => setTimeout(resolve, 0));
		expect(openInBrowser).not.toHaveBeenCalled();
		expect(onOpenFile).not.toHaveBeenCalled();
		expect(postMock).not.toHaveBeenCalled();
	});

	it("opens each plain Chat link in a new AO Browser tab", async () => {
		const user = userEvent.setup();
		const openInNewTab = vi.fn().mockResolvedValue(undefined);
		const queryClient = new QueryClient({
			defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
		});

		render(
			<Wrapper client={queryClient}>
				<SessionChatSurface session={session} onOpenLinkInBrowser={openInNewTab} />
			</Wrapper>,
		);
		await user.click(screen.getByRole("button", { name: "Open chat link" }));

		expect(openInNewTab).toHaveBeenCalledWith(LINK);
		expect(postMock).not.toHaveBeenCalledWith("/api/v1/sessions/{sessionId}/preview", expect.anything());
	});

	it("forwards session tab actions into the chat workspace", () => {
		const queryClient = new QueryClient({
			defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
		});
		render(
			<Wrapper client={queryClient}>
				<SessionChatSurface
					session={session}
					sessionTabAction={<button type="button">Session actions</button>}
				/>
			</Wrapper>,
		);

		expect(screen.getByRole("button", { name: "Session actions" })).toBeInTheDocument();
	});

	it("fences new work without applying the decision-blocking agent lock", () => {
		const queryClient = new QueryClient({
			defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
		});
		render(
			<Wrapper client={queryClient}>
				<SessionChatSurface session={session} newWorkDisabled />
			</Wrapper>,
		);

		expect(screen.getByTestId("chat-agent-input")).toHaveAttribute("data-disabled", "false");
		expect(screen.getByTestId("chat-new-work")).toHaveAttribute("data-disabled", "true");
	});

	it("keeps a selected shell renderable when the conversation is unavailable", () => {
		conversationState.snapshot = undefined;
		conversationState.unavailable = { message: "Controller is unavailable" };
		const queryClient = new QueryClient({
			defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
		});

		render(
			<Wrapper client={queryClient}>
				<SessionChatSurface
					session={session}
					shellTarget={{
						kind: "shell",
						handleId: "shell-1",
						sessionId: session.id,
						title: "shell",
						generation: "2026-08-16T00:00:00Z",
					}}
				/>
			</Wrapper>,
		);

		expect(screen.getByTestId("shell-target")).toHaveTextContent("shell-1");
		expect(screen.queryByText("Conversation unavailable")).not.toBeInTheDocument();
	});

	it("offers the interface switch when the conversation is unavailable", () => {
		conversationState.snapshot = undefined;
		conversationState.unavailable = { message: "Controller is unavailable" };
		const queryClient = new QueryClient({
			defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
		});
		render(
			<Wrapper client={queryClient}>
				<SessionChatSurface session={session} interfaceSwitch={{ control: <button type="button">Switch to terminal UI</button> }} />
			</Wrapper>,
		);
		expect(screen.getByText("Conversation unavailable")).toBeInTheDocument();
		expect(screen.getByRole("button", { name: "Switch to terminal UI" })).toBeInTheDocument();
	});

	it("offers the interface switch when the conversation cannot be loaded", () => {
		conversationState.snapshot = undefined;
		conversationState.error = "Could not load this conversation.";
		render(
			<Wrapper client={new QueryClient()}>
				<SessionChatSurface session={session} interfaceSwitch={{ control: <button type="button">Switch to terminal UI</button> }} />
			</Wrapper>,
		);
		expect(screen.getByText("Could not load this conversation.")).toBeInTheDocument();
		expect(screen.getByRole("button", { name: "Switch to terminal UI" })).toBeInTheDocument();
	});

	it("remounts the chat workspace when switching between chat sessions", () => {
		const first = { ...session, id: "proj-orchestrator-1", kind: "orchestrator" as const };
		const second = { ...session, id: "proj-orchestrator-2", kind: "orchestrator" as const };
		const queryClient = new QueryClient({
			defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
		});
		conversationState.snapshot = snapshotFor(first.id);

		const view = render(
			<Wrapper client={queryClient}>
				<SessionChatSurface session={first} />
			</Wrapper>,
		);

		expect(screen.getByText("Mounted proj-orchestrator-1")).toBeInTheDocument();
		expect(screen.getByText("Rendered proj-orchestrator-1")).toBeInTheDocument();

		conversationState.snapshot = snapshotFor(second.id);
		view.rerender(
			<Wrapper client={queryClient}>
				<SessionChatSurface session={second} />
			</Wrapper>,
		);

		expect(screen.getByText("Mounted proj-orchestrator-2")).toBeInTheDocument();
		expect(screen.getByText("Rendered proj-orchestrator-2")).toBeInTheDocument();
		expect(screen.queryByText("Mounted proj-orchestrator-1")).not.toBeInTheDocument();
	});
});


describe("controller catalogs during an interface handoff", () => {
	it("hides a stale catalog error while a hibernated controller is offline", () => {
		configState.error = "the agent controller for this session is not running";
		conversationState.snapshot = { capabilities: ["config_options"], controller: { state: "hibernated" } };
		render(<Wrapper client={new QueryClient()}><SessionChatSurface session={session} /></Wrapper>);
		expect(screen.getByTestId("config-option-error")).toBeEmptyDOMElement();
	});

	it.each(["stopped", "connecting", "ready"] as const)("waits through handoff with a %s snapshot, then loads catalogs", (state) => {
		conversationState.snapshot = { capabilities: ["config_options"], controller: { state } };
		const client = new QueryClient();
		const { rerender } = render(<Wrapper client={client}><SessionChatSurface session={session} controllerTransitioning /></Wrapper>);
		for (const hook of [useConversationConfigOptions, useConversationModels, useConversationSkills]) {
			expect(hook).toHaveBeenLastCalledWith(session.id, false, undefined);
		}

		conversationState.snapshot = { capabilities: ["config_options"], controller: { state: "ready" } };
		rerender(<Wrapper client={client}><SessionChatSurface session={session} /></Wrapper>);
		for (const hook of [useConversationConfigOptions, useConversationModels, useConversationSkills]) {
			expect(hook).toHaveBeenLastCalledWith(session.id, true, undefined);
		}
	});

	it("does not poll an unavailable controller after a failed handoff", () => {
		conversationState.snapshot = { capabilities: ["config_options"], controller: { state: "stopped" } };
		render(<Wrapper client={new QueryClient()}><SessionChatSurface session={session} /></Wrapper>);
		for (const hook of [useConversationConfigOptions, useConversationModels, useConversationSkills]) {
			expect(hook).toHaveBeenLastCalledWith(session.id, false, undefined);
		}
	});
});

describe("project remembering waits for provider permissions", () => {
	it.each([undefined, "Catalog unavailable"])("withholds Remember when provider catalog is not known (%s)", (error) => {
		conversationState.snapshot = { capabilities: ["config_options"] };
		configState.error = error;
		render(<Wrapper client={new QueryClient()}><SessionChatSurface session={session} /></Wrapper>);
		expect(screen.getByTestId("remember-available")).toHaveTextContent("false");
	});

	it("allows remembering after a model-only catalog successfully loads", () => {
		conversationState.snapshot = { capabilities: ["config_options"] };
		const client = new QueryClient();
		const { rerender } = render(<Wrapper client={client}><SessionChatSurface session={session} /></Wrapper>);
		expect(screen.getByTestId("remember-available")).toHaveTextContent("false");
		configState.loaded = true;
		configState.options = [{ id: "model", name: "Model", category: "model", type: "select", choices: [] }];
		// The real query observer schedules this component when catalog data lands.
		// The lightweight hook mock has no subscription, so change the parent
		// session identity to model that notification through the memo boundary.
		rerender(<Wrapper client={client}><SessionChatSurface session={{ ...session }} /></Wrapper>);
		expect(screen.getByTestId("remember-available")).toHaveTextContent("true");
	});
});


describe("SessionChatSurface continuation identity", () => {
	it("starts a fresh intent when undo makes a previously continued stop eligible again", async () => {
		const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
		conversationState.snapshot = { ...snapshotFor(session.id), continueTurnId: "stopped-1", latestSequence: 1 };
		const view = render(<Wrapper client={client}><SessionChatSurface session={session} /></Wrapper>);
		await userEvent.click(screen.getByRole("button", { name: "Continue test turn" }));
		const originalId = conversationCommandState.send.mock.calls[0]![0].clientMessageId;
		conversationState.snapshot = { ...snapshotFor(session.id), continueTurnId: "", latestSequence: 2 };
		view.rerender(<Wrapper client={client}><SessionChatSurface session={{ ...session }} /></Wrapper>);
		conversationState.snapshot = { ...snapshotFor(session.id), continueTurnId: "stopped-1", latestSequence: 2 };
		view.rerender(<Wrapper client={client}><SessionChatSurface session={{ ...session }} /></Wrapper>);
		await userEvent.click(screen.getByRole("button", { name: "Continue test turn" }));
		expect(conversationCommandState.send.mock.calls[1]![0].clientMessageId).not.toBe(originalId);
	});

	it("reuses an intent id after an ambiguous failure and changes it for a new stopped turn", async () => {
		const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
		conversationState.snapshot = { ...snapshotFor(session.id), continueTurnId: "stopped-1" };
		conversationCommandState.send.mockRejectedValueOnce(new Error("response lost"));
		const view = render(<Wrapper client={client}><SessionChatSurface session={session} /></Wrapper>);
		await userEvent.click(screen.getByRole("button", { name: "Continue test turn" }));
		await userEvent.click(screen.getByRole("button", { name: "Continue test turn" }));
		const first = conversationCommandState.send.mock.calls[0]![0];
		const second = conversationCommandState.send.mock.calls[1]![0];
		expect(first.clientMessageId).toBeTruthy();
		expect(second.clientMessageId).toBe(first.clientMessageId);
		expect(first).toMatchObject({ text: "Continue from where you stopped.", continuation: true });
		conversationState.snapshot = { ...snapshotFor(session.id), continueTurnId: "stopped-2" };
		view.rerender(<Wrapper client={client}><SessionChatSurface session={{ ...session }} /></Wrapper>);
		await userEvent.click(screen.getByRole("button", { name: "Continue test turn" }));
		expect(conversationCommandState.send.mock.calls[2]![0].clientMessageId).not.toBe(first.clientMessageId);
	});
});

describe("side command routing", () => {
	beforeEach(() => {
		useUiStore.setState({ developerMode: true, sideChats: true });
		conversationState.snapshot = { ...conversationState.snapshot, capabilities: ["resume"] };
	});
	afterEach(() => {
		useUiStore.setState({ developerMode: false, sideChats: false });
	});

	it.each([
		["side chats are off", { developerMode: true, sideChats: false }, "codex"],
		["Developer mode is off", { developerMode: false, sideChats: true }, "codex"],
	] as const)("sends /btw to the main chat when %s", async (_case, flags, provider) => {
		useUiStore.setState(flags);
		sideMocks.create.mockReset();
		const mainSend = conversationCommandState.send;
		const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
		render(
			<Wrapper client={queryClient}>
				<SessionChatSurface session={{ ...session, provider }} />
			</Wrapper>,
		);
		await act(async () => {
			await sideMocks.workspaceSend!("/btw what is this?", undefined, "submission-main");
		});
		expect(sideMocks.create).not.toHaveBeenCalled();
		expect(mainSend).toHaveBeenCalledWith(expect.objectContaining({ text: "/btw what is this?", clientMessageId: "submission-main" }));
	});

	it.each(["btw", "side"])(
		"/%s creates a new side for every new submission and reuses a retry key",
		async (command) => {
			sideMocks.create.mockReset().mockImplementation(async (_excerpt, _force, key) => ({ id: `side-${key}` }));
			sideMocks.send.mockReset().mockResolvedValue(undefined);
			const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
			render(
				<Wrapper client={queryClient}>
					<SessionChatSurface session={session} />
				</Wrapper>,
			);
			await act(async () => {
				await sideMocks.workspaceSend!(`/${command} first`, undefined, "submission-1");
				await sideMocks.workspaceSend!(`/${command} second`, undefined, "submission-2");
				await sideMocks.workspaceSend!(`/${command} second`, undefined, "submission-2");
			});
			expect(sideMocks.create.mock.calls).toEqual([
				[undefined, true, "submission-1"],
				[undefined, true, "submission-2"],
				[undefined, true, "submission-2"],
			]);
			expect(sideMocks.send).toHaveBeenNthCalledWith(
				1,
				"side-submission-1",
				"first",
				undefined,
				undefined,
				true,
				"submission-1",
			);
			expect(sideMocks.send).toHaveBeenNthCalledWith(
				2,
				"side-submission-2",
				"second",
				undefined,
				undefined,
				true,
				"submission-2",
			);
		},
	);
	it.each(["btw", "side"])("preserves provider /%s when resume is unavailable", async (command) => {
		conversationState.snapshot = { ...conversationState.snapshot, capabilities: [] };
		sideMocks.create.mockReset();
		const mainSend = vi.fn().mockResolvedValue(undefined);
		(conversationCommandState as { send?: unknown }).send = mainSend;
		render(<Wrapper client={new QueryClient()}><SessionChatSurface session={session} /></Wrapper>);
		await act(async () => { await sideMocks.workspaceSend!(`/${command} question`, undefined, "provider-command"); });
		expect(sideMocks.create).not.toHaveBeenCalled();
		expect(mainSend).toHaveBeenCalledWith(expect.objectContaining({ text: `/${command} question` }));
		delete (conversationCommandState as { send?: unknown }).send;
	});
});

describe("SessionChatSurface startup recovery", () => {
	function conversationRefreshes(invalidate: { mock: { calls: unknown[][] } }) {
		return invalidate.mock.calls.filter(([filters]) =>
			JSON.stringify((filters as { queryKey?: unknown }).queryKey) === JSON.stringify(["conversation", session.id])).length;
	}

	it("refreshes the controller state when the daemon's recovery check settles", () => {
		const queryClient = new QueryClient();
		const invalidate = vi.spyOn(queryClient, "invalidateQueries");
		const tree = (statusReadiness: "checking" | "unavailable" | "ready") =>
			<Wrapper client={queryClient}><SessionChatSurface session={{ ...session, statusReadiness }} /></Wrapper>;
		const view = render(tree("unavailable"));
		view.rerender(tree("checking"));
		expect(conversationRefreshes(invalidate)).toBe(0);
		view.rerender(tree("ready"));
		expect(conversationRefreshes(invalidate)).toBe(1);
		view.rerender(tree("ready"));
		expect(conversationRefreshes(invalidate)).toBe(1);
	});

	it("leaves a session that was ready from the start alone", () => {
		const queryClient = new QueryClient();
		const invalidate = vi.spyOn(queryClient, "invalidateQueries");
		const view = render(<Wrapper client={queryClient}><SessionChatSurface session={{ ...session, statusReadiness: "ready" }} /></Wrapper>);
		view.rerender(<Wrapper client={queryClient}><SessionChatSurface session={{ ...session, statusReadiness: "ready" }} /></Wrapper>);
		expect(conversationRefreshes(invalidate)).toBe(0);
	});
});
