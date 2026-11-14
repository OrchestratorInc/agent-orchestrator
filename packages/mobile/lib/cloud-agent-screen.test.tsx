import { act, create, type ReactTestInstance, type ReactTestRenderer } from "react-test-renderer";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true });

const fixtures = vi.hoisted(() => ({
	platform: { OS: "android", Version: 36 },
	prepare: vi.fn(),
	cancelled: vi.fn(),
	theme: new Proxy({}, { get: () => "#000000" }),
}));

vi.mock("expo-router", async () => {
	const React = await import("react");
	const host = (name: string) => ({ children, ...props }: { children?: React.ReactNode }) => React.createElement(name, props, children);
	return {
		Stack: Object.assign(host("StackScreenHost"), {
			Screen: host("StackScreen"),
			Toolbar: Object.assign(host("StackToolbar"), { Button: host("StackToolbarButton"), View: host("StackToolbarView") }),
		}),
		useLocalSearchParams: () => ({ agent: "codex" }),
		useNavigation: () => ({ isFocused: () => true }),
		useRouter: () => ({ back: vi.fn(), canGoBack: () => true }),
	};
});
vi.mock("react-native", () => ({
	Alert: { alert: vi.fn() },
	Platform: fixtures.platform,
	StyleSheet: { create: (styles: object) => styles },
	View: "View",
}));
vi.mock("react-native-keyboard-controller", () => ({ KeyboardAvoidingView: "KeyboardAvoidingView" }));
vi.mock("expo-clipboard", () => ({ setStringAsync: vi.fn() }));
vi.mock("expo-crypto", () => ({ getRandomBytes: vi.fn(), digestStringAsync: vi.fn(), CryptoDigestAlgorithm: {}, CryptoEncoding: {} }));
// The rows stay hosts so the page's wiring can be read off their props.
vi.mock("./SettingsForm", () => ({
	SettingsForm: "SettingsForm",
	SettingsSection: "SettingsSection",
	SettingsButtonRow: "SettingsButtonRow",
	SettingsValueRow: "SettingsValueRow",
}));
vi.mock("./cloud/CloudProvider", () => ({
	useCloud: () => ({
		status: "ready",
		error: null,
		client: {},
		account: { id: "account-1" },
		connections: [],
		connectionsLoaded: true,
		refresh: vi.fn(async () => {}),
		refreshConnections: vi.fn(async () => {}),
	}),
}));
vi.mock("./cloud/browserSignIn", () => ({
	BrowserSignInUnavailable: class extends Error {},
	canSignInWithBrowser: () => true,
	openSignedInBrowser: vi.fn(),
	prepareBrowserSignIn: fixtures.prepare,
}));
vi.mock("./cloud/agentLogin", () => ({
	CLAUDE_LOOPBACK_PATH: "/callback",
	CODEX_LOOPBACK_PATH: "/auth/callback",
	CODEX_LOOPBACK_PORT: 1455,
	claudeLoopbackRedirect: vi.fn(),
	exchangeClaudeCode: vi.fn(),
	finishClaudeLogin: vi.fn(),
	finishCodexBrowserLogin: vi.fn(),
	finishCodexLogin: vi.fn(),
	startClaudeLogin: vi.fn(),
	startCodexBrowserLogin: vi.fn(async () => ({ state: "state", authorizeUrl: "https://auth.openai.com/authorize" })),
	startCodexLogin: vi.fn(async () => ({ deviceAuthId: "device-1", userCode: "ABCD-1234", intervalMs: 5000, verificationUrl: "https://auth.openai.com/codex/device" })),
	// Approval never arrives in these tests: the page keeps waiting.
	waitForCodexApproval: vi.fn(() => new Promise(() => {})),
}));
vi.mock("./cloud/components", () => ({ CloudSignIn: "CloudSignIn" }));
vi.mock("./cloud/CredentialFieldSection", () => ({ CredentialFieldSection: "CredentialFieldSection" }));
vi.mock("./cloud/errors", () => ({ cloudErrorMessage: (cause: unknown) => String(cause) }));
vi.mock("./formFields", () => ({ FormPasswordField: "FormPasswordField", FormTextField: "FormTextField" }));
vi.mock("./haptics", () => ({ haptics: { error: vi.fn(), select: vi.fn(), success: vi.fn(), tap: vi.fn(), warning: vi.fn() } }));
vi.mock("./Spinner", () => ({ Spinner: "Spinner" }));
vi.mock("./ThemeProvider", () => ({ useTheme: () => fixtures.theme }));
vi.mock("./Toast", () => ({ ToastHost: "ToastHost" }));
vi.mock("./toolbarIcon", () => ({ toolbarIcon: (name: string) => name }));
vi.mock("./ui", () => ({ Button: "Button", EmptyState: "EmptyState" }));
vi.mock("./useHeaderOverlap", () => ({ useGlassBarInset: () => 0 }));
vi.mock("./useToast", () => ({ useToast: () => ({ show: vi.fn() }) }));
vi.mock("./backNavigation", () => ({ backOr: vi.fn() }));
vi.mock("./RouteErrorBoundary", () => ({ RouteErrorBoundary: () => null }));

import CloudAgentScreen from "../app/settings/cloud/agent/[agent]";

/** A listener whose run() waits for the browser until cancel() aborts it, as the real one does. */
function browserSession() {
	let abort: (() => void) | undefined;
	return {
		port: 1455,
		run: vi.fn(() => new Promise<string>((_resolve, reject) => {
			abort = () => reject(Object.assign(new Error("Sign-in cancelled."), { name: "AbortError" }));
		})),
		cancel: vi.fn(() => {
			fixtures.cancelled();
			abort?.();
		}),
	};
}

const row = (tree: ReactTestRenderer, testID: string): ReactTestInstance => tree.root.find((node) => node.props.testID === testID && typeof node.type === "string");
const settle = async () => {
	for (let i = 0; i < 5; i++) await act(async () => { await Promise.resolve(); });
};

describe("Cloud coding agent: browser sign-in", () => {
	let tree: ReactTestRenderer;

	beforeEach(() => {
		fixtures.prepare.mockReset();
		fixtures.prepare.mockImplementation(async () => browserSession());
		fixtures.cancelled.mockReset();
	});
	afterEach(() => { act(() => tree?.unmount()); });

	it("offers every way to connect again after a cancelled sign-in", async () => {
		await act(async () => { tree = create(<CloudAgentScreen />); });
		await act(async () => { row(tree, "cloud-agent-method-codex-login").props.onPress(); });
		await settle();
		expect(row(tree, "cloud-agent-waiting").props.label).toBe("Waiting for the browser");

		await act(async () => { row(tree, "cloud-agent-cancel").props.onPress(); });
		await settle();

		expect(fixtures.cancelled).toHaveBeenCalled();
		for (const id of ["cloud-agent-method-codex-login", "cloud-agent-method-openai-api-key", "cloud-agent-method-codex-auth-json"]) {
			expect(row(tree, id).props.disabled).toBe(false);
		}

		// A second sign-in starts a listener of its own.
		await act(async () => { row(tree, "cloud-agent-method-codex-login").props.onPress(); });
		await settle();
		expect(fixtures.prepare).toHaveBeenCalledTimes(2);
		expect(row(tree, "cloud-agent-waiting").props.label).toBe("Waiting for the browser");
	});

	it("switches a waiting browser sign-in to a device code", async () => {
		await act(async () => { tree = create(<CloudAgentScreen />); });
		await act(async () => { row(tree, "cloud-agent-method-codex-login").props.onPress(); });
		await settle();

		await act(async () => { row(tree, "cloud-agent-use-code").props.onPress(); });
		await settle();

		expect(fixtures.cancelled).toHaveBeenCalled();
		expect(row(tree, "cloud-agent-device-code").props.value).toBe("ABCD-1234");
		expect(row(tree, "cloud-agent-waiting").props.label).toBe("Waiting for approval");
		// Cancelling the code brings back the choices, all enabled.
		await act(async () => { row(tree, "cloud-agent-cancel").props.onPress(); });
		await settle();
		expect(row(tree, "cloud-agent-method-codex-login").props.disabled).toBe(false);
	});
});
