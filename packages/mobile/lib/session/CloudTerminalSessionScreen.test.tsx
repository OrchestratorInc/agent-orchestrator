import { act, create, type ReactTestRenderer } from "react-test-renderer";
import { Children, isValidElement, type ReactElement, type ReactNode } from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { Alert } from "react-native";
import type { RouteSession } from "./sessionRoute";
import type { CloudInterfaceControls } from "../cloud/useCloudInterfaceTransition";
import type { CloudInterfaceStatus } from "../cloud/interfaceTransition";
import { useChatViewLease } from "../chat/useChatViewLease";
import { useCloudInterfaceTransition } from "../cloud/useCloudInterfaceTransition";

const fixture = vi.hoisted(() => {
	const workspace = { isCurrent: vi.fn(() => true) };
	const preview = { scopeKey: "cloud-account-1", isCurrent: vi.fn(() => true) };
	const source = { scopeKey: "cloud-account-1", workspace: workspace as typeof workspace | undefined, preview: preview as typeof preview | undefined, switchToChat: vi.fn(), resumeSession: vi.fn(), restoreSession: vi.fn(), retryStartup: vi.fn(), startInterfaceTransition: vi.fn() };
	return {
		source,
		workspace,
		preview,
		isCurrent: vi.fn(() => true),
		sourceFor: vi.fn<() => typeof source | undefined>(() => source),
		client: {},
		sessionEpoch: 1,
		scheme: "dark" as "dark" | "light",
		refreshSource: vi.fn(async () => {}),
		setOptions: vi.fn(),
		replace: vi.fn(),
		push: vi.fn(),
		write: vi.fn(),
		webViewResize: vi.fn(),
		sendInput: vi.fn(() => true),
		sendPrompt: vi.fn(async () => true),
		onStatus: null as null | ((status: string) => void),
		terminals: [] as Array<{ input: { kind?: string; terminalId?: string; onStatus: (status: string) => void; onError: (message: string) => void; onOutput: (bytes: Uint8Array) => void }; disconnect: ReturnType<typeof vi.fn>; resize: ReturnType<typeof vi.fn> }>,
	};
});

vi.mock("@expo/vector-icons", async () => {
	const React = await import("react");
	return { Feather: (props: object) => React.createElement("Feather", props) };
});
vi.mock("../icons", async () => {
	const React = await import("react");
	return { Feather: (props: object) => React.createElement("Feather", props) };
});
vi.mock("react-native-reanimated", () => ({ default: { View: "AnimatedView" }, FadeOut: { duration: () => undefined }, LinearTransition: { duration: () => undefined } }));
vi.mock("../ui", async () => {
	const React = await import("react");
	return { Spinning: ({ children }: { children: React.ReactNode }) => React.createElement("Spinning", {}, children), useBreathing: () => undefined };
});
vi.mock("../useReducedMotion", () => ({ useReducedMotion: () => true }));
vi.mock("../useNow", () => ({ useNow: () => Date.parse("2026-10-02T00:00:03Z") }));
vi.mock("@fressh/react-native-xtermjs-webview", async () => {
	const React = await import("react");
	return { XtermJsWebView: React.forwardRef((props: object, ref) => { React.useImperativeHandle(ref, () => ({ write: fixture.write, resize: fixture.webViewResize }), []); return React.createElement("XtermJsWebView", props); }) };
});
vi.mock("expo-router", () => ({ useNavigation: () => ({ setOptions: fixture.setOptions }), useRouter: () => ({ replace: fixture.replace, push: fixture.push }) }));
vi.mock("react-native", async () => {
	const React = await import("react");
	const host = (name: string) => ({ children, ...props }: { children?: React.ReactNode }) => React.createElement(name, props, children);
	return {
		ActivityIndicator: host("ActivityIndicator"), Alert: { alert: vi.fn() }, Keyboard: { dismiss: vi.fn() },
		Platform: { OS: "ios" }, Pressable: host("Pressable"), StyleSheet: { create: (styles: object) => styles },
		Text: host("Text"), View: host("View"), Animated: { Text: host("AnimatedText") },
	};
});
vi.mock("react-native-keyboard-controller", () => ({ useKeyboardState: (select: (state: object) => unknown) => select({ height: 0, isVisible: false }) }));
vi.mock("react-native-safe-area-context", () => ({ useSafeAreaInsets: () => ({ top: 0, bottom: 0, left: 0, right: 0 }) }));
vi.mock("../cloud/authStore", () => ({ useCloudAuth: () => ({ client: fixture.client, orgId: "org-1", sessionEpoch: fixture.sessionEpoch, isCurrentSession: fixture.isCurrent }) }));
vi.mock("../cloud/terminal", async (importOriginal) => ({ ...await importOriginal<typeof import("../cloud/terminal")>(), createCloudTerminal: (input: { onStatus: (status: string) => void; onError: (message: string) => void; onOutput: (bytes: Uint8Array) => void }) => {
	fixture.onStatus = input.onStatus;
	const disconnect = vi.fn(); const resize = vi.fn(); fixture.terminals.push({ input, disconnect, resize });
	return { connect: async () => {}, disconnect, resize, sendInput: fixture.sendInput, sendPrompt: fixture.sendPrompt };
} }));
vi.mock("../store", () => ({ useApp: () => ({ refreshSource: fixture.refreshSource, sourceFor: fixture.sourceFor }) }));
vi.mock("../haptics", () => ({ haptics: { error: vi.fn(), success: vi.fn(), tap: vi.fn() } }));
vi.mock("../chat/useChatViewLease", () => ({ useChatViewLease: vi.fn() }));
vi.mock("../cloud/useCloudInterfaceTransition", () => ({ useCloudInterfaceTransition: vi.fn(() => undefined) }));
vi.mock("../ThemeProvider", async () => {
	const { darkTheme, lightTheme } = await import("../theme");
	const theme = () => fixture.scheme === "light" ? lightTheme : darkTheme;
	return { useTheme: theme, useThemeState: () => ({ scheme: fixture.scheme }), useThemedStyles: (factory: (theme: typeof darkTheme) => unknown) => factory(theme()) };
});
vi.mock("../headerRightSwap", () => ({ resetHeaderRightForSwap: (_reset: () => void, ready: () => void) => { ready(); } }));
vi.mock("../voice/useVoiceInput", () => ({ useVoiceInput: () => ({ error: null }) }));
vi.mock("./Composer", async () => {
	const React = await import("react");
	return { Composer: (props: object) => React.createElement("Composer", props) };
});
vi.mock("./KeyRow", async () => {
	const React = await import("react");
	return { KeyRow: (props: object) => React.createElement("KeyRow", props) };
});

import { CloudTerminalSessionScreen } from "./CloudTerminalSessionScreen";
import { SessionStartupStatus } from "../chat/SessionStartup";

const cloudSource = { kind: "cloud" as const, id: "org-1" };
const session = (observedState: string, runtimeConnected: boolean, id = "session-1"): RouteSession => ({
	id, projectId: "project-1", status: null, mode: "tui", branch: null,
	issueId: null, issueTitle: null, userPrompt: null, displayName: "New session", summary: null,
	createdAt: "2026-10-02T00:00:00Z", lastActivityAt: "2026-10-02T00:00:00Z",
	runtimeConnected, cloud: { desiredState: "running", observedState },
});

let renderer: ReactTestRenderer | undefined;
const text = (value: string) => renderer!.root.findAll((node) => String(node.type) === "Text" && node.props.children === value);
const component = (name: string) => renderer!.root.findAll((node) => node.type === name);
const startupLabel = () => renderer!.root.findAll((node) => node.props.accessibilityLabel?.startsWith("Starting Cloud terminal:"))[0]?.props.accessibilityLabel;
type HeaderButtonProps = { accessibilityLabel?: string; disabled?: boolean; onPress: () => void | Promise<void>; children?: ReactNode };
function headerButton(label: string): ReactElement<HeaderButtonProps> | undefined {
	const find = (children: ReactNode): ReactElement<HeaderButtonProps> | undefined => {
		for (const child of Children.toArray(children)) {
			if (!isValidElement<HeaderButtonProps>(child)) continue;
			if (child.props.accessibilityLabel === label) return child;
			const nested = find(child.props.children);
			if (nested) return nested;
		}
	};
	return find(fixture.setOptions.mock.calls.map(([options]) => options.headerRight).filter(Boolean).at(-1)!());
}
const chatButton = () => headerButton("Open Chat UI") ?? headerButton("Return to session")!;
const alertChoice = (label: string) => vi.mocked(Alert.alert).mock.calls.at(-1)?.[2]?.find((button) => button.text === label);
function handoffControls(latest: Partial<CloudInterfaceStatus> = {}) {
	const status: CloudInterfaceStatus = { supported: true, targetMode: "chat", currentMode: "tui", runtimeConnected: true, sourceBusy: false, sourceWaitingForInput: false, ...latest };
	return {
		status, transition: status.transition, loading: false, starting: false, cancelling: false,
		start: vi.fn(async () => {}), refresh: vi.fn(async () => ({ ok: true as const, status })),
		cancel: vi.fn(async () => {}), acknowledgeNotice: vi.fn(async () => {}),
	} as unknown as CloudInterfaceControls;
}

beforeEach(() => {
	vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true);
	fixture.onStatus = null;
	fixture.sessionEpoch = 1;
	fixture.isCurrent.mockReset().mockReturnValue(true);
	fixture.scheme = "dark";
	fixture.terminals = [];
	fixture.write.mockClear(); fixture.webViewResize.mockClear(); fixture.replace.mockClear(); fixture.push.mockClear();
	fixture.setOptions.mockClear(); fixture.sendInput.mockClear(); fixture.sendPrompt.mockClear();
	fixture.source.resumeSession.mockReset(); fixture.source.switchToChat.mockClear(); fixture.refreshSource.mockClear();
	fixture.source.restoreSession.mockReset(); fixture.source.startInterfaceTransition.mockClear();
	vi.mocked(useChatViewLease).mockClear(); vi.mocked(useCloudInterfaceTransition).mockClear();
	fixture.source.retryStartup.mockReset().mockResolvedValue(undefined);
	fixture.source.workspace = fixture.workspace;
	fixture.source.preview = fixture.preview;
	fixture.preview.isCurrent.mockReset().mockReturnValue(true);
	fixture.workspace.isCurrent.mockReset().mockReturnValue(true);
	fixture.sourceFor.mockReset().mockReturnValue(fixture.source);
	vi.mocked(Alert.alert).mockClear();
	const error = console.error;
	vi.spyOn(console, "error").mockImplementation((...args) => {
		if (String(args[0]).startsWith("react-test-renderer is deprecated")) return;
		error(...args);
	});
});

afterEach(async () => {
	if (renderer) await act(async () => renderer?.unmount());
	renderer = undefined;
	vi.restoreAllMocks();
	vi.unstubAllGlobals();
});

describe("Cloud reviewer terminal", () => {
	const reviewerTerminalId = "00000000-0000-0000-0000-000000000091";
	it("opens only the existing reviewer with inert worker controls and navigation-only Session action", async () => {
		const controls = { ...handoffControls(), loading: true };
		await act(async () => { renderer = create(<CloudTerminalSessionScreen session={session("running", true)} source={cloudSource} reviewerTerminalId={reviewerTerminalId} interfaceSwitch={controls} showSpawnStartup />); });
		expect(fixture.terminals).toHaveLength(1);
		expect(fixture.terminals[0].input).toMatchObject({ kind: "agent", terminalId: reviewerTerminalId });
		expect(fixture.setOptions.mock.calls.at(-1)?.[0].title).toBe("Reviewer terminal");
		expect(vi.mocked(useCloudInterfaceTransition).mock.calls.every(([source]) => source === undefined)).toBe(true);
		expect(vi.mocked(useChatViewLease).mock.calls.every(([, , source]) => source === undefined)).toBe(true);
		expect(startupLabel()).toBeUndefined(); expect(component("Composer")).toHaveLength(1);
		await act(async () => { fixture.terminals[0].input.onStatus("ready"); });
		expect(text("Connected to reviewer terminal")).toHaveLength(1);
		expect(chatButton().props.accessibilityLabel).toBe("Return to session");
		await act(async () => { await chatButton().props.onPress(); });
		expect(fixture.replace).toHaveBeenCalledExactlyOnceWith({ pathname: "/session/[id]", params: { id: "session-1", source: "cloud", sourceId: "org-1" } });
		expect(controls.refresh).not.toHaveBeenCalled(); expect(controls.start).not.toHaveBeenCalled();
		expect(fixture.source.switchToChat).not.toHaveBeenCalled(); expect(fixture.source.startInterfaceTransition).not.toHaveBeenCalled();
		expect(fixture.source.resumeSession).not.toHaveBeenCalled(); expect(fixture.source.restoreSession).not.toHaveBeenCalled();
		expect(fixture.source.retryStartup).not.toHaveBeenCalled(); expect(fixture.refreshSource).not.toHaveBeenCalled();
	});
	it.each(["paused", "archived", "failed"])("does not recover a %s worker when opening its old reviewer", async (state) => {
		const current = session(state === "archived" ? "terminated" : state === "paused" ? "stopped" : "running", state === "failed");
		if (state === "paused") current.cloud!.desiredState = "paused";
		if (state === "archived" && !("projectName" in current)) current.isTerminated = true;
		if (state === "failed") current.cloud!.startupError = { code: "project_setup_failed", message: "Setup failed", at: "now" };
		await act(async () => { renderer = create(<CloudTerminalSessionScreen session={current} source={cloudSource} reviewerTerminalId={reviewerTerminalId} />); });
		expect(fixture.terminals).toHaveLength(0); expect(component("Composer")).toHaveLength(0);
		expect(text("Reviewer terminal unavailable")).toHaveLength(1);
		for (const label of ["Restore session", "Resume sandbox", "Retry startup"]) expect(renderer!.root.findAllByProps({ accessibilityLabel: label })).toHaveLength(0);
		expect(fixture.source.resumeSession).not.toHaveBeenCalled(); expect(fixture.source.restoreSession).not.toHaveBeenCalled(); expect(fixture.source.retryStartup).not.toHaveBeenCalled();
	});
	it.each(["", "worker-default", "../worker"])("rejects invalid reviewer ID %j without opening the worker terminal", async (invalid) => {
		await act(async () => { renderer = create(<CloudTerminalSessionScreen session={session("running", true)} source={cloudSource} reviewerTerminalId={invalid} />); });
		expect(fixture.terminals).toHaveLength(0);
		expect(text("Reviewer terminal unavailable")).toHaveLength(1);
	});
	it("replaces reviewer output and input ownership when the terminal changes", async () => {
		await act(async () => { renderer = create(<CloudTerminalSessionScreen session={session("running", true)} source={cloudSource} reviewerTerminalId={reviewerTerminalId} />); });
		const previous = fixture.terminals[0];
		await act(async () => component("Composer")[0].props.onChangeText("old reviewer draft"));
		await act(async () => { renderer!.update(<CloudTerminalSessionScreen session={session("running", true)} source={cloudSource} reviewerTerminalId="00000000-0000-0000-0000-000000000092" />); });
		expect(previous.disconnect).toHaveBeenCalledOnce();
		expect(fixture.terminals.at(-1)!.input.terminalId).toBe("00000000-0000-0000-0000-000000000092");
		expect(component("Composer")[0].props.value).toBe("");
		await act(async () => { component("XtermJsWebView")[0].props.onInitialized(); previous.input.onOutput(new Uint8Array([1])); previous.input.onError("old reviewer error"); });
		expect(fixture.write).not.toHaveBeenCalled(); expect(text("old reviewer error")).toHaveLength(0);
	});
});

describe("Cloud terminal callback ownership", () => {
	it.each(["account", "reviewer", "source"])("ignores a previous %s owner's Composer send and draft callbacks", async (change) => {
		const original = "00000000-0000-0000-0000-000000000091";
		await act(async () => { renderer = create(<CloudTerminalSessionScreen session={session("running", true)} source={cloudSource} reviewerTerminalId={original} />); });
		await act(async () => component("Composer")[0].props.onChangeText("previous owner private draft"));
		const oldComposer = component("Composer")[0].props;
		if (change === "account") fixture.sessionEpoch = 2;
		if (change === "source") fixture.sourceFor.mockReturnValue({ ...fixture.source });
		const next = change === "reviewer" ? "00000000-0000-0000-0000-000000000092" : original;
		await act(async () => { renderer!.update(<CloudTerminalSessionScreen session={session("running", true)} source={cloudSource} reviewerTerminalId={next} />); });
		await act(async () => component("Composer")[0].props.onChangeText("new owner draft"));
		await act(async () => { await oldComposer.onSend(); oldComposer.onChangeText("late old draft"); });
		expect(fixture.sendPrompt).not.toHaveBeenCalled();
		expect(component("Composer")[0].props.value).toBe("new owner draft");
	});

	it.each(["account", "reviewer", "source"])("ignores a previous %s owner's direct input and WebView layout/initialization", async (change) => {
		const original = "00000000-0000-0000-0000-000000000091";
		await act(async () => { renderer = create(<CloudTerminalSessionScreen session={session("running", true)} source={cloudSource} reviewerTerminalId={original} />); });
		const oldKey = component("KeyRow")[0].props.onKey;
		const oldWebView = component("XtermJsWebView")[0].props;
		if (change === "account") fixture.sessionEpoch = 2;
		if (change === "source") fixture.sourceFor.mockReturnValue({ ...fixture.source });
		const next = change === "reviewer" ? "00000000-0000-0000-0000-000000000092" : original;
		await act(async () => { renderer!.update(<CloudTerminalSessionScreen session={session("running", true)} source={cloudSource} reviewerTerminalId={next} />); });
		const terminal = fixture.terminals.at(-1)!;
		fixture.write.mockClear(); fixture.webViewResize.mockClear(); terminal.resize.mockClear();
		await act(async () => { terminal.input.onOutput(new Uint8Array([2])); oldWebView.onInitialized(); oldWebView.logger.log("FRESSH_DIMS 100 40"); oldWebView.onData("private-old-input"); oldKey("\r"); });
		expect(fixture.sendInput).not.toHaveBeenCalled();
		expect(terminal.resize).not.toHaveBeenCalled(); expect(fixture.webViewResize).not.toHaveBeenCalled();
		expect(fixture.write).not.toHaveBeenCalled();
		await act(async () => component("XtermJsWebView")[0].props.onInitialized());
		expect(fixture.write).toHaveBeenCalledExactlyOnceWith(new Uint8Array([2]));
	});

	it.each(["resume", "restore", "retry", "chat", "shell-return", "reviewer-return"])("retires a parked %s action when its account expires before rerender", async (action) => {
		const current = session(action === "resume" ? "stopped" : action === "restore" ? "terminated" : "running", action !== "resume" && action !== "restore");
		if (action === "resume") current.cloud!.desiredState = "paused";
		if (action === "restore" && !("projectName" in current)) current.isTerminated = true;
		if (action === "retry") current.cloud!.startupError = { code: "project_setup_failed", message: "failed", at: "now" };
		const controls = handoffControls();
		await act(async () => { renderer = create(<CloudTerminalSessionScreen session={current} source={cloudSource} kind={action === "shell-return" ? "workspace" : "agent"} reviewerTerminalId={action === "reviewer-return" ? "00000000-0000-0000-0000-000000000091" : undefined} interfaceSwitch={controls} />); });
		const label = action === "resume" ? "Resume sandbox" : action === "restore" ? "Restore session" : "Retry startup";
		const parked = ["resume", "restore", "retry"].includes(action) ? renderer!.root.findAllByProps({ accessibilityLabel: label })[0].props.onPress : chatButton().props.onPress;
		const resumes = fixture.source.resumeSession.mock.calls.length;
		fixture.isCurrent.mockReturnValue(false);
		await act(async () => { await parked(); });
		expect(fixture.source.resumeSession).toHaveBeenCalledTimes(resumes);
		expect(fixture.source.restoreSession).not.toHaveBeenCalled(); expect(fixture.source.retryStartup).not.toHaveBeenCalled();
		expect(controls.refresh).not.toHaveBeenCalled(); expect(controls.start).not.toHaveBeenCalled();
		expect(fixture.replace).not.toHaveBeenCalled(); expect(fixture.refreshSource).toHaveBeenCalledTimes(action === "shell-return" ? 1 : 0);
	});
});

describe("Cloud terminal browser entry", () => {
	it.each(["agent", "workspace"] as const)("opens the owning Cloud browser directly from the %s terminal without a handoff", async (kind) => {
		const controls = handoffControls();
		await act(async () => { renderer = create(<CloudTerminalSessionScreen session={session("running", true)} source={cloudSource} kind={kind} interfaceSwitch={controls} />); });
		const resumes = fixture.source.resumeSession.mock.calls.length;
		const browser = headerButton("Open Cloud browser");
		expect(browser).toBeDefined();
		await act(async () => browser!.props.onPress());
		expect(fixture.push).toHaveBeenCalledExactlyOnceWith({ pathname: "/preview/[id]", params: { id: "session-1", source: "cloud", sourceId: "org-1", scopeKey: "cloud-account-1", title: "New session" } });
		expect(fixture.source.resumeSession).toHaveBeenCalledTimes(resumes);
		expect(controls.refresh).not.toHaveBeenCalled(); expect(controls.start).not.toHaveBeenCalled(); expect(fixture.source.switchToChat).not.toHaveBeenCalled();
	});
	it.each(["absent", "retired", "reviewer"])("hides the Cloud browser action for an %s capability/view", async (state) => {
		if (state === "absent") fixture.source.preview = undefined;
		if (state === "retired") fixture.preview.isCurrent.mockReturnValue(false);
		await act(async () => { renderer = create(<CloudTerminalSessionScreen session={session("running", true)} source={cloudSource} reviewerTerminalId={state === "reviewer" ? "00000000-0000-0000-0000-000000000091" : undefined} />); });
		expect(headerButton("Open Cloud browser")).toBeUndefined(); expect(fixture.push).not.toHaveBeenCalled();
	});
	it.each(["account", "source", "capability", "expired"])("retires a parked Cloud browser action after its %s changes", async (change) => {
		await act(async () => { renderer = create(<CloudTerminalSessionScreen session={session("running", true)} source={cloudSource} />); });
		const browser = headerButton("Open Cloud browser");
		expect(browser).toBeDefined();
		if (change === "account") { fixture.sessionEpoch = 2; await act(async () => { renderer!.update(<CloudTerminalSessionScreen session={session("running", true)} source={cloudSource} />); }); }
		if (change === "source") fixture.sourceFor.mockReturnValue({ ...fixture.source });
		if (change === "capability") fixture.source.preview = { ...fixture.preview };
		if (change === "expired") fixture.preview.isCurrent.mockReturnValue(false);
		await act(async () => browser!.props.onPress());
		expect(fixture.push).not.toHaveBeenCalled();
	});
});

describe("Cloud terminal sizing", () => {
	it("keeps tiny bridge fits out of the grid cache and replays the last settled size after a worker reconnect", async () => {
		const currentSession = session("running", true);
		await act(async () => { renderer = create(<CloudTerminalSessionScreen session={currentSession} source={cloudSource} />); });
		const report = async (cols: number, rows: number) => act(async () => component("XtermJsWebView")[0].props.logger.log(`FRESSH_DIMS ${cols} ${rows}`));
		await report(2, 1);
		expect(fixture.terminals[0].resize).not.toHaveBeenCalled();
		expect(fixture.webViewResize).not.toHaveBeenCalled();
		await report(42, 23);
		await report(19, 4);
		await report(20, 3);
		expect(fixture.terminals[0].resize).toHaveBeenCalledExactlyOnceWith(42, 23);
		expect(fixture.webViewResize).toHaveBeenCalledExactlyOnceWith({ cols: 42, rows: 23 });
		await act(async () => { renderer!.update(<CloudTerminalSessionScreen session={{ ...currentSession, cloud: { ...currentSession.cloud, workerEpoch: 2 } }} source={cloudSource} />); });
		expect(fixture.terminals[1].resize).toHaveBeenCalledExactlyOnceWith(42, 23);
		await report(20, 4);
		expect(fixture.terminals[1].resize).toHaveBeenLastCalledWith(20, 4);
	});
});

describe("Cloud terminal Files entry", () => {
	it.each(["agent", "workspace"] as const)("opens the owning account's Files from the %s header", async (kind) => {
		const controls = handoffControls();
		await act(async () => { renderer = create(<CloudTerminalSessionScreen session={session("running", true)} source={cloudSource} kind={kind} interfaceSwitch={controls} />); });
		const resumes = fixture.source.resumeSession.mock.calls.length;
		const files = headerButton("Files");
		expect(files).toBeDefined();
		expect(chatButton().props.accessibilityLabel).toBe(kind === "workspace" ? "Return to session" : "Open Chat UI");
		await act(async () => { files!.props.onPress(); });

		expect(fixture.push).toHaveBeenCalledExactlyOnceWith({ pathname: "/files/[sessionId]", params: { sessionId: "session-1", source: "cloud", sourceId: "org-1", scopeKey: "cloud-account-1", title: "New session" } });
		expect(fixture.source.resumeSession).toHaveBeenCalledTimes(resumes);
		expect(fixture.source.switchToChat).not.toHaveBeenCalled();
		expect(controls.start).not.toHaveBeenCalled(); expect(controls.refresh).not.toHaveBeenCalled();
		expect(fixture.sendInput).not.toHaveBeenCalled(); expect(fixture.sendPrompt).not.toHaveBeenCalled();
	});

	it("keeps Files available while an interface handoff locks Chat", async () => {
		const controls = { ...handoffControls(), loading: true };
		await act(async () => { renderer = create(<CloudTerminalSessionScreen session={session("running", true)} source={cloudSource} interfaceSwitch={controls} />); });
		expect(chatButton().props.disabled).toBe(true);
		expect(headerButton("Files")).toBeDefined();
		await act(async () => { headerButton("Files")!.props.onPress(); });
		expect(fixture.push).toHaveBeenCalledOnce();
		expect(controls.start).not.toHaveBeenCalled();
	});

	it("hides Files when the workspace capability is absent or stale", async () => {
		fixture.source.workspace = undefined;
		await act(async () => { renderer = create(<CloudTerminalSessionScreen session={session("running", true)} source={cloudSource} />); });
		expect(headerButton("Files")).toBeUndefined();
		fixture.source.workspace = fixture.workspace;
		fixture.workspace.isCurrent.mockReturnValue(false);
		await act(async () => { renderer!.update(<CloudTerminalSessionScreen session={session("running", true)} source={cloudSource} />); });
		expect(headerButton("Files")).toBeUndefined();
	});

	it.each([{ kind: "cloud" as const, id: "org-other" }, { kind: "local" as const, id: "org-1" }])("hides Files for a mismatched source $kind/$id", async (source) => {
		await act(async () => { renderer = create(<CloudTerminalSessionScreen session={session("running", true)} source={source} />); });
		expect(headerButton("Files")).toBeUndefined();
	});

	it("rejects a parked Files press after its account expires without a rerender", async () => {
		await act(async () => { renderer = create(<CloudTerminalSessionScreen session={session("running", true)} source={cloudSource} />); });
		const files = headerButton("Files");
		expect(files).toBeDefined();
		fixture.workspace.isCurrent.mockReturnValue(false);
		await act(async () => { files!.props.onPress(); });
		expect(fixture.push).not.toHaveBeenCalled();
	});

	it("rejects a parked Files press after its source is replaced", async () => {
		await act(async () => { renderer = create(<CloudTerminalSessionScreen session={session("running", true)} source={cloudSource} />); });
		const files = headerButton("Files");
		expect(files).toBeDefined();
		fixture.sourceFor.mockReturnValue({ ...fixture.source });
		await act(async () => { files!.props.onPress(); });
		expect(fixture.push).not.toHaveBeenCalled();
	});

	it("rejects a parked Files press after the account epoch changes", async () => {
		await act(async () => { renderer = create(<CloudTerminalSessionScreen session={session("running", true)} source={cloudSource} />); });
		const files = headerButton("Files");
		expect(files).toBeDefined();
		fixture.sessionEpoch = 2;
		await act(async () => { renderer!.update(<CloudTerminalSessionScreen session={session("running", true)} source={cloudSource} />); });
		await act(async () => { files!.props.onPress(); });
		expect(fixture.push).not.toHaveBeenCalled();
	});

	it("rejects a parked Files press after routing to another organization", async () => {
		await act(async () => { renderer = create(<CloudTerminalSessionScreen session={session("running", true)} source={cloudSource} />); });
		const files = headerButton("Files");
		expect(files).toBeDefined();
		await act(async () => { renderer!.update(<CloudTerminalSessionScreen session={session("running", true)} source={{ kind: "cloud", id: "org-other" }} />); });
		await act(async () => { files!.props.onPress(); });
		expect(fixture.push).not.toHaveBeenCalled();
	});
});

describe("Cloud workspace shell", () => {
	it("resumes the workspace before acquiring its shell, without handing off the agent", async () => {
		let finish!: () => void;
		fixture.source.resumeSession.mockImplementationOnce(() => new Promise<void>((resolve) => { finish = resolve; }));
		const controls = handoffControls({ currentMode: "chat" });
		await act(async () => { renderer = create(<CloudTerminalSessionScreen session={session("running", true)} source={cloudSource} kind="workspace" interfaceSwitch={controls} />); });
		expect(fixture.terminals).toHaveLength(0);
		expect(fixture.source.resumeSession).toHaveBeenCalledExactlyOnceWith("session-1");
		await act(async () => { finish(); });
		expect(fixture.terminals[0].input.kind).toBe("workspace");
		expect(controls.refresh).not.toHaveBeenCalled(); expect(controls.start).not.toHaveBeenCalled();
		expect(fixture.source.switchToChat).not.toHaveBeenCalled();
	});
	it("returns to the selected session view by navigation even during an agent handoff", async () => {
		const controls = { ...handoffControls(), loading: true };
		await act(async () => { renderer = create(<CloudTerminalSessionScreen session={session("running", true)} source={cloudSource} kind="workspace" interfaceSwitch={controls} />); });
		expect(chatButton().props.accessibilityLabel).toBe("Return to session");
		expect(chatButton().props.disabled).toBeFalsy();
		await act(async () => { await chatButton().props.onPress(); });
		expect(fixture.replace).toHaveBeenCalledWith({ pathname: "/session/[id]", params: { id: "session-1", source: "cloud", sourceId: "org-1" } });
		expect(controls.refresh).not.toHaveBeenCalled(); expect(controls.start).not.toHaveBeenCalled();
		expect(component("Composer")).toHaveLength(1);
	});
	it("wakes a paused sandbox only once for an explicit workspace-shell entry", async () => {
		const paused = { ...session("stopped", false), cloud: { desiredState: "paused", observedState: "stopped" } };
		await act(async () => { renderer = create(<CloudTerminalSessionScreen session={paused} source={cloudSource} kind="workspace" />); });
		await act(async () => { renderer!.update(<CloudTerminalSessionScreen session={{ ...paused }} source={cloudSource} kind="workspace" />); });
		expect(fixture.source.resumeSession).toHaveBeenCalledExactlyOnceWith("session-1");
		expect(fixture.terminals).toHaveLength(0);
		await act(async () => { renderer!.update(<CloudTerminalSessionScreen session={session("running", true)} source={cloudSource} kind="workspace" />); });
		expect(fixture.terminals[0].input.kind).toBe("workspace");
		expect(fixture.source.resumeSession).toHaveBeenCalledOnce();
		expect(startupLabel()).toBeUndefined();
	});
	it("does not restore an archived worker just to open its workspace shell", async () => {
		await act(async () => { renderer = create(<CloudTerminalSessionScreen session={{ ...session("terminated", false), isTerminated: true }} source={cloudSource} kind="workspace" />); });
		expect(fixture.source.resumeSession).not.toHaveBeenCalled();
		expect(fixture.terminals).toHaveLength(0);
	});
	it("does not open an orchestrator workspace shell through a direct link", async () => {
		const orchestrator = { ...session("running", true), projectName: "Test project" };
		await act(async () => { renderer = create(<CloudTerminalSessionScreen session={orchestrator} source={cloudSource} kind="workspace" />); });
		expect(fixture.source.resumeSession).not.toHaveBeenCalled();
		expect(fixture.terminals).toHaveLength(0);
		expect(text("Workspace shell unavailable")).toHaveLength(1);
	});
	it("shows a recoverable shell error instead of attaching after a failed resume", async () => {
		fixture.source.resumeSession.mockRejectedValueOnce(new Error("Network lost"));
		await act(async () => { renderer = create(<CloudTerminalSessionScreen session={session("running", true)} source={cloudSource} kind="workspace" />); });
		expect(fixture.terminals).toHaveLength(0);
		expect(text("Network lost")).toHaveLength(1);
		await act(async () => { renderer!.root.findAll((node) => String(node.type) === "Pressable" && node.props.accessibilityLabel === "Retry workspace shell")[0].props.onPress(); });
		expect(fixture.source.resumeSession).toHaveBeenCalledTimes(2);
		expect(fixture.terminals[0].input.kind).toBe("workspace");
	});
	it("replaces the agent transport and draft when opening a workspace shell", async () => {
		await act(async () => { renderer = create(<CloudTerminalSessionScreen session={session("running", true)} source={cloudSource} />); });
		const old = fixture.terminals[0];
		await act(async () => { component("Composer")[0].props.onChangeText("agent draft"); });
		await act(async () => { renderer!.update(<CloudTerminalSessionScreen session={session("running", true)} source={cloudSource} kind="workspace" />); });
		expect(old.disconnect).toHaveBeenCalledOnce();
		expect(component("Composer")[0].props.value).toBe("");
		act(() => { component("XtermJsWebView")[0].props.onInitialized(); old.input.onOutput(new Uint8Array([1])); });
		expect(fixture.write).not.toHaveBeenCalled();
	});
	it("keeps an unsent shell command across a worker reconnect without resuming again", async () => {
		await act(async () => { renderer = create(<CloudTerminalSessionScreen session={session("running", true)} source={cloudSource} kind="workspace" />); });
		await act(async () => { component("Composer")[0].props.onChangeText("git status"); });
		await act(async () => { renderer!.update(<CloudTerminalSessionScreen session={{ ...session("running", true), cloud: { desiredState: "running", observedState: "running", workerEpoch: 2 } }} source={cloudSource} kind="workspace" />); });
		expect(component("Composer")[0].props.value).toBe("git status");
		expect(fixture.source.resumeSession).toHaveBeenCalledOnce();
	});
	it("ignores a previous worker's late resume and opens only the newly routed shell", async () => {
		let finish!: () => void;
		fixture.source.resumeSession.mockImplementationOnce(() => new Promise<void>((resolve) => { finish = resolve; }));
		await act(async () => { renderer = create(<CloudTerminalSessionScreen session={session("running", true)} source={cloudSource} kind="workspace" />); });
		await act(async () => { renderer!.update(<CloudTerminalSessionScreen session={session("running", true, "session-2")} source={cloudSource} kind="workspace" />); });
		const refreshes = fixture.refreshSource.mock.calls.length;
		expect(fixture.terminals).toHaveLength(1);
		await act(async () => { finish(); });
		expect(fixture.refreshSource).toHaveBeenCalledTimes(refreshes);
		expect(fixture.terminals).toHaveLength(1);
	});
	it("does not resume or attach a shell for a different signed-in organization", async () => {
		await act(async () => { renderer = create(<CloudTerminalSessionScreen session={session("running", true)} source={{ kind: "cloud", id: "org-other" }} kind="workspace" />); });
		expect(fixture.source.resumeSession).not.toHaveBeenCalled();
		expect(fixture.terminals).toHaveLength(0);
	});
	it("disconnects an old account's shell and waits for the new account's resume", async () => {
		await act(async () => { renderer = create(<CloudTerminalSessionScreen session={session("running", true)} source={cloudSource} kind="workspace" />); });
		const old = fixture.terminals[0];
		let finish!: () => void;
		fixture.source.resumeSession.mockImplementationOnce(() => new Promise<void>((resolve) => { finish = resolve; }));
		fixture.sessionEpoch = 2;
		await act(async () => { renderer!.update(<CloudTerminalSessionScreen session={session("running", true)} source={cloudSource} kind="workspace" />); });
		expect(old.disconnect).toHaveBeenCalledOnce();
		expect(fixture.terminals).toHaveLength(1);
		await act(async () => { finish(); });
		expect(fixture.terminals).toHaveLength(2);
		act(() => { component("XtermJsWebView")[0].props.onInitialized(); old.input.onOutput(new Uint8Array([1])); old.input.onError("Old account error"); });
		expect(fixture.write).not.toHaveBeenCalled(); expect(text("Old account error")).toHaveLength(0);
	});
	it("waits for a replacement source to resume before requesting another shell ticket", async () => {
		await act(async () => { renderer = create(<CloudTerminalSessionScreen session={session("running", true)} source={cloudSource} kind="workspace" />); });
		let finish!: () => void;
		const replacement = { ...fixture.source, resumeSession: vi.fn(() => new Promise<void>((resolve) => { finish = resolve; })) };
		fixture.sourceFor.mockReturnValue(replacement);
		await act(async () => { renderer!.update(<CloudTerminalSessionScreen session={session("running", true)} source={cloudSource} kind="workspace" />); });
		expect(fixture.terminals).toHaveLength(1);
		await act(async () => { finish(); });
		expect(fixture.terminals).toHaveLength(2);
	});
	it("does not clear a new shell draft when a previous agent send finishes", async () => {
		let finish!: (sent: boolean) => void;
		fixture.sendPrompt.mockImplementationOnce(() => new Promise<boolean>((resolve) => { finish = resolve; }));
		await act(async () => { renderer = create(<CloudTerminalSessionScreen session={session("running", true)} source={cloudSource} />); });
		await act(async () => { component("Composer")[0].props.onChangeText("agent input"); });
		let sent!: Promise<void>;
		await act(async () => { sent = component("Composer")[0].props.onSend(); });
		await act(async () => { renderer!.update(<CloudTerminalSessionScreen session={session("running", true)} source={cloudSource} kind="workspace" />); });
		await act(async () => { component("Composer")[0].props.onChangeText("shell input"); });
		await act(async () => { finish(true); await sent; });
		expect(component("Composer")[0].props.value).toBe("shell input");
	});
	it("allows another command after a pause interrupts an in-flight terminal send", async () => {
		let finish!: (sent: boolean) => void;
		fixture.sendPrompt.mockImplementationOnce(() => new Promise<boolean>((resolve) => { finish = resolve; }));
		await act(async () => { renderer = create(<CloudTerminalSessionScreen session={session("running", true)} source={cloudSource} />); });
		await act(async () => { component("Composer")[0].props.onChangeText("first command"); });
		let sent!: Promise<void>;
		await act(async () => { sent = component("Composer")[0].props.onSend(); });
		const paused = { ...session("stopped", false), cloud: { desiredState: "paused", observedState: "stopped" } };
		await act(async () => { renderer!.update(<CloudTerminalSessionScreen session={paused} source={cloudSource} />); });
		await act(async () => { finish(true); await sent; });
		await act(async () => { renderer!.update(<CloudTerminalSessionScreen session={session("running", true)} source={cloudSource} />); });
		expect(component("Composer")[0].props.sending).toBe(false);
		await act(async () => { component("Composer")[0].props.onChangeText("next command"); });
		await act(async () => { await component("Composer")[0].props.onSend(); });
		expect(fixture.sendPrompt).toHaveBeenLastCalledWith("next command");
	});
});

describe("Cloud terminal to Chat policy", () => {
	it.each(["stopped", "offline"])("opens already-selected Chat for a %s worker without switching or resuming", async (observedState) => {
		const controls = handoffControls({ currentMode: "chat", runtimeConnected: false });
		const offline = { ...session(observedState, false), mode: "chat" as const, cloud: { desiredState: "paused", observedState } };
		await act(async () => { renderer = create(<CloudTerminalSessionScreen session={offline} source={cloudSource} interfaceSwitch={controls} />); });
		await act(async () => { await chatButton().props.onPress(); });
		expect(controls.refresh).toHaveBeenCalledOnce();
		expect(fixture.replace).toHaveBeenCalledWith({ pathname: "/session/[id]", params: { id: "session-1", source: "cloud", sourceId: "org-1" } });
		expect(controls.start).not.toHaveBeenCalled();
		expect(fixture.source.resumeSession).not.toHaveBeenCalled();
		expect(Alert.alert).not.toHaveBeenCalled();
	});
	it("opens already-selected Chat even when another handoff is unsupported", async () => {
		const controls = handoffControls({ currentMode: "chat", runtimeConnected: false, supported: false });
		await act(async () => { renderer = create(<CloudTerminalSessionScreen session={session("stopped", false)} source={cloudSource} interfaceSwitch={controls} />); });
		await act(async () => { await chatButton().props.onPress(); });
		expect(fixture.replace).toHaveBeenCalledOnce();
		expect(controls.start).not.toHaveBeenCalled(); expect(Alert.alert).not.toHaveBeenCalled();
	});
	it.each(["draining", "target_starting", "recovery_required"] as const)("does not open already-selected Chat while a fresh %s fence is active", async (phase) => {
		const controls = handoffControls({ currentMode: "chat", runtimeConnected: false, transition: { id: "h1", sessionId: "session-1", sourceMode: "tui", targetMode: "chat", policy: "drain", phase, createdAt: "now", updatedAt: "now" } });
		controls.status = { ...controls.status!, transition: undefined }; controls.transition = undefined;
		await act(async () => { renderer = create(<CloudTerminalSessionScreen session={session("stopped", false)} source={cloudSource} interfaceSwitch={controls} />); });
		await act(async () => { await chatButton().props.onPress(); });
		expect(controls.refresh).toHaveBeenCalledOnce();
		expect(fixture.replace).not.toHaveBeenCalled(); expect(controls.start).not.toHaveBeenCalled(); expect(Alert.alert).not.toHaveBeenCalled();
	});
	it("still rejects an unsupported actual Terminal to Chat handoff", async () => {
		const controls = handoffControls({ supported: false, reason: "Agent does not support Chat handoff" });
		await act(async () => { renderer = create(<CloudTerminalSessionScreen session={session("running", true)} source={cloudSource} interfaceSwitch={controls} />); });
		await act(async () => { await chatButton().props.onPress(); });
		expect(Alert.alert).toHaveBeenCalledWith("Chat unavailable", "Agent does not support Chat handoff");
		expect(fixture.replace).not.toHaveBeenCalled(); expect(controls.start).not.toHaveBeenCalled();
	});
	it("checks current activity and stops an idle terminal instead of waiting for another Stop hook", async () => {
		const controls = handoffControls();
		await act(async () => { renderer = create(<CloudTerminalSessionScreen session={session("running", true)} source={cloudSource} interfaceSwitch={controls} />); });
		await act(async () => { await chatButton().props.onPress(); });
		expect(controls.refresh).toHaveBeenCalledOnce();
		expect(controls.start).toHaveBeenCalledWith("chat", "interrupt");
		expect(Alert.alert).not.toHaveBeenCalled();
	});
	it.each(["drain", "interrupt"] as const)("asks before switching a freshly busy terminal with %s policy", async (policy) => {
		const controls = handoffControls({ sourceBusy: true });
		controls.status = { ...controls.status!, sourceBusy: false };
		await act(async () => { renderer = create(<CloudTerminalSessionScreen session={session("running", true)} source={cloudSource} interfaceSwitch={controls} />); });
		await act(async () => { await chatButton().props.onPress(); });
		expect(controls.start).not.toHaveBeenCalled();
		expect(alertChoice("Finish, then switch")).toBeDefined();
		await act(async () => { await alertChoice(policy === "drain" ? "Finish, then switch" : "Stop and switch")!.onPress!(); });
		expect(controls.start).toHaveBeenCalledWith("chat", policy);
	});
	it("explains that draining a waiting terminal needs the user's answer", async () => {
		const controls = handoffControls({ sourceBusy: true, sourceWaitingForInput: true });
		await act(async () => { renderer = create(<CloudTerminalSessionScreen session={session("running", true)} source={cloudSource} interfaceSwitch={controls} />); });
		await act(async () => { await chatButton().props.onPress(); });
		expect(String(vi.mocked(Alert.alert).mock.calls.at(-1)?.[1])).toMatch(/waiting for your input/i);
		await act(async () => { alertChoice("Keep Terminal")!.onPress?.(); });
		expect(controls.start).not.toHaveBeenCalled();
	});
	it("requires explicit stop confirmation if current activity cannot be read", async () => {
		const controls = handoffControls();
		vi.mocked(controls.refresh).mockResolvedValueOnce({ ok: false, error: "Network lost", status: undefined });
		await act(async () => { renderer = create(<CloudTerminalSessionScreen session={session("running", true)} source={cloudSource} interfaceSwitch={controls} />); });
		await act(async () => { await chatButton().props.onPress(); });
		expect(controls.start).not.toHaveBeenCalled();
		expect(alertChoice("Finish, then switch")).toBeUndefined();
		await act(async () => { await alertChoice("Stop and switch")!.onPress!(); });
		expect(controls.start).toHaveBeenCalledWith("chat", "interrupt");
	});
	it("does not restart a switch another client has already begun", async () => {
		const controls = handoffControls({ transition: { id: "h1", sessionId: "session-1", sourceMode: "tui", targetMode: "chat", policy: "drain", phase: "draining", createdAt: "now", updatedAt: "now" } });
		controls.status = { ...controls.status!, transition: undefined }; controls.transition = undefined;
		await act(async () => { renderer = create(<CloudTerminalSessionScreen session={session("running", true)} source={cloudSource} interfaceSwitch={controls} />); });
		await act(async () => { await chatButton().props.onPress(); });
		expect(controls.start).not.toHaveBeenCalled(); expect(Alert.alert).not.toHaveBeenCalled();
	});
	it("does not apply an old confirmation to a different routed worker", async () => {
		const controls = handoffControls({ sourceBusy: true });
		await act(async () => { renderer = create(<CloudTerminalSessionScreen session={session("running", true)} source={cloudSource} interfaceSwitch={controls} />); });
		await act(async () => { await chatButton().props.onPress(); });
		const stop = alertChoice("Stop and switch")!;
		expect(stop).toBeDefined();
		await act(async () => { renderer!.update(<CloudTerminalSessionScreen session={session("running", true, "session-2")} source={cloudSource} interfaceSwitch={handoffControls()} />); });
		await act(async () => { await stop.onPress!(); });
		expect(controls.start).not.toHaveBeenCalled();
	});
	it("does not act on a late activity check for the previous worker", async () => {
		const controls = handoffControls();
		let resolve!: (value: Awaited<ReturnType<CloudInterfaceControls["refresh"]>>) => void;
		vi.mocked(controls.refresh).mockImplementationOnce(() => new Promise((done) => { resolve = done; }));
		await act(async () => { renderer = create(<CloudTerminalSessionScreen session={session("running", true)} source={cloudSource} interfaceSwitch={controls} />); });
		let request!: Promise<void>;
			await act(async () => { request = Promise.resolve(chatButton().props.onPress()); });
		await act(async () => { renderer!.update(<CloudTerminalSessionScreen session={session("running", true, "session-2")} source={cloudSource} interfaceSwitch={handoffControls()} />); });
		await act(async () => { resolve({ ok: true, status: controls.status! }); await request; });
		expect(controls.start).not.toHaveBeenCalled(); expect(Alert.alert).not.toHaveBeenCalled();
	});
	it("does not duplicate the fresh activity check on two immediate taps", async () => {
		const controls = handoffControls();
		let resolve!: (value: Awaited<ReturnType<CloudInterfaceControls["refresh"]>>) => void;
		vi.mocked(controls.refresh).mockImplementationOnce(() => new Promise((done) => { resolve = done; }));
		await act(async () => { renderer = create(<CloudTerminalSessionScreen session={session("running", true)} source={cloudSource} interfaceSwitch={controls} />); });
		let requests!: Promise<void>[];
			await act(async () => { const press = chatButton().props.onPress; requests = [Promise.resolve(press()), Promise.resolve(press())]; });
		expect(controls.refresh).toHaveBeenCalledOnce();
		await act(async () => { resolve({ ok: true, status: controls.status! }); await Promise.all(requests); });
		expect(controls.start).toHaveBeenCalledOnce();
	});
	it("does not show a stop dialog for an aborted activity check", async () => {
		const controls = handoffControls();
		vi.mocked(controls.refresh).mockResolvedValueOnce(undefined);
		await act(async () => { renderer = create(<CloudTerminalSessionScreen session={session("running", true)} source={cloudSource} interfaceSwitch={controls} />); });
		await act(async () => { await chatButton().props.onPress(); });
		expect(Alert.alert).not.toHaveBeenCalled(); expect(controls.start).not.toHaveBeenCalled();
	});
	it("does not discard text typed while checking current activity", async () => {
		const controls = handoffControls();
		let resolve!: (value: Awaited<ReturnType<CloudInterfaceControls["refresh"]>>) => void;
		vi.mocked(controls.refresh).mockImplementationOnce(() => new Promise((done) => { resolve = done; }));
		await act(async () => { renderer = create(<CloudTerminalSessionScreen session={session("running", true)} source={cloudSource} interfaceSwitch={controls} />); });
		let request!: Promise<void>;
			await act(async () => { request = Promise.resolve(chatButton().props.onPress()); });
		await act(async () => { component("Composer")[0].props.onChangeText("typed during request"); });
		await act(async () => { resolve({ ok: true, status: controls.status! }); await request; });
		expect(controls.start).not.toHaveBeenCalled(); expect(alertChoice("Stop and switch")).toBeDefined();
		expect(component("Composer")[0].props.value).toBe("typed during request");
	});
	it("does not discard an unsent mobile terminal draft without confirmation", async () => {
		const controls = handoffControls();
		await act(async () => { renderer = create(<CloudTerminalSessionScreen session={session("running", true)} source={cloudSource} interfaceSwitch={controls} />); });
		await act(async () => { component("Composer")[0].props.onChangeText("my draft"); });
		await act(async () => { await chatButton().props.onPress(); });
		expect(controls.start).not.toHaveBeenCalled();
		expect(component("Composer")[0].props.value).toBe("my draft");
		expect(alertChoice("Stop and switch")).toBeDefined();
	});
});

describe("Cloud terminal startup", () => {
	it("resumes a paused sandbox before retrying its retained workspace error", async () => {
		const paused = session("stopped", false);
		paused.cloud = { ...paused.cloud, desiredState: "paused", startupError: { code: "project_setup_failed", message: "Setup failed before the pause", at: "now" } };
		await act(async () => { renderer = create(<CloudTerminalSessionScreen session={paused} source={cloudSource} />); });
		await act(async () => renderer!.root.findAllByProps({ accessibilityLabel: "Resume sandbox" })[0].props.onPress());
		expect(fixture.source.resumeSession).toHaveBeenCalledExactlyOnceWith("session-1");
		expect(fixture.source.retryStartup).not.toHaveBeenCalled();
	});
	it.each(["repository_checkout_failed", "working_directory_unusable", "project_setup_failed"])("shows %s and offers explicit retry for a connected worker", async (code) => {
		const failed = session("running", true);
		failed.cloud!.startupError = { code, message: "Workspace preparation failed", at: "now" };
		await act(async () => { renderer = create(<CloudTerminalSessionScreen session={failed} source={cloudSource} showSpawnStartup />); });
		expect(text("Workspace preparation failed")).toHaveLength(1);
		expect(startupLabel()).toBeUndefined();
		expect(fixture.terminals).toHaveLength(0);
		expect(component("Composer")).toHaveLength(0);
		expect(fixture.source.retryStartup).not.toHaveBeenCalled();
		const retry = renderer!.root.findAllByProps({ accessibilityLabel: "Retry startup" })[0];
		await act(async () => retry.props.onPress());
		expect(fixture.source.retryStartup).toHaveBeenCalledExactlyOnceWith("session-1");
		expect(fixture.source.resumeSession).not.toHaveBeenCalled();
	});
	it.each([false, true])("does not acquire a terminal for a finally stopped runtime with error=%s", async (hasError) => {
		const stopped = session("terminated", false);
		if (hasError) stopped.cloud!.startupError = { code: "bootstrap_failed", message: "Worker could not start", at: "now" };
		await act(async () => { renderer = create(<CloudTerminalSessionScreen session={stopped} source={cloudSource} />); });
		expect(fixture.terminals).toHaveLength(0); expect(component("Composer")).toHaveLength(0);
	});
	it("keeps the same terminal and input available during a cancellable drain", async () => {
		await act(async () => { renderer = create(<CloudTerminalSessionScreen session={session("running", true)} source={cloudSource} />); });
		const terminal = fixture.terminals[0];
		act(() => { component("XtermJsWebView")[0].props.onInitialized(); terminal.input.onStatus("ready"); });
		const controls = handoffControls({ transition: { id: "h1", sessionId: "session-1", sourceMode: "tui", targetMode: "chat", policy: "drain", phase: "draining", createdAt: "now", updatedAt: "now" } });
		await act(async () => { renderer!.update(<CloudTerminalSessionScreen session={session("running", true)} source={cloudSource} interfaceSwitch={controls} />); });
		expect(terminal.disconnect).not.toHaveBeenCalled(); expect(fixture.terminals).toHaveLength(1);
		expect(component("Composer")).toHaveLength(1); expect(component("KeyRow")).toHaveLength(1);
		act(() => { terminal.input.onOutput(new Uint8Array([3])); component("KeyRow")[0].props.onKey("\r"); });
		expect(fixture.write).toHaveBeenCalledWith(new Uint8Array([3])); expect(fixture.sendInput).toHaveBeenCalledWith("\r");
		await act(async () => { component("Composer")[0].props.onChangeText("yes"); });
		await act(async () => { await component("Composer")[0].props.onSend(); });
		expect(fixture.sendPrompt).toHaveBeenCalledWith("yes");
	});
	it("keeps the terminal while submitting a drain, before Cloud accepts it", async () => {
		await act(async () => { renderer = create(<CloudTerminalSessionScreen session={session("running", true)} source={cloudSource} />); });
		const terminal = fixture.terminals[0];
		const controls = { ...handoffControls(), starting: true, startingPolicy: "drain" } as CloudInterfaceControls;
		await act(async () => { renderer!.update(<CloudTerminalSessionScreen session={session("running", true)} source={cloudSource} interfaceSwitch={controls} />); });
		expect(terminal.disconnect).not.toHaveBeenCalled(); expect(component("Composer")).toHaveLength(1);
	});
	it.each(["interrupt", "source_stopping"])("fences terminal input once %s begins", async (phase) => {
		await act(async () => { renderer = create(<CloudTerminalSessionScreen session={session("running", true)} source={cloudSource} />); });
		const terminal = fixture.terminals[0];
		const controls = handoffControls({ transition: { id: "h1", sessionId: "session-1", sourceMode: "tui", targetMode: "chat", policy: phase === "interrupt" ? "interrupt" : "drain", phase: phase === "interrupt" ? "draining" : "source_stopping", createdAt: "now", updatedAt: "now" } });
		await act(async () => { renderer!.update(<CloudTerminalSessionScreen session={session("running", true)} source={cloudSource} interfaceSwitch={controls} />); });
		expect(terminal.disconnect).toHaveBeenCalledOnce(); expect(component("Composer")).toHaveLength(0);
		expect(component("KeyRow")).toHaveLength(0);
	});
	it("writes output after a cancelled handoff without waiting for a retained WebView to initialize again", async () => {
		await act(async () => { renderer = create(<CloudTerminalSessionScreen session={session("running", true)} source={cloudSource} />); });
		act(() => { component("XtermJsWebView")[0].props.onInitialized(); fixture.terminals[0].input.onOutput(new Uint8Array([1])); });
		const transition = { id: "h1", sourceMode: "tui", targetMode: "chat", phase: "draining" };
		const controls = { loading: false, status: { supported: true, targetMode: "chat", currentMode: "tui", transition }, transition } as unknown as import("../cloud/useCloudInterfaceTransition").CloudInterfaceControls;
		await act(async () => { renderer!.update(<CloudTerminalSessionScreen session={session("running", true)} source={cloudSource} interfaceSwitch={controls} />); });
		const resumed = { ...controls, status: { ...controls.status!, transition: { ...controls.status!.transition!, phase: "cancelled" as const } }, transition: { ...controls.transition!, phase: "cancelled" as const } };
		await act(async () => { renderer!.update(<CloudTerminalSessionScreen session={session("running", true)} source={cloudSource} interfaceSwitch={resumed} />); });
		act(() => fixture.terminals.at(-1)!.input.onOutput(new Uint8Array([2])));
		expect(fixture.write.mock.calls.map(([bytes]) => [...bytes])).toEqual([[1], [2]]);
	});
	it("retains terminal history but disconnects and hides input when handoff needs recovery", async () => {
		await act(async () => { renderer = create(<CloudTerminalSessionScreen session={session("running", true)} source={cloudSource} />); });
		const old = fixture.terminals[0];
		const controls = { loading: false, starting: false, status: { supported: true, targetMode: "chat", currentMode: "chat", transition: { id: "h1", sourceMode: "tui", targetMode: "chat", phase: "recovery_required", errorDetail: "Target stopped; recovery needed" } }, transition: { id: "h1", sourceMode: "tui", targetMode: "chat", phase: "recovery_required", errorDetail: "Target stopped; recovery needed" }, refresh: vi.fn(), start: vi.fn(), cancel: vi.fn(), acknowledgeNotice: vi.fn() } as unknown as import("../cloud/useCloudInterfaceTransition").CloudInterfaceControls;
		await act(async () => { renderer!.update(<CloudTerminalSessionScreen session={session("running", true)} source={cloudSource} interfaceSwitch={controls} />); });
		expect(old.disconnect).toHaveBeenCalledOnce(); expect(component("Composer")).toHaveLength(0);
		expect(component("XtermJsWebView")).toHaveLength(1); expect(text("Target stopped; recovery needed")).toHaveLength(1);
		expect(fixture.replace).not.toHaveBeenCalled();
	});
	it("replaces the transport on worker epoch change and ignores old ready callbacks", async () => {
		const initial = { ...session("running", true), cloud: { ...session("running", true).cloud, workerEpoch: 1 } };
		await act(async () => { renderer = create(<CloudTerminalSessionScreen session={initial} source={cloudSource} />); });
		const old = fixture.terminals[0];
		await act(async () => renderer!.update(<CloudTerminalSessionScreen session={{ ...initial, cloud: { ...initial.cloud, workerEpoch: 2 } }} source={cloudSource} />));
		expect(old.disconnect).toHaveBeenCalledOnce(); expect(fixture.terminals).toHaveLength(2);
		await act(async () => { old.input.onStatus("ready"); });
		expect(text("Connected to Cloud terminal")).toHaveLength(0);
		await act(async () => { fixture.terminals[1].input.onStatus("ready"); });
		expect(text("Connected to Cloud terminal")).toHaveLength(1);
	});
	it("does not acquire a terminal while a generic provider sandbox is paused", async () => {
		const paused = { ...session("stopped", false), cloud: { sandboxProvider: "ecs", desiredState: "paused", observedState: "stopped" } };
		await act(async () => { renderer = create(<CloudTerminalSessionScreen session={paused} source={cloudSource} />); });
		expect(fixture.terminals).toHaveLength(0); expect(text("Paused").length).toBeGreaterThan(0);
	});
	it("presents Restore for archive without connecting it", async () => {
		await act(async () => { renderer = create(<CloudTerminalSessionScreen session={{ ...session("terminated", false), isTerminated: true }} source={cloudSource} />); });
		expect(fixture.terminals).toHaveLength(0);
		expect(renderer!.root.findAll((node) => String(node.type) === "Pressable" && node.props.accessibilityLabel === "Restore session")).toHaveLength(1);
	});
	it("opens an existing session without replaying the workspace startup loader", async () => {
		await act(async () => { renderer = create(<CloudTerminalSessionScreen session={session("running", true)} source={cloudSource} />); });
		expect(startupLabel()).toBeUndefined();
		expect(text("Connecting to Cloud terminal…")).toHaveLength(1);
		expect(component("Composer")).toHaveLength(1);
		await act(async () => { fixture.onStatus?.("ready"); });
		await act(async () => { renderer?.unmount(); });
		await act(async () => { renderer = create(<CloudTerminalSessionScreen session={session("running", true)} source={cloudSource} />); });
		expect(startupLabel()).toBeUndefined();
		expect(component("Composer")).toHaveLength(1);
	});

	it("shows the desktop's four startup steps and hides input while the workspace provisions", async () => {
		await act(async () => { renderer = create(<CloudTerminalSessionScreen session={session("provisioning", false)} source={cloudSource} showSpawnStartup />); });
		for (const label of ["Creating workspace", "Connecting to worker", "Preparing repository and agent", "Connecting terminal"]) {
			expect(text(label)).toHaveLength(1);
		}
		expect(component("Composer")).toHaveLength(0);
		expect(component("KeyRow")).toHaveLength(0);
		expect(startupLabel()).toBe("Starting Cloud terminal: Creating workspace");
		await act(async () => { renderer!.update(<CloudTerminalSessionScreen session={session("bootstrapping", false)} source={cloudSource} showSpawnStartup />); });
		expect(startupLabel()).toBe("Starting Cloud terminal: Connecting to worker");
		await act(async () => { renderer!.update(<CloudTerminalSessionScreen session={session("running", true)} source={cloudSource} showSpawnStartup />); });
		expect(startupLabel()).toBe("Starting Cloud terminal: Preparing repository and agent");
		await act(async () => { fixture.onStatus?.("attaching"); });
		expect(startupLabel()).toBe("Starting Cloud terminal: Connecting terminal");
	});
	it.each(["dark", "light"] as const)("uses the Local session header and compact checklist with Cloud labels in %s mode", async (scheme) => {
		fixture.scheme = scheme;
		let local!: ReactTestRenderer;
		await act(async () => { local = create(<SessionStartupStatus startup={{ failed: false, agentName: "Codex", namingBranch: false, steps: [
			{ id: "fetch", status: "done" }, { id: "worktree", status: "running" }, { id: "agent", status: "pending" },
		] }} />); });
		try {
			await act(async () => { renderer = create(<CloudTerminalSessionScreen session={session("bootstrapping", false)} source={cloudSource} showSpawnStartup />); });
			expect(text("Setting up session")).toHaveLength(1);
			const localRows = local.root.findAll((node) => String(node.type) === "View" && node.props.testID?.startsWith("session-startup-step-"));
			const cloudRows = renderer!.root.findAll((node) => String(node.type) === "View" && node.props.testID?.startsWith("session-startup-step-"));
			expect(cloudRows.map((node) => node.props.accessibilityLabel)).toEqual([
				"Creating workspace, Done", "Connecting to worker, In progress", "Preparing repository and agent, Pending", "Connecting terminal, Pending",
			]);
			for (const [index, status] of ["done", "running", "pending"].entries()) {
				const localRow = localRows.find((node) => node.props.testID === `session-startup-step-${status}`)!;
				const cloudRow = cloudRows[index];
				expect(cloudRow.props.style).toEqual(localRow.props.style);
				const icon = (row: typeof localRow) => row.findAll((node) => String(node.type) === "Feather").map((node) => ({ name: node.props.name, size: node.props.size, color: node.props.color }));
				expect(icon(cloudRow)).toEqual(icon(localRow));
				const label = (row: typeof localRow) => row.findAll((node) => String(node.type) === "Text")[0].props.style;
				expect(label(cloudRow)).toEqual(label(localRow));
			}
			expect(cloudRows.flatMap((row) => row.findAll((node) => String(node.type) === "Text")).map((node) => node.props.children)).not.toContain("Create worktree");
		} finally { await act(async () => local.unmount()); }
	});
	it("preserves the Local checklist's branch, timing, failure and retry behavior", async () => {
		const retry = vi.fn();
		await act(async () => { renderer = create(<SessionStartupStatus onRetry={retry} startup={{ failed: true, error: "Agent failed to start", agentName: "Codex", namingBranch: false, branch: "ao/local/root", steps: [
			{ id: "worktree", status: "done", startedAt: "2026-10-02T00:00:00Z", endedAt: "2026-10-02T00:00:01Z" },
			{ id: "agent", status: "running", startedAt: "2026-10-02T00:00:01Z" },
		] }} />); });
		expect(text("Session setup failed")).toHaveLength(1);
		expect(text("ao/local/root")).toHaveLength(1); expect(text("1.0s")).toHaveLength(1);
		expect(renderer!.root.findAll((node) => String(node.type) === "View" && node.props.accessibilityLabel === "Start Codex, Failed")).toHaveLength(1);
		expect(text("Agent failed to start")).toHaveLength(1);
		expect(text("Your messages are saved here and will be sent if you retry.")).toHaveLength(1);
		await act(async () => renderer!.root.findAll((node) => String(node.type) === "Pressable")[0].props.onPress());
		expect(retry).toHaveBeenCalledOnce();
	});

	it("reveals the terminal after the first ready frame and keeps it visible during a later reconnect", async () => {
		await act(async () => { renderer = create(<CloudTerminalSessionScreen session={session("running", true)} source={cloudSource} showSpawnStartup />); });
		expect(text("Connecting terminal")).toHaveLength(1);
		await act(async () => { fixture.onStatus?.("ready"); });
		expect(component("Composer")).toHaveLength(1);
		expect(text("Creating workspace")).toHaveLength(0);
		await act(async () => { fixture.onStatus?.("disconnected"); });
		expect(component("Composer")).toHaveLength(1);
		expect(text("Creating workspace")).toHaveLength(0);
		expect(text("Reconnecting to Cloud terminal…")).toHaveLength(1);
	});

	it("does not treat the previous session's ready state as a new session's startup", async () => {
		await act(async () => { renderer = create(<CloudTerminalSessionScreen session={session("running", true)} source={cloudSource} showSpawnStartup />); });
		await act(async () => { fixture.onStatus?.("ready"); });
		expect(component("Composer")).toHaveLength(1);
		await act(async () => { renderer!.update(<CloudTerminalSessionScreen session={session("provisioning", false, "session-2")} source={cloudSource} showSpawnStartup />); });
		expect(text("Creating workspace")).toHaveLength(1);
		expect(component("Composer")).toHaveLength(0);
	});
});
