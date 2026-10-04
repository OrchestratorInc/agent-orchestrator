import { act, create, type ReactTestRenderer } from "react-test-renderer";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { RouteSession } from "./sessionRoute";

const fixture = vi.hoisted(() => {
	const source = { switchToChat: vi.fn(), resumeSession: vi.fn() };
	return {
		source,
		client: {},
		refreshSource: vi.fn(async () => {}),
		setOptions: vi.fn(),
		replace: vi.fn(),
		onStatus: null as null | ((status: string) => void),
	};
});

vi.mock("@expo/vector-icons", async () => {
	const React = await import("react");
	return { Feather: (props: object) => React.createElement("Feather", props) };
});
vi.mock("@fressh/react-native-xtermjs-webview", async () => {
	const React = await import("react");
	return { XtermJsWebView: (props: object) => React.createElement("XtermJsWebView", props) };
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
vi.mock("../cloud/terminal", () => ({ createCloudTerminal: (input: { onStatus: (status: string) => void }) => {
	fixture.onStatus = input.onStatus;
	return { connect: async () => {}, disconnect: () => {}, resize: () => {}, sendInput: () => false, sendPrompt: async () => false };
} }));
vi.mock("../store", () => ({ useApp: () => ({ refreshSource: fixture.refreshSource, sourceFor: () => fixture.source }) }));
vi.mock("../haptics", () => ({ haptics: { error: vi.fn(), success: vi.fn() } }));
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

beforeEach(() => {
	vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true);
	fixture.onStatus = null;
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

describe("Cloud terminal startup", () => {
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
