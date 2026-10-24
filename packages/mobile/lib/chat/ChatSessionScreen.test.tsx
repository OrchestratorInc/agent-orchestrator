import { act, create, type ReactTestRenderer } from "react-test-renderer";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import type { ComponentProps } from "react";
import type { DashboardSession, OrchestratorLink } from "../api";
import type { ConversationSnapshot } from "./types";
import { Alert } from "react-native";

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true, IS_REACT_NATIVE_TEST_ENVIRONMENT: true, requestAnimationFrame: (cb: () => void) => { cb(); return 1; }, cancelAnimationFrame: vi.fn() });
const state = vi.hoisted(() => ({
	options: {} as Record<string, any>,
	push: vi.fn(), openPage: vi.fn(), openShell: vi.fn(async () => ({ handleId: "shell-handle", title: "Shell" })),
	refresh: vi.fn(async () => {}), loadTurnOptions: vi.fn(async () => ({ models: [], configOptions: [] })),
	start: vi.fn(async () => {}), rename: vi.fn(), pin: vi.fn(), kill: vi.fn(),
	retryStartup: vi.fn(async () => {}),
	resumeSession: vi.fn(async () => {}),
	conversation: {} as Record<string, any>,
	cloudPRs: [] as any[],
}));
const config = { host: "localhost", httpPort: "1234", muxPort: "1234", password: "test", hostId: "local-host" };
const navigation = { setOptions: (options: object) => Object.assign(state.options, options) };
const router = { push: state.push };
const controls = { status: { supported: true }, start: state.start, refresh: vi.fn(), loading: false, starting: false };
let filesCurrent = true;
const workspace = { scopeKey: "cloud-account", isCurrent: () => filesCurrent };
const capability = { scopeKey: "cloud-account", isCurrent: () => filesCurrent };
const sessionSource = { scopeKey: "cloud-account", workspace: workspace as typeof workspace | undefined, preview: undefined as typeof capability | undefined, pullRequests: undefined as typeof capability | undefined, retryStartup: state.retryStartup, resumeSession: state.resumeSession };
const app = {
	configForHost: () => config, hostStates: [], scopedBoard: { projects: [] },
	sourceFor: () => sessionSource, refreshSource: vi.fn(async () => {}), setActiveProject: vi.fn(),
	setWorkerPinnedOn: state.pin, renameWorkerOn: state.rename, killOn: state.kill, restoreOn: vi.fn(), resumeAgentOn: vi.fn(),
};
vi.mock("react-native", () => ({
	ActivityIndicator: "ActivityIndicator", Alert: { alert: vi.fn() },
	AppState: { currentState: "active", addEventListener: () => ({ remove: vi.fn() }) },
	Easing: { bezier: vi.fn() }, Keyboard: { dismiss: vi.fn(), isVisible: () => false, metrics: () => undefined, addListener: () => ({ remove: vi.fn() }) }, Platform: { OS: "android" },
	Pressable: "Pressable", Text: "Text", View: "View", StyleSheet: { create: (styles: unknown) => styles, hairlineWidth: 1 },
}));
// The real startup helpers now load animation and focus APIs alongside the screen.
vi.mock("react-native-reanimated", () => ({ default: { View: "AnimatedView" }, FadeOut: { duration: vi.fn() }, LinearTransition: { duration: vi.fn() } }));
vi.mock("expo-router", () => ({ useNavigation: () => navigation, useRouter: () => router }));
vi.mock("expo-router/react-navigation", () => ({ useIsFocused: () => true }));
vi.mock("expo-router/build/react-navigation/elements", () => ({ useHeaderHeight: () => 40 }));
vi.mock("react-native-keyboard-controller", () => ({ KeyboardAvoidingView: "KeyboardAvoidingView", KeyboardEvents: { addListener: () => ({ remove: vi.fn() }) }, useKeyboardState: (select: (value: object) => unknown) => select({ height: 0, isVisible: false }) }));
vi.mock("react-native-safe-area-context", () => ({ useSafeAreaInsets: () => ({ bottom: 0 }) }));
vi.mock("@react-native-async-storage/async-storage", () => ({ default: { getItem: vi.fn(async () => null), setItem: vi.fn(), removeItem: vi.fn() } }));
vi.mock("../api", () => ({ mobileReachablePreviewURL: () => undefined }));
vi.mock("../config", () => ({ machineIdentity: () => "local-host" }));
vi.mock("../store", () => ({ useApp: () => app }));
vi.mock("../icons", () => ({ Feather: "Feather" }));
vi.mock("../haptics", () => ({ haptics: { tap: vi.fn(), warning: vi.fn() } }));
vi.mock("../ThemeProvider", () => ({ useTheme: () => ({}), useThemedStyles: (factory: (theme: object) => unknown) => factory({}) }));
vi.mock("../native-header-button", () => ({ NativeHeaderButton: "NativeHeaderButton" }));
vi.mock("../openGitHub", () => ({ openGitHub: vi.fn() }));
vi.mock("../pageNavigation", () => ({ useOpenPage: () => state.openPage }));
vi.mock("../usePRSummaries", () => ({ usePRSummaries: () => ({ summaryFor: vi.fn() }) }));
vi.mock("../pr/useSourcePRs", () => ({ useSourcePRs: () => ({ list: () => state.cloudPRs, reload: vi.fn() }) }));
vi.mock("../session/useInterfaceTransition", () => ({
	useInterfaceTransition: () => controls, mobileInterfaceTransitionIsActive: () => false,
	mobileInterfaceTransitionIsBusy: () => false, mobileInterfaceTransitionIsCancellable: () => false,
	mobileInterfaceTransitionRecoveryMessage: () => undefined,
}));
vi.mock("../cloud/useCloudInterfaceTransition", () => ({ useCloudInterfaceTransition: () => controls }));
vi.mock("./api", () => ({ openSessionShell: state.openShell, getWorkspacePaths: vi.fn() }));
vi.mock("./useConversation", () => ({ useMobileConversation: () => state.conversation }));
vi.mock("./useChatViewLease", () => ({ useChatViewLease: vi.fn() }));
vi.mock("./ChatComposer", () => ({ ChatComposer: "ChatComposer" }));
vi.mock("./ChatMarkdown", () => ({ ChatLinkProvider: ({ children }: { children: unknown }) => children }));
vi.mock("./ChatTimeline", () => ({ ChatTimeline: "ChatTimeline" }));
vi.mock("./ConversationTitle", () => ({ ConversationTitle: "ConversationTitle" }));
vi.mock("../files/useChangeSummary", () => ({ useChangeSummary: vi.fn(() => null) }));

import { ChatSessionScreen } from "./ChatSessionScreen";
import { readChatSheet, resetChatSheets, type ConversationActionsEntry } from "./chatSheetRegistry";

const worker: DashboardSession = {
	id: "same-session", projectId: "project", kind: "worker", status: "working", mode: "chat", harness: "codex",
	branch: null, issueId: null, issueTitle: null, userPrompt: null, displayName: "Worker", summary: null,
	createdAt: "2026-10-15T00:00:00Z", lastActivityAt: "2026-10-15T00:00:00Z", runtimeConnected: true,
	cloud: { desiredState: "running", observedState: "running" },
};
const snapshot: ConversationSnapshot = {
	conversationId: "conversation", sessionId: worker.id, harness: "codex", mode: "chat", controller: { state: "ready" },
	latestSequence: 0, oldestSequence: 0, hasMoreBefore: false, turns: [], items: [], settings: {}, capabilities: ["rename"],
};
let screen: ReactTestRenderer;
beforeEach(() => {
	filesCurrent = true; sessionSource.workspace = workspace;
	sessionSource.preview = undefined; sessionSource.pullRequests = undefined; state.cloudPRs = [];
	resetChatSheets();
	state.options = {};
	state.conversation = {
		snapshot, models: [], configOptions: [], skills: [], pendingActions: [], pendingSends: [], actionCodes: {}, actionErrors: {},
		refresh: state.refresh, loadTurnOptions: state.loadTurnOptions,
	};
});
afterEach(() => { if (screen) act(() => screen.unmount()); vi.clearAllMocks(); });

it.each(["cloud", "local"] as const)("passes only %s routed connection to attachment rendering", async (kind) => {
	await act(async () => { screen = create(<ChatSessionScreen session={worker} source={{ kind, id: kind === "local" ? "local-host" : "org-one" }} />); });
	expect(screen.root.findByType("ChatTimeline" as never).props.config).toBe(kind === "local" ? config : null);
});

it.each(["cloud", "local"] as const)("only wires queued promotion for a local conversation, not %s", async (kind) => {
	const promote = vi.fn(async () => {});
	const cancel = vi.fn(async () => {});
	state.conversation.promoteQueuedTurn = promote;
	state.conversation.cancelQueuedTurn = cancel;
	await act(async () => { screen = create(<ChatSessionScreen session={worker} source={{ kind, id: kind === "cloud" ? "org-one" : "local-host" }} />); });
	const composer = screen.root.findByType("ChatComposer" as never);
	expect(composer.props.onPromoteQueuedTurn).toBe(kind === "local" ? promote : undefined);
	expect(composer.props.onCancelQueuedTurn).toBe(cancel);
});

it("offers Resume rather than startup Retry while a failed workspace is paused", async () => {
	const paused = { ...worker, runtimeConnected: false, cloud: { desiredState: "paused", observedState: "stopped", startupError: { code: "project_setup_failed", message: "Setup failed before the pause", at: "now" } } };
	await act(async () => { screen = create(<ChatSessionScreen session={paused} source={{ kind: "cloud", id: "org-one" }} />); });
	const buttons = screen.root.findAllByType("Pressable" as never);
	expect(buttons.filter((node) => node.findAllByProps({ children: "Retry" }).length > 0)).toHaveLength(0);
	const resume = buttons.find((node) => node.findAllByProps({ children: "Resume" }).length > 0);
	expect(resume).toBeDefined();
	await act(async () => resume!.props.onPress());
	expect(state.resumeSession).toHaveBeenCalledExactlyOnceWith(worker.id);
	expect(state.retryStartup).not.toHaveBeenCalled();
});

it.each(["repository_checkout_failed", "working_directory_unusable", "project_setup_failed"])("shows %s and explicit retry in Chat for a connected worker", async (code) => {
	const failed = { ...worker, cloud: { ...worker.cloud, startupError: { code, message: "Workspace preparation failed", at: "now" } } };
	await act(async () => { screen = create(<ChatSessionScreen session={failed} source={{ kind: "cloud", id: "org-one" }} />); });
	expect(screen.root.findAllByProps({ children: "Workspace preparation failed" }).length).toBeGreaterThan(0);
	expect(screen.root.findByType("ChatComposer" as never).props.disabled).toBe(true);
	expect(state.retryStartup).not.toHaveBeenCalled();
	const retry = screen.root.findAllByType("Pressable" as never).find((node) => node.findAllByProps({ children: "Retry" }).length > 0);
	expect(retry).toBeDefined();
	await act(async () => retry!.props.onPress());
	expect(state.retryStartup).toHaveBeenCalledExactlyOnceWith(worker.id);
});

async function openMenu(session: DashboardSession | OrchestratorLink = worker, source: ComponentProps<typeof ChatSessionScreen>["source"] = { kind: "cloud", id: "org-one" }) {
	await act(async () => { screen = create(<ChatSessionScreen session={session} source={source} />); });
	let header: ReactTestRenderer;
	act(() => { header = create(state.options.headerRight()); });
	await act(async () => { header!.root.findByProps({ label: "Conversation actions" }).props.onPress(); });
	act(() => header!.unmount());
	const route = state.push.mock.lastCall?.[0];
	expect(route.pathname).toBe("/sheets/conversation-actions");
	state.push.mockClear();
	return readChatSheet(route.params.sheetKey) as ConversationActionsEntry;
}

it("routes a Cloud worker shell using the organization source instead of a local host", async () => {
	const entry = await openMenu();
	await act(async () => { entry.onOpenShell(); });
	expect(state.push).toHaveBeenCalledExactlyOnceWith({ pathname: "/session/[id]", params: { id: "same-session", source: "cloud", sourceId: "org-one", view: "shell" } });
	expect(state.openShell).not.toHaveBeenCalled();
});

it("opens Cloud orchestrator Files but keeps unsupported workspace actions guarded", async () => {
	const orchestrator: OrchestratorLink = { id: "orchestrator", projectId: "project", projectName: "Project", mode: "chat", runtimeConnected: true, cloud: worker.cloud };
	const entry = await openMenu(orchestrator);
	await act(async () => { entry.onFiles(); entry.onOpenShell(); entry.onPreview(); entry.onPullRequests(); entry.onRename(); entry.onTogglePin(); entry.onDelete(); });
	expect(state.push).toHaveBeenCalledExactlyOnceWith({ pathname: "/files/[sessionId]", params: { sessionId: "orchestrator", source: "cloud", sourceId: "org-one", scopeKey: "cloud-account", title: "Project" } });
	expect(state.openPage).not.toHaveBeenCalled();
	expect(state.openShell).not.toHaveBeenCalled();
	expect(state.rename).not.toHaveBeenCalled();
	expect(state.pin).not.toHaveBeenCalled();
	expect(state.kill).not.toHaveBeenCalled();
	expect(Alert.alert).not.toHaveBeenCalled();
	expect(entry.canOpenFiles).toBe(true);
	expect(entry.canOpenShell).toBe(false);
	expect(entry.canPreview).toBe(false);
	expect(entry.canPullRequests).toBe(false);
});

it("guards unsupported Cloud worker preview and PR routes", async () => {
	const entry = await openMenu();
	await act(async () => { entry.onPreview(); entry.onPullRequests(); });
	expect(state.push).not.toHaveBeenCalled();
	expect(state.openPage).not.toHaveBeenCalled();
	expect(entry.canOpenFiles).toBe(true);
	expect(entry.canOpenShell).toBe(true);
	expect(entry.canPreview).toBe(false);
	expect(entry.canPullRequests).toBe(false);
});

it("opens supported Cloud PR and browser routes with the account owner, never a local host", async () => {
	sessionSource.preview = capability; sessionSource.pullRequests = capability;
	const entry = await openMenu();
	expect(entry.canPreview).toBe(true); expect(entry.canPullRequests).toBe(true);
	await act(async () => { entry.onPreview(); entry.onPullRequests(); });
	expect(state.push).toHaveBeenCalledWith({ pathname: "/preview/[id]", params: { id: worker.id, title: "Worker", source: "cloud", sourceId: "org-one", scopeKey: "cloud-account" } });
	expect(state.push).toHaveBeenCalledWith({ pathname: "/(tabs)/prs", params: { source: "cloud", sourceId: "org-one", projectId: "project" } });
	state.push.mockClear(); filesCurrent = false;
	await act(async () => { entry.onPreview(); entry.onPullRequests(); });
	expect(state.push).not.toHaveBeenCalled();
});

it("shows the Cloud review shortcut from its PR source and qualifies the detail link", async () => {
	sessionSource.pullRequests = capability;
	const pr = { number: 7, url: "https://github.com/owner/repo/pull/7", state: "open", title: "Cloud fix" };
	state.cloudPRs = [pr];
	await act(async () => { screen = create(<ChatSessionScreen session={worker} source={{ kind: "cloud", id: "org-one" }} />); });
	const composer = screen.root.findByType("ChatComposer" as never);
	expect(composer.props.reviewPR).toMatchObject(pr);
	expect(composer.props.reviewPRSummary).toEqual(pr);
	await act(async () => composer.props.onOpenReview());
	expect(state.openPage).toHaveBeenCalledWith({ pathname: "/review/[sessionId]", params: { sessionId: worker.id, prNumber: "7", prUrl: pr.url, source: "cloud", sourceId: "org-one", scopeKey: "cloud-account" } });
});

it("opens Cloud Files with its account and organization instead of the selected local host", async () => {
	const entry = await openMenu();
	await act(async () => { entry.onFiles(); });
	expect(state.push).toHaveBeenCalledExactlyOnceWith({ pathname: "/files/[sessionId]", params: { sessionId: "same-session", source: "cloud", sourceId: "org-one", scopeKey: "cloud-account", title: "Worker" } });
});

it("does not let a parked Files callback navigate after its Cloud owner retires", async () => {
	const entry = await openMenu();
	filesCurrent = false;
	await act(async () => { entry.onFiles(); });
	expect(state.push).not.toHaveBeenCalled();
});

it("does not expose Files when the session has no workspace capability", async () => {
	sessionSource.workspace = undefined;
	const entry = await openMenu();
	await act(async () => { entry.onFiles(); });
	expect(entry.canOpenFiles).toBe(false); expect(state.push).not.toHaveBeenCalled();
});

it("keeps Cloud history, settings, refresh and Terminal UI actions functional", async () => {
	const entry = await openMenu();
	await act(async () => { entry.onMap(); entry.onSettings(); entry.onRefresh(); entry.onSwitchInterface(); });
	const sheets = state.push.mock.calls.map(([route]) => readChatSheet(route.params.sheetKey)?.kind);
	expect(sheets).toEqual(["conversation-map", "turn-settings"]);
	expect(state.refresh).toHaveBeenCalledOnce();
	expect(state.loadTurnOptions).toHaveBeenCalled();
	expect(state.start).toHaveBeenCalledExactlyOnceWith("tui", "drain", {});
});

it("opens Cloud settings with the native model and effort returned by the freshly loaded catalog", async () => {
	const entry = await openMenu();
	state.loadTurnOptions.mockResolvedValueOnce({ models: [], configOptions: [], settings: { model: "native-sonnet", reasoningEffort: "high" } } as never);
	await act(async () => { entry.onSettings(); });
	const route = state.push.mock.calls[0][0];
	const settings = readChatSheet(route.params.sheetKey);
	expect(settings?.kind).toBe("turn-settings");
	if (settings?.kind === "turn-settings") expect(settings.snapshot.settings).toMatchObject({ model: "native-sonnet", reasoningEffort: "high" });
});

it.each(["cloud", "local"] as const)("ignores a superseded Cloud catalog without changing the %s fallback settings flow", async (kind) => {
	const entry = await openMenu(worker, { kind, id: kind === "cloud" ? "org-one" : "local-host" });
	state.loadTurnOptions.mockRejectedValueOnce(Object.assign(new Error("Superseded discovery"), { name: "AbortError" }));
	await act(async () => { entry.onSettings(); });
	expect(state.push).toHaveBeenCalledTimes(kind === "cloud" ? 0 : 1);
});

it("keeps local shell and preview routes qualified by their host", async () => {
	const entry = await openMenu(worker, { kind: "local", id: "local-host" });
	await act(async () => { entry.onFiles(); entry.onOpenShell(); entry.onPreview(); });
	expect(state.openShell).toHaveBeenCalledExactlyOnceWith(config, "same-session", "project");
	expect(state.push).toHaveBeenCalledWith({ pathname: "/shell/[handleId]", params: { handleId: "shell-handle", projectId: "project", sessionId: "same-session", title: "Shell", hostId: "local-host" } });
	expect(state.push).toHaveBeenCalledWith({ pathname: "/files/[sessionId]", params: { sessionId: "same-session", title: "Worker", hostId: "local-host" } });
	expect(state.push).toHaveBeenCalledWith({ pathname: "/preview/[id]", params: { id: "same-session", title: "Worker", hostId: "local-host" } });
	expect(entry.canOpenFiles).toBe(true);
});
