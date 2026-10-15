import { act, create, type ReactTestRenderer } from "react-test-renderer";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { Alert } from "react-native";
import type { RouteSession } from "./sessionRoute";
import type { CloudInterfaceControls } from "../cloud/useCloudInterfaceTransition";
import type { CloudInterfaceStatus } from "../cloud/interfaceTransition";

const fixture = vi.hoisted(() => {
	const source = { switchToChat: vi.fn(), resumeSession: vi.fn(), startInterfaceTransition: vi.fn() };
	return {
		source,
		client: {},
		refreshSource: vi.fn(async () => {}),
		setOptions: vi.fn(),
		replace: vi.fn(),
		write: vi.fn(),
		sendInput: vi.fn(() => true),
		sendPrompt: vi.fn(async () => true),
		onStatus: null as null | ((status: string) => void),
		terminals: [] as Array<{ input: { onStatus: (status: string) => void; onError: (message: string) => void; onOutput: (bytes: Uint8Array) => void }; disconnect: ReturnType<typeof vi.fn> }>,
	};
});

vi.mock("@expo/vector-icons", async () => {
	const React = await import("react");
	return { Feather: (props: object) => React.createElement("Feather", props) };
});
vi.mock("@fressh/react-native-xtermjs-webview", async () => {
	const React = await import("react");
	return { XtermJsWebView: React.forwardRef((props: object, ref) => { React.useImperativeHandle(ref, () => ({ write: fixture.write, resize: () => {} }), []); return React.createElement("XtermJsWebView", props); }) };
});
vi.mock("expo-router", () => ({ useNavigation: () => ({ setOptions: fixture.setOptions }), useRouter: () => ({ replace: fixture.replace }) }));
vi.mock("react-native", async () => {
	const React = await import("react");
	const host = (name: string) => ({ children, ...props }: { children?: React.ReactNode }) => React.createElement(name, props, children);
	return {
		ActivityIndicator: host("ActivityIndicator"), Alert: { alert: vi.fn() }, Keyboard: { dismiss: vi.fn() },
		Platform: { OS: "ios" }, Pressable: host("Pressable"), StyleSheet: { create: (styles: object) => styles },
		Text: host("Text"), View: host("View"),
	};
});
vi.mock("react-native-keyboard-controller", () => ({ useKeyboardState: (select: (state: object) => unknown) => select({ height: 0, isVisible: false }) }));
vi.mock("react-native-safe-area-context", () => ({ useSafeAreaInsets: () => ({ top: 0, bottom: 0, left: 0, right: 0 }) }));
vi.mock("../cloud/authStore", () => ({ useCloudAuth: () => ({ client: fixture.client, orgId: "org-1" }) }));
vi.mock("../cloud/terminal", () => ({ createCloudTerminal: (input: { onStatus: (status: string) => void; onError: (message: string) => void; onOutput: (bytes: Uint8Array) => void }) => {
	fixture.onStatus = input.onStatus;
	const disconnect = vi.fn(); fixture.terminals.push({ input, disconnect });
	return { connect: async () => {}, disconnect, resize: () => {}, sendInput: fixture.sendInput, sendPrompt: fixture.sendPrompt };
} }));
vi.mock("../store", () => ({ useApp: () => ({ refreshSource: fixture.refreshSource, sourceFor: () => fixture.source }) }));
vi.mock("../haptics", () => ({ haptics: { error: vi.fn(), success: vi.fn() } }));
vi.mock("../chat/useChatViewLease", () => ({ useChatViewLease: vi.fn() }));
vi.mock("../cloud/useCloudInterfaceTransition", () => ({ useCloudInterfaceTransition: () => undefined }));
vi.mock("../ThemeProvider", async () => {
	const { darkTheme } = await import("../theme");
	return { useTheme: () => darkTheme, useThemeState: () => ({ scheme: "dark" }), useThemedStyles: (factory: (theme: typeof darkTheme) => unknown) => factory(darkTheme) };
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
const chatButton = () => fixture.setOptions.mock.calls.map(([options]) => options.headerRight).filter(Boolean).at(-1)!();
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
	fixture.terminals = [];
	fixture.write.mockClear(); fixture.replace.mockClear();
	fixture.setOptions.mockClear(); fixture.sendInput.mockClear(); fixture.sendPrompt.mockClear();
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
		await act(async () => { request = chatButton().props.onPress(); });
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
		await act(async () => { const press = chatButton().props.onPress; requests = [press(), press()]; });
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
		await act(async () => { request = chatButton().props.onPress(); });
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
