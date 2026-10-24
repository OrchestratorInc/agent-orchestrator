import { CloudApiError, type AgentLogin, type CloudClient, type RedactedProviderConnection } from "@aoagents/cloud-client";
import { act, create, type ReactTestRenderer } from "react-test-renderer";
import { afterEach, beforeEach, expect, it, vi } from "vitest";

const native = vi.hoisted(() => ({
	open: vi.fn(), state: "active", listener: null as ((state: string) => void) | null,
	alert: vi.fn(),
	push: vi.fn(), auth: null as unknown, uuid: vi.fn(), platform: { OS: "ios" },
	focus: null as (() => void) | null, blur: null as (() => void) | null,
}));
vi.mock("@react-native-async-storage/async-storage", () => ({ default: {
	getItem: async () => null, getAllKeys: async () => [], setItem: vi.fn(), removeItem: vi.fn(),
} }));
vi.mock("@expo/ui", () => ({ Host: "Host", Picker: "Picker", Checkbox: "Checkbox", Column: "Column" }));
vi.mock("@expo/vector-icons", () => ({ Feather: "Feather" }));
vi.mock("react-native-safe-area-context", () => ({ useSafeAreaInsets: () => ({ bottom: 0 }) }));
vi.mock("../icons", () => ({ Feather: "Feather" }));
vi.mock("../haptics", () => ({ haptics: { tap: vi.fn(), select: vi.fn() } }));
vi.mock("../openGitHub", () => ({ openGitHub: vi.fn() }));
vi.mock("../store", () => ({ useApp: () => ({ refreshSource: vi.fn() }) }));
vi.mock("react-native", () => ({
	ActivityIndicator: "ActivityIndicator", Text: "Text", View: "View", TextInput: "TextInput", ScrollView: "ScrollView",
	Pressable: "Pressable", Keyboard: { dismiss: vi.fn() }, BackHandler: { addEventListener: () => ({ remove: vi.fn() }) },
	StyleSheet: { create: (styles: object) => styles, hairlineWidth: 1 },
	Platform: native.platform,
	useWindowDimensions: () => ({ width: 390, height: 844 }),
	Linking: { openURL: native.open },
	Alert: { alert: native.alert },
	AppState: { get currentState() { return native.state; }, addEventListener: (_event: string, listener: (state: string) => void) => { native.listener = listener; return { remove: () => { native.listener = null; } }; } },
}));
vi.mock("expo-router", async () => {
	const React = await import("react");
	return { useRouter: () => ({ push: native.push }), useFocusEffect: (effect: () => (() => void)) => React.useEffect(() => {
		native.focus = () => { native.blur = effect(); };
		native.focus();
		return () => { native.blur?.(); native.focus = null; native.blur = null; };
	}, [effect]) };
});
vi.mock("expo-crypto", () => ({ randomUUID: native.uuid }));
vi.mock("../ThemeProvider", async () => {
	const { darkTheme } = await import("../theme");
	return { useTheme: () => darkTheme, useThemeState: () => ({ scheme: "dark" }), useThemedStyles: (factory: (theme: typeof darkTheme) => unknown) => factory(darkTheme) };
});
vi.mock("../AgentLogo", () => ({ AgentLogo: "AgentLogo" }));
vi.mock("../ui", () => ({ Button: "Button", HeaderIconButton: "HeaderIconButton" }));
vi.mock("./authStore", () => ({ useCloudAuth: () => native.auth }));
vi.mock("../RouteErrorBoundary", () => ({ RouteErrorBoundary: "RouteErrorBoundary", SheetErrorBoundary: "SheetErrorBoundary" }));

import { CloudAgentConnections, type CloudAgentConnectionsProps } from "./CloudAgentConnections";
import CloudAgentsScreen from "../../app/settings/cloud-agents";
import CreateCloudProject from "../../app/create-project";
import { cloudProjectSetupCache, type ProjectGitHubSetup } from "./projectSetupCache";

let renderer: ReactTestRenderer | undefined;
const client = {
	getAgentLoginCapabilities: vi.fn(), listUserProviderConnections: vi.fn(), startAgentLogin: vi.fn(),
	getAgentLogin: vi.fn(), submitAgentLoginCode: vi.fn(), cancelAgentLogin: vi.fn(), putUserAgentConnection: vi.fn(),
	deleteUserAgentConnection: vi.fn(), deleteSession: vi.fn(), resumeSession: vi.fn(),
};
const waiting = (overrides: Partial<AgentLogin> = {}): AgentLogin => ({
	id: "login-1", harness: "codex", status: "waiting", expiresAt: "2099-10-23T20:00:00Z",
	authorizationUrl: "https://auth.openai.com/codex/device", userCode: "ABCD-EFGH", ...overrides,
});
const connection = (provider: string, validationState = "valid") => ({
	id: provider, provider, label: "default", config: {}, validationState, createdAt: "2026-10-23T00:00:00Z", updatedAt: "2026-10-23T00:00:00Z",
}) as RedactedProviderConnection;
const current = () => true;
const props = (overrides: Partial<CloudAgentConnectionsProps> = {}): CloudAgentConnectionsProps => ({
	client: client as unknown as CloudClient, orgId: "org-1", ownerKey: '["https://cloud.test","account-1",1]', isCurrent: current, ...overrides,
});
const text = () => renderer!.root.findAllByType("Text" as never).map(node => node.props.children).flat(Infinity).join(" ");
const button = (title: string) => renderer!.root.findByProps({ title });
const press = async (title: string) => act(async () => { await button(title).props.onPress(); });
const confirmDisconnect = async () => act(async () => {
	await native.alert.mock.calls.at(-1)?.[2]?.find((choice: { text: string }) => choice.text === "Disconnect")?.onPress();
});
async function mount(overrides: Partial<CloudAgentConnectionsProps> = {}) {
	await act(async () => { renderer = create(<CloudAgentConnections {...props(overrides)} />); });
}
function deferred<T>() {
	let resolve!: (value: T) => void;
	const promise = new Promise<T>(done => { resolve = done; });
	return { promise, resolve };
}
beforeEach(() => {
	vi.useFakeTimers(); vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true);
	vi.spyOn(console, "error").mockImplementation(() => {});
	native.state = "active"; native.open.mockReset().mockResolvedValue(undefined); native.push.mockReset();
	native.uuid.mockReset().mockReturnValue("login-request");
	native.platform.OS = "ios";
	native.alert.mockReset();
	client.getAgentLoginCapabilities.mockReset().mockResolvedValue({ harnesses: ["claude-code", "codex"] });
	client.listUserProviderConnections.mockReset().mockResolvedValue([connection("codex")]);
	client.startAgentLogin.mockReset().mockResolvedValue({ login: waiting() });
	client.getAgentLogin.mockReset().mockResolvedValue({ login: waiting() });
	client.submitAgentLoginCode.mockReset().mockResolvedValue({ login: waiting({ harness: "claude-code", status: "connecting", requiresCode: false }) });
	client.cancelAgentLogin.mockReset().mockResolvedValue(undefined);
	client.putUserAgentConnection.mockReset().mockResolvedValue({ providerConnection: connection("claude-code") });
	client.deleteUserAgentConnection.mockReset().mockResolvedValue(undefined);
	client.deleteSession.mockReset(); client.resumeSession.mockReset();
});

it("requires explicit confirmation to disconnect and keeps the connection when cancelled", async () => {
	await mount(); await press("Disconnect Codex");
	expect(native.alert).toHaveBeenCalledWith("Disconnect Codex?", expect.stringMatching(/personal.*credential/i), expect.any(Array));
	expect(client.deleteUserAgentConnection).not.toHaveBeenCalled();
	await act(async () => native.alert.mock.calls.at(-1)?.[2]?.find((choice: { text: string }) => choice.text === "Cancel")?.onPress?.());
	expect(button("Disconnect Codex")).toBeDefined();
	expect(text()).toContain("Connected");
	expect(client.deleteUserAgentConnection).not.toHaveBeenCalled();
});

it("disconnects only the saved personal agent, invalidates its account cache, and refreshes readiness", async () => {
	const projectKey = '["https://cloud.test","account-1","org-1",1]';
	const otherKey = '["https://cloud.test","account-2","org-1",1]';
	cloudProjectSetupCache.setConnections(projectKey, [connection("codex")]);
	cloudProjectSetupCache.setConnections(otherKey, [connection("codex")]);
	const onChanged = vi.fn(); await mount({ onChanged });
	client.listUserProviderConnections.mockResolvedValue([]);
	await press("Disconnect Codex"); await confirmDisconnect();
	expect(client.deleteUserAgentConnection).toHaveBeenCalledWith("codex", { signal: expect.any(AbortSignal) });
	expect(cloudProjectSetupCache.read(projectKey).connections).toBeUndefined();
	expect(cloudProjectSetupCache.read(otherKey).connections?.fresh).toBe(true);
	expect(client.listUserProviderConnections).toHaveBeenCalledTimes(2);
	expect(onChanged).toHaveBeenCalledOnce();
	expect(renderer!.root.findAllByProps({ title: "Disconnect Codex" })).toHaveLength(0);
	expect(button("Connect Codex")).toBeDefined();
	expect(text()).toContain("Codex disconnected");
	expect(client.deleteSession).not.toHaveBeenCalled(); expect(client.resumeSession).not.toHaveBeenCalled();
	cloudProjectSetupCache.invalidate(otherKey, "connections");
});

it("retains a saved connection and cache when disconnect fails without exposing provider details", async () => {
	const key = '["https://cloud.test","account-1","org-1",1]';
	cloudProjectSetupCache.setConnections(key, [connection("codex")]);
	client.deleteUserAgentConnection.mockRejectedValue(new Error("private-provider-secret HTTP 500 Reference: secret-id"));
	const onChanged = vi.fn(); await mount({ onChanged });
	await press("Disconnect Codex"); await confirmDisconnect();
	expect(button("Disconnect Codex")).toBeDefined(); expect(text()).toContain("Connected");
	expect(text()).toContain("Could not disconnect");
	expect(text()).not.toMatch(/private-provider-secret|HTTP|Reference:|secret-id/);
	expect(cloudProjectSetupCache.read(key).connections?.fresh).toBe(true);
	expect(onChanged).not.toHaveBeenCalled(); expect(client.listUserProviderConnections).toHaveBeenCalledOnce();
	cloudProjectSetupCache.invalidate(key, "connections");
});

it("settles an aborted metadata refresh when disconnect fails and keeps refresh usable", async () => {
	const key = '["https://cloud.test","account-1","org-1",1]';
	cloudProjectSetupCache.setConnections(key, [connection("codex")]);
	const onChanged = vi.fn(); await mount({ onChanged });
	const pending = deferred<RedactedProviderConnection[]>();
	client.listUserProviderConnections.mockReturnValueOnce(pending.promise);
	await press("Refresh connected agents");
	const signal = client.listUserProviderConnections.mock.calls[1][0].signal;
	expect(renderer!.root.findAllByProps({ accessibilityLabel: "Loading connected agents" })).toHaveLength(1);
	client.deleteUserAgentConnection.mockRejectedValue(new Error("delete failed"));
	await press("Disconnect Codex"); await confirmDisconnect();
	expect(signal.aborted).toBe(true);
	expect(button("Disconnect Codex")).toBeDefined(); expect(text()).toContain("Connected");
	expect(cloudProjectSetupCache.read(key).connections?.value).toEqual([connection("codex")]);
	expect(onChanged).not.toHaveBeenCalled();
	expect(renderer!.root.findAllByProps({ accessibilityLabel: "Loading connected agents" })).toHaveLength(0);
	expect(button("Refresh connected agents").props.disabled).toBe(false);
	await act(async () => pending.resolve([]));
	expect(button("Disconnect Codex")).toBeDefined();
	await press("Refresh connected agents");
	expect(client.listUserProviderConnections).toHaveBeenCalledTimes(3);
	expect(button("Refresh connected agents").props.disabled).toBe(false);
	expect(renderer!.root.findAllByProps({ accessibilityLabel: "Loading connected agents" })).toHaveLength(0);
	cloudProjectSetupCache.invalidate(key, "connections");
});

it.each(["pending", "invalid", "expired"])("allows a saved %s personal connection to be disconnected", async validationState => {
	client.listUserProviderConnections.mockResolvedValue([connection("codex", validationState), { ...connection("cursor"), label: "shared" }]);
	await mount();
	expect(text()).not.toMatch(/\bConnected\b/);
	expect(text()).toMatch(/Validation pending|Connection needs attention|Connection expired/);
	expect(renderer!.root.findAllByProps({ title: "Disconnect Cursor" })).toHaveLength(0);
	await press("Disconnect Codex"); await confirmDisconnect();
	expect(client.deleteUserAgentConnection).toHaveBeenCalledOnce();
});

it("fences a disconnect confirmation after its account is replaced", async () => {
	await mount(); await press("Disconnect Codex");
	await act(async () => renderer!.update(<CloudAgentConnections {...props({ ownerKey: "new-account" })} />));
	await confirmDisconnect();
	expect(client.deleteUserAgentConnection).not.toHaveBeenCalled();
});

it("aborts a disconnect on account replacement and discards its late response", async () => {
	const pending = deferred<void>(); client.deleteUserAgentConnection.mockReturnValue(pending.promise);
	const onChanged = vi.fn(); await mount({ onChanged }); await press("Disconnect Codex");
	await act(async () => { native.alert.mock.calls.at(-1)?.[2]?.find((choice: { text: string }) => choice.text === "Disconnect")?.onPress(); });
	const signal = client.deleteUserAgentConnection.mock.calls[0][1].signal;
	client.listUserProviderConnections.mockResolvedValue([connection("claude-code")]);
	await act(async () => renderer!.update(<CloudAgentConnections {...props({ ownerKey: "new-account", onChanged })} />));
	expect(signal.aborted).toBe(true);
	await act(async () => pending.resolve());
	expect(onChanged).not.toHaveBeenCalled(); expect(text()).not.toContain("Codex disconnected");
	expect(button("Disconnect Claude Code")).toBeDefined();
	expect(client.listUserProviderConnections).toHaveBeenCalledTimes(2);
});
afterEach(async () => {
	if (renderer) await act(async () => renderer!.unmount());
	renderer = undefined; vi.useRealTimers(); vi.restoreAllMocks(); vi.unstubAllGlobals();
});

it.each([404, 503])("keeps existing personal readiness when native login returns %s and offers secure API key fallback", async (status) => {
	client.getAgentLoginCapabilities.mockRejectedValue({ status });
	await mount();
	expect(text()).toContain("Connected");
	expect(text()).toMatch(/Browser sign-in is unavailable/);
	expect(renderer!.root.findAllByProps({ title: "Reconnect Codex" })).toHaveLength(0);
	await press("Use Claude Code API key");
	const input = renderer!.root.findByProps({ accessibilityLabel: "Claude Code API key" });
	expect(input.props.secureTextEntry).toBe(true);
	await act(async () => input.props.onChangeText("secret-value"));
	const onChanged = vi.fn();
	await act(async () => renderer!.update(<CloudAgentConnections {...props({ onChanged })} />));
	await press("Save API key");
	expect(client.putUserAgentConnection).toHaveBeenCalledWith("claude-code", { credentialType: "api_key", secret: "secret-value" }, { signal: expect.any(AbortSignal) });
	expect(text()).not.toContain("secret-value");
	expect(renderer!.root.findAllByType("TextInput" as never).some(node => node.props.value === "secret-value")).toBe(false);
	expect(onChanged).toHaveBeenCalledOnce();
});

it("does not offer browser sign-in before capabilities arrive", async () => {
	const pending = deferred<{ harnesses: string[] }>(); client.getAgentLoginCapabilities.mockReturnValue(pending.promise);
	await mount();
	expect(renderer!.root.findAllByProps({ title: "Connect Claude Code" })).toHaveLength(0);
	expect(client.startAgentLogin).not.toHaveBeenCalled();
	await act(async () => pending.resolve({ harnesses: ["codex"] }));
	expect(renderer!.root.findAllByProps({ title: "Connect Claude Code" })).toHaveLength(0);
	expect(button("Reconnect Codex")).toBeDefined();
});

it.each([
	[403, "forbidden", /permission/i],
	[409, "SANDBOX_QUOTA_EXCEEDED", /capacity.*limit/i],
	[422, "invalid_credential", /credential.*invalid or expired/i],
	[503, "agent_login_unavailable", /unavailable/i],
	[502, "provider_unavailable", /provider.*unavailable/i],
	[402, "PLAN_REQUIRED", /plan/i],
])("explains browser sign-in error %s/%s without server messages or references", async (status, code, copy) => {
	client.startAgentLogin.mockRejectedValue(new CloudApiError(status as number, { error: code as string, code: code as string, message: "HTTP private-provider-secret", requestId: "secret-reference" }));
	await mount(); await press("Connect Claude Code");
	expect(text()).toMatch(copy as RegExp);
	expect(text()).not.toMatch(/HTTP|private-provider-secret|secret-reference|Reference:/);
	expect(button("Use Claude Code API key")).toBeDefined();
	expect(client.deleteSession).not.toHaveBeenCalled(); expect(client.resumeSession).not.toHaveBeenCalled();
});

it("explains a network failure while saving a personal key and clears the secret", async () => {
	client.putUserAgentConnection.mockRejectedValue(new TypeError("private-provider-secret"));
	await mount(); await press("Use Claude Code API key");
	await act(async () => renderer!.root.findByProps({ accessibilityLabel: "Claude Code API key" }).props.onChangeText("private-key"));
	await press("Save API key");
	expect(text()).toMatch(/connection.*try again/i); expect(text()).not.toMatch(/private-key|private-provider-secret/);
	expect(renderer!.root.findByProps({ accessibilityLabel: "Claude Code API key" }).props.value).toBe("");
});

it.each(["pending", "invalid"])("does not claim a saved %s API key is ready and keeps its disconnect action", async validationState => {
	const saved = connection("claude-code", validationState);
	client.putUserAgentConnection.mockResolvedValue({ providerConnection: saved });
	await mount(); await press("Use Claude Code API key");
	client.listUserProviderConnections.mockResolvedValue([saved]);
	await act(async () => renderer!.root.findByProps({ accessibilityLabel: "Claude Code API key" }).props.onChangeText("private-key"));
	await press("Save API key");
	expect(text()).not.toContain("API key connected");
	expect(text()).toMatch(/API key saved/);
	expect(text()).toMatch(/Validation pending|Connection needs attention/);
	expect(button("Disconnect Claude Code")).toBeDefined();
	expect(text()).not.toContain("private-key");
});

it("opens Codex device sign-in in the system browser only when requested", async () => {
	await mount(); await press("Reconnect Codex");
	expect(client.startAgentLogin).toHaveBeenCalledWith({ orgId: "org-1", harness: "codex" }, "login-request", { signal: expect.any(AbortSignal) });
	expect(text()).toContain("ABCD-EFGH");
	expect(native.open).not.toHaveBeenCalled();
	await press("Open provider sign-in");
	expect(native.open).toHaveBeenCalledWith("https://auth.openai.com/codex/device");
});

it.each([
	"http://auth.openai.com/codex/device", "https://auth.openai.com.evil.test/codex/device", "https://user:secret@auth.openai.com/codex/device",
	"https://auth.openai.com/other", "https://auth.openai.com:444/codex/device", "https://auth.openai.com/codex/device#secret",
	"https://claude.ai/oauth/authorize?redirect_uri=http%3A%2F%2Flocalhost%3A9000",
])("does not open an untrusted provider authorization URL: %s", async (url) => {
	client.startAgentLogin.mockResolvedValue({ login: waiting({ authorizationUrl: url }) });
	await mount(); await press("Reconnect Codex");
	expect(renderer!.root.findAllByProps({ title: "Open provider sign-in" })).toHaveLength(0);
	expect(native.open).not.toHaveBeenCalled();
	expect(text()).toMatch(/valid provider sign-in link/);
});

it("submits a Claude one-time code securely and clears it immediately even when the request fails", async () => {
	client.startAgentLogin.mockResolvedValue({ login: waiting({ harness: "claude-code", authorizationUrl: "https://claude.ai/oauth/authorize?code=true&redirect_uri=https%3A%2F%2Fconsole.anthropic.com%2Foauth%2Fcode%2Fcallback", userCode: undefined, requiresCode: true }) });
	client.submitAgentLoginCode.mockRejectedValue(new Error("secret-value"));
	await mount(); await press("Connect Claude Code");
	const input = renderer!.root.findByProps({ accessibilityLabel: "Claude sign-in code" });
	expect(input.props.secureTextEntry).toBe(true);
	await act(async () => input.props.onChangeText("secret-value"));
	await press("Submit sign-in code");
	expect(client.submitAgentLoginCode).toHaveBeenCalledWith("login-1", { code: "secret-value" }, { signal: expect.any(AbortSignal) });
	expect(renderer!.root.findByProps({ accessibilityLabel: "Claude sign-in code" }).props.value).toBe("");
	expect(text()).not.toContain("secret-value");
});

it("pauses polling on background and blur and catches up immediately on return", async () => {
	await mount(); await press("Reconnect Codex");
	await act(async () => { native.state = "background"; native.listener!("background"); });
	await act(async () => { await vi.advanceTimersByTimeAsync(5000); });
	expect(client.getAgentLogin).not.toHaveBeenCalled();
	await act(async () => { native.state = "active"; native.listener!("active"); });
	expect(client.getAgentLogin).toHaveBeenCalledOnce();
	await act(async () => native.blur!());
	await act(async () => { await vi.advanceTimersByTimeAsync(5000); });
	expect(client.getAgentLogin).toHaveBeenCalledOnce();
	await act(async () => native.focus!());
	expect(client.getAgentLogin).toHaveBeenCalledTimes(2);
});

it("cancels an active attempt, fences a late poll, and clears the device code", async () => {
	const pending = deferred<{ login: AgentLogin }>(); client.getAgentLogin.mockReturnValue(pending.promise);
	const onChanged = vi.fn(); await mount({ onChanged }); await press("Reconnect Codex");
	await act(async () => { await vi.advanceTimersByTimeAsync(1500); });
	const signal = client.getAgentLogin.mock.calls[0][1].signal;
	await press("Cancel sign-in");
	expect(client.cancelAgentLogin).toHaveBeenCalledWith("login-1");
	expect(signal.aborted).toBe(true);
	await act(async () => pending.resolve({ login: waiting({ status: "connected" }) }));
	expect(onChanged).not.toHaveBeenCalled();
	expect(text()).not.toContain("ABCD-EFGH");
});

it("does not claim Cloud cancellation succeeded when its request fails", async () => {
	client.cancelAgentLogin.mockRejectedValue(new Error("provider-private-secret"));
	await mount(); await press("Reconnect Codex"); await press("Cancel sign-in");
	expect(text()).toContain("Could not confirm Cloud cancellation");
	expect(text()).not.toContain("Sign-in cancelled.");
	expect(text()).not.toContain("provider-private-secret");
	expect(text()).not.toContain("ABCD-EFGH");
});

it("refreshes connected metadata once after successful login and starts no task", async () => {
	client.getAgentLogin.mockResolvedValue({ login: waiting({ status: "connected", userCode: undefined, authorizationUrl: undefined }) });
	const onChanged = vi.fn(); await mount({ onChanged }); await press("Reconnect Codex");
	client.listUserProviderConnections.mockResolvedValue([connection("codex"), connection("claude-code")]);
	await act(async () => { await vi.advanceTimersByTimeAsync(1500); });
	expect(client.listUserProviderConnections).toHaveBeenCalledTimes(2);
	expect(onChanged).toHaveBeenCalledOnce();
	expect(text()).toContain("Sign-in connected");
	await act(async () => { await vi.advanceTimersByTimeAsync(5000); });
	expect(onChanged).toHaveBeenCalledOnce();
});

it("cancels on owner replacement and discards late success from the previous account", async () => {
	const pending = deferred<{ login: AgentLogin }>(); client.getAgentLogin.mockReturnValue(pending.promise);
	const onChanged = vi.fn(); await mount({ onChanged }); await press("Reconnect Codex");
	await act(async () => { await vi.advanceTimersByTimeAsync(1500); });
	const oldSignal = client.getAgentLogin.mock.calls[0][1].signal;
	await act(async () => renderer!.update(<CloudAgentConnections {...props({ ownerKey: "new-account", onChanged })} />));
	expect(oldSignal.aborted).toBe(true);
	expect(client.cancelAgentLogin).toHaveBeenCalledWith("login-1");
	await act(async () => pending.resolve({ login: waiting({ status: "connected" }) }));
	expect(text()).not.toContain("ABCD-EFGH"); expect(onChanged).not.toHaveBeenCalled();
});

it("cancels a start that completes after unmount without publishing a success", async () => {
	const pending = deferred<{ login: AgentLogin }>(); client.startAgentLogin.mockReturnValue(pending.promise);
	const onChanged = vi.fn(); await mount({ onChanged });
	await act(async () => { button("Reconnect Codex").props.onPress(); });
	const signal = client.startAgentLogin.mock.calls[0][2].signal;
	await act(async () => renderer!.unmount()); renderer = undefined;
	expect(signal.aborted).toBe(true);
	await act(async () => pending.resolve({ login: waiting({ status: "connected" }) }));
	expect(client.cancelAgentLogin).toHaveBeenCalledWith("login-1"); expect(onChanged).not.toHaveBeenCalled();
});

it("clears a pasted code and API key when the phone backgrounds", async () => {
	await mount(); await press("Use Claude Code API key");
	await act(async () => renderer!.root.findByProps({ accessibilityLabel: "Claude Code API key" }).props.onChangeText("private-key"));
	await act(async () => { native.state = "background"; native.listener!("background"); });
	expect(renderer!.root.findByProps({ accessibilityLabel: "Claude Code API key" }).props.value).toBe("");
});

it("retains the request identity for an uncertain start retry and uses a new identity after cancellation", async () => {
	native.uuid.mockReturnValueOnce("login-request").mockReturnValueOnce("new-login-request");
	client.startAgentLogin.mockRejectedValueOnce(new Error("response lost"));
	await mount(); await press("Reconnect Codex"); await press("Reconnect Codex");
	expect(client.startAgentLogin.mock.calls[0].slice(0, 2)).toEqual([{ orgId: "org-1", harness: "codex" }, "login-request"]);
	expect(client.startAgentLogin.mock.calls[1].slice(0, 2)).toEqual(client.startAgentLogin.mock.calls[0].slice(0, 2));
	await press("Cancel sign-in");
	expect(client.cancelAgentLogin).toHaveBeenCalledWith("login-1");
	await press("Reconnect Codex");
	expect(client.startAgentLogin.mock.calls[2][1]).toBe("new-login-request");
});

it("expires an attempt without polling beyond the deadline", async () => {
	client.startAgentLogin.mockResolvedValue({ login: waiting({ expiresAt: "2000-01-01T00:00:00Z" }) });
	await mount(); await press("Reconnect Codex");
	await act(async () => { await vi.advanceTimersByTimeAsync(1500); });
	expect(text()).toContain("Sign-in expired");
	expect(client.getAgentLogin).not.toHaveBeenCalled();
	expect(client.cancelAgentLogin).toHaveBeenCalledWith("login-1");
	expect(text()).not.toContain("ABCD-EFGH");
});

it("keeps current connection metadata when refreshing fails and redacts provider errors", async () => {
	await mount();
	client.listUserProviderConnections.mockRejectedValue(new Error("private-key-from-server"));
	await press("Refresh connected agents");
	expect(text()).toContain("Connected"); expect(text()).toContain("Could not refresh connected agents");
	expect(text()).not.toContain("private-key-from-server");
});

it("offers account sign-in without loading personal agent metadata for a signed-out Settings route", async () => {
	native.auth = { signedIn: false, accountId: null, baseUrl: "https://cloud.test", sessionEpoch: 2 };
	await act(async () => { renderer = create(<CloudAgentsScreen />); });
	expect(text()).toContain("Sign in to connect");
	expect(client.listUserProviderConnections).not.toHaveBeenCalled();
	await press("Sign in"); expect(native.push).toHaveBeenCalledWith("/sheets/cloud-signin");
});

it("resets the Settings connection view before displaying another account's metadata", async () => {
	native.auth = { signedIn: true, accountId: "account-1", baseUrl: "https://cloud.test", orgId: "org-1", sessionEpoch: 1, client, isCurrentSession: current };
	await act(async () => { renderer = create(<CloudAgentsScreen />); });
	await press("Use Claude Code API key");
	await act(async () => renderer!.root.findByProps({ accessibilityLabel: "Claude Code API key" }).props.onChangeText("previous-account-key"));
	client.listUserProviderConnections.mockResolvedValue([]);
	native.auth = { ...native.auth as object, accountId: "account-2", sessionEpoch: 2 };
	await act(async () => renderer!.update(<CloudAgentsScreen />));
	expect(renderer!.root.findAllByType("TextInput" as never)).toHaveLength(0);
	expect(text()).not.toContain("previous-account-key");
	expect(text()).not.toContain("Connected");
});

it("opens a Claude hosted code flow without placing its code in navigation", async () => {
	const url = "https://claude.com/cai/oauth/authorize?code=true&redirect_uri=https%3A%2F%2Fplatform.claude.com%2Foauth%2Fcode%2Fcallback";
	client.startAgentLogin.mockResolvedValue({ login: waiting({ harness: "claude-code", authorizationUrl: url, requiresCode: true, userCode: undefined }) });
	await mount(); await press("Connect Claude Code"); await press("Open provider sign-in");
	expect(native.open).toHaveBeenCalledWith(url); expect(native.push).not.toHaveBeenCalled();
});

it("bounds Android sheet scrolling while keeping it inside the sheet", async () => {
	native.platform.OS = "android";
	await mount({ inSheet: true });
	const scroll = renderer!.root.findByType("ScrollView" as never);
	expect(scroll.props.style.maxHeight).toBeGreaterThan(0);
	expect(scroll.props.style.maxHeight).toBeLessThan(844);
	expect(scroll.props.nestedScrollEnabled).toBe(true);
});

it.each([
	["Codex", "codex", "api_key"], ["Cursor", "cursor", "api_key"], ["OpenCode", "opencode", "opencode_api_key"],
])("saves a personal %s API key through its supported credential type", async (label, agent, credentialType) => {
	await mount(); await press(`Use ${label} API key`);
	await act(async () => renderer!.root.findByProps({ accessibilityLabel: `${label} API key` }).props.onChangeText("provider-private-key"));
	await press("Save API key");
	expect(client.putUserAgentConnection).toHaveBeenCalledWith(agent, { credentialType, secret: "provider-private-key" }, { signal: expect.any(AbortSignal) });
	expect(text()).not.toContain("provider-private-key");
});

it.each(["native", "api-key"])("reopening project creation immediately includes an agent connected in Settings through %s", async (method) => {
	const projectKey = JSON.stringify(["https://cloud.test", "cache-account", "org-1", 37]);
	const github: ProjectGitHubSetup = {
		installations: [{ id: "install-1", githubInstallationId: "1", accountLogin: "me", accountType: "User", status: "active", repositorySelection: "selected", syncStatus: "ready", createdAt: "2026-10-23T00:00:00Z", updatedAt: "2026-10-23T00:00:00Z" }],
		repositories: [{ githubRepositoryId: "1", name: "app", fullName: "me/app", htmlUrl: "https://github.com/me/app", defaultBranch: "main", visibility: "private", isPrivate: true, isArchived: false, access: "active", grantedAt: "2026-10-23T00:00:00Z" }],
	};
	cloudProjectSetupCache.setGitHub(projectKey, github);
	cloudProjectSetupCache.setConnections(projectKey, [connection("codex")]);
	native.auth = { signedIn: true, accountId: "cache-account", baseUrl: "https://cloud.test", orgId: "org-1", sessionEpoch: 37, orgLoading: false, client, isCurrentSession: current };
	await act(async () => { renderer = create(<CloudAgentsScreen />); });
	client.listUserProviderConnections.mockResolvedValue([connection("codex"), connection("claude-code")]);
	if (method === "native") {
		client.startAgentLogin.mockResolvedValue({ login: waiting({ harness: "claude-code", status: "connected" }) });
		await press("Connect Claude Code");
	} else {
		await press("Use Claude Code API key");
		await act(async () => renderer!.root.findByProps({ accessibilityLabel: "Claude Code API key" }).props.onChangeText("provider-private-key"));
		await press("Save API key");
	}
	expect(cloudProjectSetupCache.read(projectKey).github).toEqual({ value: github, fresh: true });
	await act(async () => renderer!.update(<CreateCloudProject />));
	await act(async () => renderer!.root.findByProps({ accessibilityLabel: "Select a repository", accessibilityRole: "combobox" }).props.onPress());
	await act(async () => renderer!.root.findByProps({ accessibilityLabel: "Search repositories" }).props.onChangeText("app"));
	await act(async () => renderer!.root.findByProps({ accessibilityLabel: "Select me/app" }).props.onPress());
	await press("Continue");
	await act(async () => renderer!.root.findByProps({ testID: "Worker agent" }).props.onPress());
	expect(text()).toContain("Claude Code");
	expect(text()).toContain("Codex");
	expect(cloudProjectSetupCache.read(projectKey).connections?.value.map(item => item.provider)).toEqual(["codex", "claude-code"]);
	cloudProjectSetupCache.invalidate(projectKey, "github"); cloudProjectSetupCache.invalidate(projectKey, "connections");
});
