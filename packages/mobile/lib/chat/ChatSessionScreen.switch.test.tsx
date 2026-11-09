import { act, create, type ReactTestRenderer } from "react-test-renderer";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const fixtures = vi.hoisted(() => {
	const interfaceSwitch = () => ({
		status: { supported: true } as { supported?: boolean } | undefined,
		transition: undefined as Record<string, unknown> | undefined,
		loading: false,
		starting: false,
		cancelling: false,
		acknowledgingNotice: false,
		error: undefined as string | undefined,
		acknowledgeNoticeError: undefined as string | undefined,
		fetchFailed: false,
		start: vi.fn(async () => {}),
		cancel: vi.fn(async () => {}),
		acknowledgeNotice: vi.fn(async (_id: string) => {}),
		refresh: vi.fn(async () => undefined),
	});
	const conversation = () => ({
		snapshot: {
			title: "Fix the bug",
			harness: "claude-code",
			turns: [] as unknown[],
			items: [] as unknown[],
			controller: { state: "running" } as { state: string; error?: string },
		},
		loading: false,
		error: undefined as string | undefined,
		unavailable: undefined,
		pendingActions: [] as string[],
		pendingSends: [] as unknown[],
		actionCodes: {},
		actionErrors: {},
		actionError: undefined,
		loadingOlder: false,
		skills: [],
		configOptions: [],
		models: [],
		refresh: vi.fn(async () => {}),
		loadTurnOptions: vi.fn(async () => {}),
	});
	return {
		interfaceSwitch: interfaceSwitch(),
		freshInterfaceSwitch: interfaceSwitch,
		conversation: conversation(),
		freshConversation: conversation,
		refreshBoard: vi.fn(async () => {}),
		navigation: { setOptions: vi.fn() },
		router: { push: vi.fn(), back: vi.fn(), replace: vi.fn(), canGoBack: () => true },
	};
});

// A stand-in that renders as a named host element, so tests read its props.
const host = vi.hoisted(() => async (name: string) => {
	const React = await import("react");
	return ({ children, ...props }: { children?: import("react").ReactNode }) => React.createElement(name, props, children);
});

vi.mock("react-native", async () => ({
	Alert: { alert: vi.fn() },
	Keyboard: { addListener: () => ({ remove: () => {} }), dismiss: vi.fn() },
	Platform: { OS: "ios", Version: "18.0", select: (options: Record<string, unknown>) => options.ios ?? options.default },
	StyleSheet: { create: (styles: object) => styles, absoluteFill: {}, hairlineWidth: 1 },
	View: await host("View"),
	Text: await host("Text"),
}));
vi.mock("expo-router", async () => {
	const Toolbar = Object.assign(await host("StackToolbar"), { Button: await host("StackToolbarButton") });
	return { Stack: { Toolbar }, useIsFocused: () => true, useNavigation: () => fixtures.navigation, useRouter: () => fixtures.router };
});
vi.mock("expo-router/react-navigation", () => ({ useHeaderHeight: () => 0 }));
vi.mock("react-native-keyboard-controller", async () => ({ KeyboardAvoidingView: await host("KeyboardAvoidingView") }));
vi.mock("../api", () => ({ mobileReachablePreviewURL: () => undefined, restoreSession: vi.fn(), resumeSessionAgent: vi.fn() }));
vi.mock("../haptics", () => ({ haptics: { tap: vi.fn(), success: vi.fn(), warning: vi.fn(), error: vi.fn() } }));
vi.mock("../openGitHub", () => ({ openGitHub: vi.fn() }));
vi.mock("../InlineBanner", async () => ({ InlineBanner: await host("InlineBanner") }));
vi.mock("../store", () => ({
	useApp: () => ({
		config: { host: "dev.local" },
		machine: "machine-1",
		currentHostId: "host-1",
		connection: "open",
		unreachable: false,
		linkHealth: "ok",
		projects: [],
		sessions: [],
		refresh: fixtures.refreshBoard,
		setWorkerPinned: vi.fn(),
		kill: vi.fn(),
	}),
}));
vi.mock("../session/useInterfaceTransition", async () => ({
	...(await vi.importActual<typeof import("../session/interfaceTransition")>("../session/interfaceTransition")),
	useInterfaceTransition: () => fixtures.interfaceSwitch,
}));
vi.mock("../session/InterfaceSwitchCard", async () => ({ InterfaceSwitchCard: await host("InterfaceSwitchCard") }));
vi.mock("../frost", async () => ({ Frost: await host("Frost"), FROST_TAIL: 0, frostStops: () => [] }));
vi.mock("../stackScreenOptions", () => ({ stackScreenOptions: () => ({}), SYSTEM_GLASS_HEADER: false }));
vi.mock("../ThemeProvider", () => {
	const theme = new Proxy({}, { get: (_target, key) => `#${String(key)}` });
	return { useTheme: () => theme, useThemedStyles: (factory: (value: unknown) => unknown) => factory(theme) };
});
vi.mock("./api", () => ({ getWorkspacePaths: vi.fn(), openSessionShell: vi.fn() }));
vi.mock("./requestDockModel", () => ({ requestDockModel: () => undefined }));
vi.mock("./ChatComposer", async () => ({ ChatComposer: await host("ChatComposer") }));
vi.mock("./KeyboardFollower", async () => ({ KeyboardFollower: await host("KeyboardFollower") }));
vi.mock("./ChatMarkdown", async () => ({ ChatLinkProvider: await host("ChatLinkProvider") }));
vi.mock("./ChatTimeline", async () => ({ ChatTimeline: await host("ChatTimeline") }));
vi.mock("./McpServerNotice", async () => ({ McpServerNotice: await host("McpServerNotice") }));
vi.mock("./useChatKeyboard", () => ({
	useChatKeyboard: () => ({ keyboardVisible: false, rootStyle: {}, behavior: "padding", keyboardVerticalOffset: 0, composerLift: 0, restingInset: 0, dockRiseOverride: undefined }),
}));
vi.mock("./ConversationTitle", async () => ({ ConversationTitle: await host("ConversationTitle") }));
vi.mock("./chatSheetRegistry", () => ({ chatSheetRoute: () => "/sheet" }));
vi.mock("./conversationMenuModel", () => ({ conversationMenuGroups: () => [] }));
vi.mock("./conversationChrome", () => ({ quotaWarning: () => undefined, contextReadout: () => undefined }));
vi.mock("./autoResume", () => ({ shouldAutoResume: () => false }));
vi.mock("./conversationBanners", () => ({
	controllerStoppedBanner: () => ({ key: "stopped", title: "The agent stopped" }),
	errorBanner: () => undefined,
	mcpNoticeKey: () => "notice",
	quotaBanner: () => undefined,
	reauthBanner: () => undefined,
	rolledBackBanner: () => undefined,
	threadBanner: () => undefined,
}));
vi.mock("./conversationErrors", () => ({ conversationActionError: () => undefined, conversationActionUnsupported: () => false }));
vi.mock("./turnOptionsCatalog", () => ({ providerCatalogLive: () => false, turnOptionsLoadFor: () => undefined }));
vi.mock("./types", () => ({ brokenMcpServers: () => [], can: () => false }));
vi.mock("./useConversation", () => ({ useMobileConversation: () => fixtures.conversation }));
vi.mock("../useLastVerifiedConfig", () => ({ useLastVerifiedConfig: () => undefined }));
vi.mock("./SessionStartup", async () => ({ SessionStartupStatus: await host("SessionStartupStatus") }));
vi.mock("../session/sessionStartup", () => ({ sessionStartupView: () => undefined, startupVisible: () => false }));
vi.mock("../session/serialPoll", () => ({ startSerialPoll: () => () => {} }));
vi.mock("../session/startupPoll", () => ({ STARTUP_POLL_MS: 1_000 }));
vi.mock("../session/useStartupSession", () => ({ useStartupSession: (_config: unknown, session: unknown) => session }));
vi.mock("../session/useSessionPreview", () => ({ useSessionPreview: () => false }));
vi.mock("../toolbarIcon", () => ({ toolbarIcon: (name: string) => name }));
vi.mock("./useChatViewLease", () => ({ useChatViewLease: () => {} }));
vi.mock("../pageNavigation", () => ({ useOpenPage: () => vi.fn() }));
vi.mock("../usePRSummaries", () => ({ usePRSummaries: () => ({ summaryFor: () => undefined }) }));
vi.mock("../reviewView", () => ({ reviewRouteForPR: vi.fn(), reviewRouteForSession: vi.fn(), sessionPRReadyForReview: () => undefined }));
vi.mock("../tokens", () => ({ space: { md: 12, xl: 20 } }));
vi.mock("../ui", async () => ({ Button: await host("Button"), EmptyState: await host("EmptyState") }));
vi.mock("../backNavigation", () => ({ backOr: vi.fn() }));
vi.mock("../files/navigation", async () => {
	const React = await import("react");
	return { changedPathCount: () => 0, fileRoute: vi.fn(), filesRoute: vi.fn(), FilesNavigationContext: React.createContext(null) };
});
vi.mock("../files/useChangeSummary", () => ({ useChangeSummary: () => undefined }));
vi.mock("./automationMessages", () => ({ sessionLink: () => undefined }));
vi.mock("./sessionLookup", () => ({ readSession: vi.fn(async () => undefined) }));

import { ChatSessionScreen } from "./ChatSessionScreen";
import { INTERFACE_HANDOFF_WAIT_MS } from "../session/useInterfaceHandoff";

const session = { id: "worker-1", projectId: "project-1", displayName: "Fix the bug", status: "working", activity: "idle", mode: "chat" } as never;
let renderer: ReactTestRenderer | undefined;

beforeEach(() => {
	vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true);
	fixtures.interfaceSwitch = fixtures.freshInterfaceSwitch();
	fixtures.conversation = fixtures.freshConversation();
	fixtures.refreshBoard.mockReset().mockResolvedValue(undefined);
	const error = console.error;
	vi.spyOn(console, "error").mockImplementation((...args) => {
		if (String(args[0]).startsWith("react-test-renderer is deprecated")) return;
		error(...args);
	});
});

afterEach(async () => {
	if (renderer) await act(async () => renderer?.unmount());
	renderer = undefined;
	vi.useRealTimers();
	vi.restoreAllMocks();
	vi.unstubAllGlobals();
});

async function mount() {
	await act(async () => {
		renderer = create(<ChatSessionScreen session={session} />);
	});
}
async function rerender() {
	await act(async () => renderer!.update(<ChatSessionScreen session={session} />));
}
const find = (type: string) => renderer!.root.findAllByType(type as never);
const card = () => find("InterfaceSwitchCard")[0];
const bannerTitles = () => find("InlineBanner").map((banner) => banner.props.title);
const composer = () => find("ChatComposer")[0];

describe("Chat while it switches to Terminal", () => {
	it("keeps the conversation readable behind a banner while the turn drains", async () => {
		fixtures.interfaceSwitch.transition = { id: "t-1", phase: "draining", targetMode: "tui" };
		await mount();
		expect(find("InterfaceSwitchCard")).toHaveLength(0);
		expect(find("ChatTimeline")).toHaveLength(1);
		const [banner] = find("InlineBanner");
		expect(banner.props).toMatchObject({ title: "Switching to Terminal UI · finishing current work", action: "Cancel" });
		await act(async () => (banner.props.onPress as () => void)());
		expect(fixtures.interfaceSwitch.cancel).toHaveBeenCalledTimes(1);
		expect(composer().props.disabled).toBe(true);
	});

	it("covers the conversation with the card once the Chat controller stops", async () => {
		fixtures.interfaceSwitch.transition = { id: "t-1", phase: "draining", targetMode: "tui" };
		await mount();
		fixtures.interfaceSwitch.transition = { id: "t-1", phase: "source_stopping", targetMode: "tui" };
		// The stop the switch asked for, not something to Resume.
		fixtures.conversation.snapshot.controller = { state: "stopped" };
		await rerender();
		expect(card().props).toMatchObject({ title: "Switching to Terminal", message: "Stopping the Chat controller before the terminal starts.", cancellable: false });
		expect(bannerTitles()).toEqual([]);
		expect(composer().props.disabled).toBe(true);
	});

	it("holds the card through the gap before the route swaps to Terminal, then offers Retry", async () => {
		vi.useFakeTimers();
		fixtures.interfaceSwitch.transition = { id: "t-1", phase: "activating", targetMode: "tui" };
		await mount();
		fixtures.interfaceSwitch.transition = { id: "t-1", phase: "completed", targetMode: "tui" };
		fixtures.conversation.snapshot.controller = { state: "stopped" };
		await rerender();
		expect(card().props).toMatchObject({ title: "Switching to Terminal", message: "Terminal UI is ready. Opening it now." });
		expect(bannerTitles()).toEqual([]);
		expect(composer().props.disabled).toBe(true);

		await act(async () => { vi.advanceTimersByTime(INTERFACE_HANDOFF_WAIT_MS); });
		expect(card().props).toMatchObject({ title: "Terminal UI is ready", ready: true, retryable: true });
		await act(async () => (card().props.onRetry as () => void)());
		expect(fixtures.refreshBoard).toHaveBeenCalledTimes(1);
	});

	it("shows the stopped controller as before for a switch that completed before this visit", async () => {
		fixtures.interfaceSwitch.transition = { id: "t-0", phase: "completed", targetMode: "tui" };
		fixtures.conversation.snapshot.controller = { state: "stopped" };
		await mount();
		expect(find("InterfaceSwitchCard")).toHaveLength(0);
		expect(bannerTitles()).toEqual(["The agent stopped"]);
		expect(composer().props.disabled).toBe(false);
	});
});
