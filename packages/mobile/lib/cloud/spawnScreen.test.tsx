import { act, create, type ReactTestRenderer } from "react-test-renderer";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
const f = vi.hoisted(() => ({
	storage: new Map<string, string>(), current: true, push: vi.fn(), back: vi.fn(), callbacks: [] as Array<() => void>, spawn: vi.fn(),
	project: { id: "p1", displayName: "Demo", config: { worker: { agent: "codex", agentConfig: { model: "configured", effort: "high" } } } },
	client: { getProject: vi.fn(), listUserProviderConnections: vi.fn(), getCurrentAccount: vi.fn(), createSession: vi.fn() },
	dismissKeyboard: vi.fn(),
	alert: vi.fn(),
	platform: { OS: "ios" },
	hardwareBack: null as (() => boolean) | null,
	source: { kind: "cloud", scopeKey: "account-one" },
	sourceFor: vi.fn(), configForHost: vi.fn(() => null), isCurrent: vi.fn(),
	board: { projects: [{ source: { kind: "cloud", id: "o1" }, value: { id: "p1", name: "Demo" } }], sources: { '["cloud","o1"]': { resolved: true, loading: false } } },
	available: [{ kind: "cloud", id: "o1" }],
	hostStates: [] as Array<{ hostId: string; name: string }>,
}));
vi.mock("@react-native-async-storage/async-storage", () => ({ default: {
	getItem: async (key: string) => f.storage.get(key) ?? null, setItem: async (key: string, value: string) => { f.storage.set(key, value); }, removeItem: async (key: string) => { f.storage.delete(key); },
} }));
vi.mock("expo-router", () => ({ useLocalSearchParams: () => ({ source: "cloud", sourceId: "o1", projectId: "p1" }), useRouter: () => ({ push: f.push, canGoBack: () => true, back: f.back }) }));
vi.mock("react-native", () => ({ View: "View", Text: "Text", TextInput: "TextInput", ScrollView: "ScrollView", Pressable: "Pressable", Platform: f.platform, StyleSheet: { create: (v: unknown) => v }, Keyboard: { dismiss: f.dismissKeyboard }, BackHandler: { addEventListener: (_event: string, listener: () => boolean) => { f.hardwareBack = listener; return { remove: () => { f.hardwareBack = null; } }; } }, InteractionManager: { runAfterInteractions: (fn: () => void) => f.callbacks.push(fn) }, Alert: { alert: f.alert } }));
vi.mock("expo-clipboard", () => ({ setStringAsync: vi.fn() }));
vi.mock("react-native-keyboard-controller", () => ({ KeyboardStickyView: "KeyboardStickyView", useKeyboardState: () => 0 }));
vi.mock("@expo/ui/community/bottom-sheet", () => ({ default: "BottomSheet", BottomSheetView: "BottomSheetView" }));
vi.mock("expo-document-picker", () => ({ getDocumentAsync: vi.fn() }));
vi.mock("expo-file-system", () => ({ File: class {} }));
vi.mock("../icons", () => ({ Feather: "Feather" }));
vi.mock("../api", () => ({ ApiError: class extends Error {}, getAgentModels: vi.fn(), getAgents: vi.fn(), getProject: vi.fn(), getSettings: vi.fn() }));
vi.mock("../store", () => ({ useApp: () => ({ scopedBoard: f.board, availableSources: f.available, sourceFor: f.sourceFor, spawnOn: f.spawn, configForHost: f.configForHost, hostStates: f.hostStates, configResolved: true, switchHost: vi.fn(async () => {}), setActiveProject: vi.fn() }) }));
vi.mock("./authStore", () => ({ useCloudAuth: () => ({ client: f.client, orgId: "o1", isCurrentSession: f.isCurrent }) }));
vi.mock("../ThemeProvider", () => ({ useTheme: () => ({}), useThemedStyles: (fn: (v: unknown) => unknown) => fn({}) }));
vi.mock("../haptics", () => ({ haptics: { tap: vi.fn(), success: vi.fn(), error: vi.fn() } }));
vi.mock("../voice/useVoiceInput", () => ({ useVoiceInput: () => ({ state: "idle", mode: "push", pressIn: vi.fn(), pressOut: vi.fn() }) }));
vi.mock("../spawn-prompt-input", () => ({ SpawnPromptInput: "Prompt" }));
vi.mock("../spawn-composer-controls", () => ({ SpawnComposerControls: "Controls" }));
vi.mock("../ui", () => ({ Button: "Button" }));
vi.mock("./CloudSessionManager", () => ({ CloudSessionManager: "Manager" }));
vi.mock("./CloudAgentConnections", () => ({ CloudAgentConnections: "AgentConnections" }));
vi.mock("../RouteErrorBoundary", () => ({ SheetErrorBoundary: "Boundary" }));
import SpawnModal from "../../app/spawn";
import { createCloudSessionSource } from "./source";
import { acceptCloudOperation, prepareCloudOperation, readCloudOperation, reserveCloudOperation } from "./operation";
import { CloudApiError } from "@aoagents/cloud-client";
import AsyncStorage from "@react-native-async-storage/async-storage";
import { getAgentModels, getAgents, getProject, getSettings } from "../api";
let renderer: ReactTestRenderer | undefined;
const mount = async () => { await act(async () => { renderer = create(<SpawnModal />); }); };
const controls = () => renderer!.root.findByType("Controls" as never).props;
const savedRequest = () => prepareCloudOperation(undefined, "account-one", { projectId: "p1", kind: "worker", harness: "codex", prompt: "Original task", model: "frozen", reasoningEffort: "low", mode: "trusted", deniedCommands: [] }, () => "original");
async function startNewRequestChoice() {
	await act(async () => renderer!.root.findByProps({ accessibilityLabel: "More recovery options", accessibilityRole: "button" }).props.onPress());
	await act(async () => renderer!.root.findByProps({ title: "Start a new request…" }).props.onPress());
}
const confirmNewRequest = () => f.alert.mock.calls.at(-1)![2].find((action: { text: string }) => action.text === "Use current draft").onPress();
beforeEach(() => {
	vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true);
	vi.spyOn(console, "error").mockImplementation(() => {});
	f.storage.clear(); f.current = true; f.callbacks = []; f.push.mockReset(); f.back.mockReset(); f.spawn.mockReset().mockResolvedValue({ id: "s1", projectId: "p1" });
	f.hardwareBack = null;
	f.dismissKeyboard.mockReset();
	f.alert.mockReset();
	f.platform.OS = "ios";
	f.sourceFor.mockImplementation(() => f.source); f.isCurrent.mockImplementation(() => f.current);
	f.configForHost.mockReset().mockReturnValue(null);
	f.available = [{ kind: "cloud", id: "o1" }]; f.hostStates = [];
	f.board.projects = [{ source: { kind: "cloud", id: "o1" }, value: { id: "p1", name: "Demo" } }];
	f.client.getProject.mockReset().mockResolvedValue({ project: f.project });
	f.client.getCurrentAccount.mockReset().mockResolvedValue({ chatHarnesses: ["codex", "claude-code"] });
	f.client.createSession.mockReset().mockResolvedValue({ session: { id: "s1" } });
	f.client.listUserProviderConnections.mockReset().mockResolvedValue([{ provider: "codex", label: "default", validationState: "valid" }]);
});
afterEach(async () => { if (renderer) await act(async () => renderer!.unmount()); renderer = undefined; vi.restoreAllMocks(); vi.unstubAllGlobals(); });
describe("Cloud spawn sheet", () => {
	it("connects an agent inside the sheet, refreshes metadata, and preserves the frozen worker request", async () => {
		await reserveCloudOperation("worker-create/p1", savedRequest());
		await mount();
		const original = await readCloudOperation("account-one", "worker-create/p1");
		await act(async () => renderer!.root.findByProps({ title: "Connect coding agents" }).props.onPress());
		const setup = renderer!.root.findByType("AgentConnections" as never);
		const loads = f.client.listUserProviderConnections.mock.calls.length;
		await act(async () => setup.props.onChanged());
		expect(f.client.listUserProviderConnections.mock.calls.length).toBeGreaterThan(loads);
		expect(f.spawn).not.toHaveBeenCalled();
		await act(async () => setup.props.onBack());
		expect(renderer!.root.findByType("Prompt" as never).props.value).toBe("Original task");
		expect(controls().projectId).toBe("p1");
		expect(controls().modelLabel).toBe("frozen");
		expect(await readCloudOperation("account-one", "worker-create/p1")).toEqual(original);
	});
	it.each([
		{ harness: "codex", chatHarnesses: ["codex"], expectedMode: "chat" },
		{ harness: "claude-code", chatHarnesses: ["claude-code"], expectedMode: "chat" },
		{ harness: "opencode", chatHarnesses: ["codex"], expectedMode: "tui" },
		{ harness: "codex", chatHarnesses: [], expectedMode: "tui" },
	])("creates $harness through the real Cloud source in $expectedMode mode", async ({ harness, chatHarnesses, expectedMode }) => {
		f.client.getCurrentAccount.mockResolvedValue({ chatHarnesses });
		f.client.listUserProviderConnections.mockResolvedValue([{ provider: harness, label: "default", validationState: "valid" }]);
		const source = createCloudSessionSource({ client: f.client as never, orgId: "o1", scopeKey: "account-one", isCurrent: () => f.current });
		f.spawn.mockImplementation(async (_destination, input) => ({ ...await source.createSession(input), projectId: input.projectId }));
		await mount();
		await act(async () => controls().onSelectHarness(harness));
		await act(async () => controls().onSpawn());
		expect(f.client.createSession).toHaveBeenCalledWith("o1", expect.objectContaining({ harness, interfaceMode: expectedMode }), { idempotencyKey: expect.any(String) });
	});
	it.each(["chat", "tui", undefined] as const)("retries the reserved %s interface without changing its original payload", async (interfaceMode) => {
		const operation = savedRequest();
		const payload = { ...operation.payload, ...(interfaceMode ? { interfaceMode } : {}) };
		await reserveCloudOperation("worker-create/p1", prepareCloudOperation(undefined, "account-one", payload, () => "original"));
		const source = createCloudSessionSource({ client: f.client as never, orgId: "o1", scopeKey: "account-one", isCurrent: () => f.current });
		f.spawn.mockImplementation(async (_destination, input) => ({ ...await source.createSession(input), projectId: input.projectId }));
		await mount();
		await act(async () => controls().onSpawn());
		expect(f.client.createSession).toHaveBeenCalledWith("o1", payload, { idempotencyKey: "original" });
	});
	it("recovers an authoritative Chat rejection by creating a Terminal task with a new request identity", async () => {
		f.client.createSession.mockRejectedValueOnce(new CloudApiError(409, { error: "conflict", code: "SESSION_MODE_UNSUPPORTED", message: "Codex cannot start in Chat here.", requestId: "unsupported" }));
		const source = createCloudSessionSource({ client: f.client as never, orgId: "o1", scopeKey: "account-one", isCurrent: () => f.current });
		f.spawn.mockImplementation(async (_destination, input) => ({ ...await source.createSession(input), projectId: input.projectId }));
		await mount();
		await act(async () => renderer!.root.findByType("Prompt" as never).props.onChangeText("Inspect the repository"));
		await act(async () => controls().onSpawn());
		const fallback = renderer!.root.findAllByProps({ title: "Create as Terminal UI instead" });
		expect(fallback).toHaveLength(1);
		expect(await readCloudOperation("account-one", "worker-create/p1")).toBeNull();
		const chatRequest = f.client.createSession.mock.calls[0];
		expect(chatRequest[1]).toMatchObject({ interfaceMode: "chat", prompt: "Inspect the repository", model: "configured", reasoningEffort: "high" });
		await act(async () => fallback[0].props.onPress());
		await act(async () => controls().onSpawn());
		const terminalRequest = f.client.createSession.mock.calls[1];
		expect(terminalRequest[1]).toMatchObject({ interfaceMode: "tui", prompt: "Inspect the repository", harness: "codex", model: "configured", reasoningEffort: "high" });
		expect(terminalRequest[2].idempotencyKey).not.toBe(chatRequest[2].idempotencyKey);
	});
	it.each([
		new TypeError("Network connection lost"),
		new CloudApiError(503, { error: "internal_error", code: "SESSION_MODE_UNSUPPORTED", message: "Unavailable", requestId: "ambiguous" }),
		new CloudApiError(409, { error: "conflict", code: "CHAT_DRIVER_UNAVAILABLE", message: "Unavailable", requestId: "other-rejection" }),
	])("keeps an uncertain Cloud request frozen without offering a changed-interface retry (%s)", async (error) => {
		f.client.createSession.mockRejectedValueOnce(error);
		const source = createCloudSessionSource({ client: f.client as never, orgId: "o1", scopeKey: "account-one", isCurrent: () => f.current });
		f.spawn.mockImplementation(async (_destination, input) => ({ ...await source.createSession(input), projectId: input.projectId }));
		await mount(); await act(async () => controls().onSpawn());
		expect(renderer!.root.findAllByProps({ title: "Create as Terminal UI instead" })).toHaveLength(0);
		expect(controls().submitLabel).toBe("Retry");
		expect(await readCloudOperation("account-one", "worker-create/p1")).toMatchObject({ key: f.client.createSession.mock.calls[0][2].idempotencyKey, payload: { interfaceMode: "chat" } });
		await act(async () => controls().onSpawn());
		expect(f.client.createSession.mock.calls[1]).toEqual(f.client.createSession.mock.calls[0]);
	});
	it("keeps the original retry when a known rejection cannot release its journal record", async () => {
		f.client.createSession.mockRejectedValueOnce(new CloudApiError(409, { error: "conflict", code: "SESSION_MODE_UNSUPPORTED", message: "Chat unavailable", requestId: "unsupported" }));
		vi.spyOn(AsyncStorage, "removeItem").mockRejectedValueOnce(new Error("Storage unavailable"));
		const source = createCloudSessionSource({ client: f.client as never, orgId: "o1", scopeKey: "account-one", isCurrent: () => f.current });
		f.spawn.mockImplementation(async (_destination, input) => ({ ...await source.createSession(input), projectId: input.projectId }));
		await mount(); await act(async () => controls().onSpawn());
		expect(renderer!.root.findAllByProps({ title: "Create as Terminal UI instead" })).toHaveLength(0);
		expect(controls().submitLabel).toBe("Retry");
		expect(await readCloudOperation("account-one", "worker-create/p1")).toMatchObject({ payload: { interfaceMode: "chat" } });
	});
	it("does not offer a changed-interface retry after an uncertain request later receives an unsupported response", async () => {
		f.client.createSession.mockRejectedValueOnce(new TypeError("Response lost")).mockRejectedValueOnce(new CloudApiError(409, { error: "conflict", code: "SESSION_MODE_UNSUPPORTED", message: "Chat is no longer available.", requestId: "capability-changed" }));
		const source = createCloudSessionSource({ client: f.client as never, orgId: "o1", scopeKey: "account-one", isCurrent: () => f.current });
		f.spawn.mockImplementation(async (_destination, input) => ({ ...await source.createSession(input), projectId: input.projectId }));
		await mount(); await act(async () => controls().onSpawn());
		await act(async () => controls().onSpawn());
		expect(renderer!.root.findAllByProps({ title: "Create as Terminal UI instead" })).toHaveLength(0);
		expect(controls().submitLabel).toBe("Retry");
		expect(await readCloudOperation("account-one", "worker-create/p1")).toMatchObject({ key: f.client.createSession.mock.calls[0][2].idempotencyKey, payload: { interfaceMode: "chat" } });
		expect(f.client.createSession.mock.calls[1]).toEqual(f.client.createSession.mock.calls[0]);
	});
	it("keeps the Local Chat-preflight fallback on the paired desktop", async () => {
		const localConfig = { hostId: "desktop", host: "desktop.local", httpPort: 3000, password: "paired" };
		f.available.push({ kind: "local", id: "desktop" });
		f.configForHost.mockReturnValue(localConfig as never);
		f.sourceFor.mockImplementation((source) => source.kind === "local" ? { kind: "local" } : f.source);
		const agents = [{ id: "codex", label: "Codex" }];
		vi.mocked(getAgents).mockResolvedValue({ supported: agents, installed: agents, authorized: agents });
		vi.mocked(getSettings).mockResolvedValue({ chatHarnesses: ["codex"] } as never);
		vi.mocked(getAgentModels).mockResolvedValue({ models: [] } as never);
		f.spawn.mockRejectedValueOnce(Object.assign(new Error("Chat driver unavailable"), { code: "CHAT_DRIVER_UNAVAILABLE" }));
		await mount();
		await act(async () => controls().onSelectDestination({ kind: "local", id: "desktop" }));
		await act(async () => controls().onSelectProject("__standalone__"));
		await act(async () => controls().onSpawn());
		await act(async () => renderer!.root.findByProps({ title: "Create as Terminal UI instead" }).props.onPress());
		await act(async () => controls().onSpawn());
		expect(f.spawn.mock.calls.map(([source, input]) => [source, input.mode])).toEqual([
			[{ kind: "local", id: "desktop" }, "chat"], [{ kind: "local", id: "desktop" }, "tui"],
		]);
		expect(f.client.createSession).not.toHaveBeenCalled();
	});
	it("shows one known-rejection state instead of the uncertain retry warning or raw support reference", async () => {
		f.spawn.mockImplementation(async (_source, input) => {
			await reserveCloudOperation("worker-create/p1", prepareCloudOperation(undefined, "account-one", { projectId: "p1", kind: "worker", harness: "codex", prompt: input.prompt, mode: "trusted", deniedCommands: [] }, () => input.clientRequestId));
			throw new CloudApiError(409, { error: "conflict", code: "SANDBOX_QUOTA_EXCEEDED", message: "Raw server quota message", requestId: "hidden-reference" });
		});
		await mount(); await act(async () => controls().onSpawn());
		const text = renderer!.root.findAllByType("Text" as never).map(node => node.props.children).join(" ");
		expect(text).toContain("Cloud session limit reached");
		expect(text).not.toContain("may have completed");
		expect(text).not.toContain("Raw server quota message");
		expect(text).not.toContain("hidden-reference");
		expect(renderer!.root.findAllByProps({ title: "Discard retry record" })).toHaveLength(0);
		expect(controls().submitLabel).toBe("Manage Cloud sessions");
		expect(f.dismissKeyboard).toHaveBeenCalledOnce();
	});

	it.each(["button", "hardware"])("keeps the task draft while visiting Cloud management and returning by %s", async (back) => {
		f.spawn.mockRejectedValue(new CloudApiError(409, { error: "conflict", code: "SANDBOX_QUOTA_EXCEEDED", message: "Quota reached", requestId: "ref" }));
		await mount();
		await act(async () => renderer!.root.findByType("Prompt" as never).props.onChangeText("Keep my instructions"));
		await act(async () => controls().onSelectModel("chosen-model"));
		await act(async () => controls().onSpawn());
		await act(async () => controls().onSpawn());
		expect(f.spawn).toHaveBeenCalledOnce();
		expect(f.back).not.toHaveBeenCalled(); expect(f.push).not.toHaveBeenCalled();
		const manager = renderer!.root.findByType("Manager" as never);
		expect(manager.parent?.props.style).toContainEqual(expect.objectContaining({ paddingHorizontal: expect.any(Number), paddingBottom: expect.any(Number) }));
		expect(manager.props.orgId).toBe("o1"); expect(manager.props.scopeKey).toBe("account-one");
		expect(manager.props.projectNames).toMatchObject({ p1: "Demo" });
		if (back === "button") await act(async () => manager.props.onBack());
		else await act(async () => { expect(f.hardwareBack?.()).toBe(true); });
		expect(renderer!.root.findByType("Prompt" as never).props.value).toBe("Keep my instructions");
		expect(controls().harness).toBe("codex"); expect(controls().modelLabel).toBe("chosen-model"); expect(controls().projectId).toBe("p1");
		expect(f.spawn).toHaveBeenCalledOnce();
	});

	it("still keeps the safe retry identity for a genuinely uncertain request", async () => {
		f.spawn.mockImplementation(async (_source, input) => {
			await reserveCloudOperation("worker-create/p1", prepareCloudOperation(undefined, "account-one", { projectId: "p1", kind: "worker", harness: "codex", prompt: "Inspect", mode: "trusted", interfaceMode: input.mode, deniedCommands: [], model: input.model, reasoningEffort: input.reasoningEffort }, () => input.clientRequestId));
			throw new TypeError("Network connection lost");
		});
		await mount(); await act(async () => renderer!.root.findByType("Prompt" as never).props.onChangeText("Inspect"));
		await act(async () => controls().onSpawn());
		expect(renderer!.root.findAllByType("Text" as never).map(node => node.props.children).join(" ")).toContain("Couldn’t confirm the worker started");
		expect(controls().submitLabel).toBe("Retry");
		const id = f.spawn.mock.calls[0][1].clientRequestId;
		await act(async () => controls().onSpawn());
		expect(f.spawn.mock.calls[1][1].clientRequestId).toBe(id);
	});
	it("restores recovery without submitting and retries only the frozen request after draft edits", async () => {
		await reserveCloudOperation("worker-create/p1", savedRequest());
		f.spawn.mockRejectedValue(new TypeError("Network lost"));
		await mount();
		expect(f.spawn).not.toHaveBeenCalled();
		expect(controls().submitLabel).toBe("Retry");
		expect(renderer!.root.findAllByProps({ title: "Check workers" })).toHaveLength(0);
		expect(renderer!.root.findAllByProps({ title: "Discard retry record" })).toHaveLength(0);
		await act(async () => renderer!.root.findByType("Prompt" as never).props.onChangeText("Edited task draft"));
		await act(async () => controls().onSelectModel("edited-model"));
		await act(async () => controls().onSpawn());
		expect(f.spawn).toHaveBeenCalledExactlyOnceWith({ kind: "cloud", id: "o1" }, expect.objectContaining({ prompt: "Original task", model: "frozen", reasoningEffort: "low", clientRequestId: "original" }));
		expect(renderer!.root.findByType("Prompt" as never).props.value).toBe("Edited task draft");
		expect(controls().modelLabel).toBe("edited-model");
	});
	it("views Cloud sessions inside recovery and returns without losing an edited draft", async () => {
		await reserveCloudOperation("worker-create/p1", savedRequest());
		await mount();
		await act(async () => renderer!.root.findByType("Prompt" as never).props.onChangeText("My current draft"));
		await act(async () => renderer!.root.findByProps({ title: "View Cloud sessions" }).props.onPress());
		expect(f.back).not.toHaveBeenCalled(); expect(f.push).not.toHaveBeenCalled(); expect(f.spawn).not.toHaveBeenCalled();
		await act(async () => renderer!.root.findByType("Manager" as never).props.onBack());
		expect(renderer!.root.findByType("Prompt" as never).props.value).toBe("My current draft");
		expect(await readCloudOperation("account-one", "worker-create/p1")).toMatchObject({ key: "original" });
	});
	it("prepares a separate request only after confirmation and keeps the complete current draft", async () => {
		await reserveCloudOperation("worker-create/p1", savedRequest());
		await mount();
		await act(async () => renderer!.root.findByType("Prompt" as never).props.onChangeText("Keep this draft"));
		await act(async () => controls().onSelectModel("edited-model"));
		await startNewRequestChoice();
		expect(f.alert.mock.calls.at(-1)![1]).toMatch(/may.*created.*worker/i);
		expect(await readCloudOperation("account-one", "worker-create/p1")).not.toBeNull();
		await act(async () => confirmNewRequest());
		expect(await readCloudOperation("account-one", "worker-create/p1")).toBeNull();
		expect(renderer!.root.findByType("Prompt" as never).props.value).toBe("Keep this draft");
		expect(controls().modelLabel).toBe("edited-model"); expect(controls().harness).toBe("codex"); expect(controls().projectId).toBe("p1");
		expect(controls().submitLabel).toBeUndefined(); expect(f.spawn).not.toHaveBeenCalled();
	});
	it("cancelling the new-request choice keeps the original recovery identity", async () => {
		await reserveCloudOperation("worker-create/p1", savedRequest());
		await mount(); await startNewRequestChoice();
		const cancel = f.alert.mock.calls.at(-1)![2].find((action: { style?: string }) => action.style === "cancel");
		await act(async () => cancel.onPress?.());
		expect(await readCloudOperation("account-one", "worker-create/p1")).toMatchObject({ key: "original" });
		expect(controls().submitLabel).toBe("Retry"); expect(f.spawn).not.toHaveBeenCalled();
	});
	it("does not discard recovery from a confirmation belonging to a replaced account", async () => {
		await reserveCloudOperation("worker-create/p1", savedRequest());
		await mount(); await startNewRequestChoice();
		f.current = false;
		await act(async () => confirmNewRequest());
		expect(await readCloudOperation("account-one", "worker-create/p1")).toMatchObject({ key: "original" });
		expect(f.spawn).not.toHaveBeenCalled();
	});
	it("keeps an acceptance recorded while the new-request confirmation was open", async () => {
		await reserveCloudOperation("worker-create/p1", savedRequest());
		await mount(); await startNewRequestChoice();
		await acceptCloudOperation("account-one", "worker-create/p1", "original", "already-created");
		await act(async () => confirmNewRequest());
		expect(await readCloudOperation("account-one", "worker-create/p1")).toMatchObject({ acceptedId: "already-created" });
		expect(controls().submitLabel).toBe("Open worker"); expect(f.spawn).not.toHaveBeenCalled();
	});
	it("keeps a structured retry problem readable without showing codes or references", async () => {
		await reserveCloudOperation("worker-create/p1", savedRequest());
		f.spawn.mockRejectedValue(new CloudApiError(401, { error: "unauthorized", code: "UNAUTHORIZED", message: "Sign in again to continue.", requestId: "hidden-auth-reference" }));
		await mount(); await act(async () => controls().onSpawn());
		const text = renderer!.root.findAllByType("Text" as never).map(node => node.props.children).join(" ");
		expect(text).toContain("Sign in again to continue."); expect(text).not.toContain("hidden-auth-reference"); expect(text).not.toContain("UNAUTHORIZED"); expect(text).not.toContain("HTTP 401");
		expect(controls().submitLabel).toBe("Retry");
	});
	it("returns from Android's native sheet dismissal to the preserved draft in a reopened sheet", async () => {
		f.platform.OS = "android";
		f.spawn.mockRejectedValue(new CloudApiError(409, { error: "conflict", code: "SANDBOX_QUOTA_EXCEEDED", message: "Quota reached", requestId: "android-quota-ref" }));
		await mount();
		await act(async () => renderer!.root.findByType("Prompt" as never).props.onChangeText("Keep my Android draft"));
		await act(async () => controls().onSpawn());
		await act(async () => controls().onSpawn());
		const sheet = renderer!.root.findByType("BottomSheet" as never);
		await act(async () => sheet.props.onClose());
		expect(f.back).not.toHaveBeenCalled();
		expect(renderer!.root.findAllByType("Manager" as never)).toHaveLength(0);
		expect(renderer!.root.findByType("Prompt" as never).props.value).toBe("Keep my Android draft");
		expect(renderer!.root.findByType("BottomSheet" as never)).not.toBe(sheet);
		expect(f.spawn).toHaveBeenCalledOnce();
		await act(async () => renderer!.root.findByType("BottomSheet" as never).props.onClose());
		expect(f.back).toHaveBeenCalledOnce();
	});
	it("offers machine names and Cloud as destination labels", async () => {
		f.available = [{ kind: "local", id: "host-a" }, { kind: "local", id: "host-b" }, { kind: "cloud", id: "o1" }];
		f.hostStates = [{ hostId: "host-a", name: "Alpha" }, { hostId: "host-b", name: "Beta" }];
		f.sourceFor.mockImplementation((source) => source.id === "host-b" ? null : f.source);
		await mount();
		expect(controls().destinations).toEqual([
			{ source: { kind: "local", id: "host-a" }, label: "Alpha", available: true, unavailableReason: null },
			{ source: { kind: "local", id: "host-b" }, label: "Beta", available: false, unavailableReason: null },
			{ source: { kind: "cloud", id: "o1" }, label: "Cloud", available: true, unavailableReason: null },
		]);
		expect(controls().destination).toEqual({ kind: "cloud", id: "o1" });
	});
	it("shows project-default and custom model choices without a desktop catalog", async () => {
		await mount(); expect(controls().showModels).toBe(true); expect(controls().showAttachments).toBe(false);
		expect(controls().modelLabel).toBe("configured"); expect(controls().models).toContainEqual({ id: "__custom__", label: "Custom model…" });
		await act(async () => controls().onSelectModel("__custom__"));
		const input = renderer!.root.findByType("TextInput" as never);
		await act(async () => input.props.onChangeText("custom-model"));
		const useModel = renderer!.root.findAllByType("Button" as never).find((node) => node.props.title === "Use model");
		await act(async () => useModel!.props.onPress());
		await act(async () => controls().onSpawn());
		expect(f.spawn).toHaveBeenCalledWith({ kind: "cloud", id: "o1" }, expect.objectContaining({ model: "custom-model", reasoningEffort: "high", clientRequestId: expect.any(String) }));
	});
	it("submits a double tap once and does not navigate after dismissal", async () => {
		let resolve!: (v: unknown) => void; f.spawn.mockImplementation(() => new Promise((done) => { resolve = done; }));
		await mount(); await act(async () => { controls().onSpawn(); controls().onSpawn(); });
		expect(f.spawn).toHaveBeenCalledOnce();
		await act(async () => renderer!.unmount()); renderer = undefined;
		await act(async () => resolve({ id: "s1", projectId: "p1" }));
		expect(f.back).not.toHaveBeenCalled(); expect(f.callbacks).toHaveLength(0);
	});
	it.each(["success", "failure"])("releases submission state after a destination change and late %s", async (outcome) => {
		f.available = [{ kind: "cloud", id: "o1" }, { kind: "local", id: "host-a" }];
		f.hostStates = [{ hostId: "host-a", name: "Alpha" }];
		let finish!: () => void;
		f.spawn.mockImplementationOnce(() => new Promise((resolve, reject) => {
			finish = () => outcome === "success" ? resolve({ id: "old-worker", projectId: "p1" }) : reject(new Error("Old failure"));
		}));
		await mount();
		await act(async () => renderer!.root.findByType("Prompt" as never).props.onChangeText("Keep my draft"));
		let pending!: Promise<void>;
		await act(async () => { pending = controls().onSpawn(); });
		expect(controls().busy).toBe(true);
		await act(async () => controls().onSelectDestination({ kind: "local", id: "host-a" }));
		await act(async () => { finish(); await pending; });
		expect(controls().busy).toBe(false);
		expect(controls().destination).toEqual({ kind: "local", id: "host-a" });
		expect(renderer!.root.findByType("Prompt" as never).props.value).toBe("Keep my draft");
		expect(f.spawn).toHaveBeenCalledOnce();
		expect(f.back).not.toHaveBeenCalled(); expect(f.callbacks).toHaveLength(0);
		expect(renderer!.root.findAllByType("Text" as never).map(node => node.props.children).join(" ")).not.toContain("Old failure");
	});
	it("does not publish old Cloud recovery after switching during its storage read", async () => {
		f.available = [{ kind: "cloud", id: "o1" }, { kind: "local", id: "host-a" }];
		f.hostStates = [{ hostId: "host-a", name: "Alpha" }];
		await mount();
		const operation = savedRequest();
		let finish!: () => void;
		f.spawn.mockImplementationOnce(async () => {
			await reserveCloudOperation("worker-create/p1", operation);
			vi.spyOn(AsyncStorage, "getItem").mockImplementation((key) => new Promise((resolve) => { finish = () => resolve(f.storage.get(key) ?? null); }));
			throw new Error("Connection lost");
		});
		let pending!: Promise<void>;
		await act(async () => { pending = controls().onSpawn(); });
		expect(finish).toBeTypeOf("function");
		await act(async () => controls().onSelectDestination({ kind: "local", id: "host-a" }));
		await act(async () => { finish(); await pending; });
		expect(controls().busy).toBe(false);
		expect(controls().submitLabel).not.toBe("Retry");
		expect(renderer!.root.findAllByProps({ title: "View Cloud sessions" })).toHaveLength(0);
	});
	it.each(["success", "failure"])("keeps a different project draft after the old project's late %s", async (outcome) => {
		f.board.projects.push({ source: { kind: "cloud", id: "o1" }, value: { id: "p2", name: "Second" } });
		f.client.getProject.mockImplementation(async (_org, id) => ({ project: { ...f.project, id } }));
		let finish!: () => void;
		f.client.createSession.mockImplementationOnce(() => new Promise((resolve, reject) => {
			finish = () => outcome === "success" ? resolve({ session: { id: "old-worker" } }) : reject(new TypeError("Old response lost"));
		}));
		const source = createCloudSessionSource({ client: f.client as never, orgId: "o1", scopeKey: "account-one", isCurrent: () => f.current });
		f.spawn.mockImplementation(async (_destination, input) => ({ ...await source.createSession(input), projectId: input.projectId }));
		await mount();
		await act(async () => renderer!.root.findByType("Prompt" as never).props.onChangeText("First project task"));
		let pending!: Promise<void>;
		await act(async () => { pending = controls().onSpawn(); });
		await act(async () => controls().onSelectProject("p2"));
		await act(async () => renderer!.root.findByType("Prompt" as never).props.onChangeText("Keep the second project's draft"));
		await act(async () => { finish(); await pending; });
		expect(controls().projectId).toBe("p2"); expect(controls().busy).toBe(false);
		expect(renderer!.root.findByType("Prompt" as never).props.value).toBe("Keep the second project's draft");
		expect(f.back).not.toHaveBeenCalled(); expect(f.callbacks).toHaveLength(0);
		expect(controls().submitLabel).not.toBe("Retry");
		expect(renderer!.root.findAllByType("Text" as never).map(node => node.props.children).join(" ")).not.toContain("Old response lost");
		const old = await readCloudOperation("account-one", "worker-create/p1");
		expect(old).toMatchObject({ payload: { projectId: "p1", prompt: "First project task" }, ...(outcome === "success" ? { acceptedId: "old-worker" } : {}) });
		await act(async () => controls().onSpawn());
		expect(f.client.createSession.mock.calls[1][1]).toMatchObject({ projectId: "p2", prompt: "Keep the second project's draft" });
		expect(f.client.createSession.mock.calls[1][2].idempotencyKey).not.toBe(f.client.createSession.mock.calls[0][2].idempotencyKey);
	});
	it("ignores a scheduled worker navigation after choosing another project", async () => {
		f.board.projects.push({ source: { kind: "cloud", id: "o1" }, value: { id: "p2", name: "Second" } });
		await mount(); await act(async () => controls().onSpawn());
		expect(f.callbacks).toHaveLength(1);
		await act(async () => controls().onSelectProject("p2"));
		await act(async () => f.callbacks.forEach((callback) => callback()));
		expect(f.push).not.toHaveBeenCalled();
	});
	it("finishes the destination catalog load after choosing a different project", async () => {
		f.board.projects.push({ source: { kind: "cloud", id: "o1" }, value: { id: "p2", name: "Second" } });
		f.client.getProject.mockImplementation(async (_org, id) => ({ project: { ...f.project, id } }));
		let finish!: (connections: Array<{ provider: string; label: string; validationState: string }>) => void;
		f.client.listUserProviderConnections.mockImplementationOnce(() => new Promise((resolve) => { finish = resolve; }));
		await mount();
		await act(async () => controls().onSelectProject("p2"));
		await act(async () => finish([{ provider: "codex", label: "default", validationState: "valid" }]));
		expect(controls().agents).toEqual([expect.objectContaining({ id: "codex" })]);
		expect(controls().projectId).toBe("p2"); expect(controls().disabled).toBe(false);
	});
	it.each(["success", "failure"])("preserves the Local project selection after an earlier creation's late %s", async (outcome) => {
		f.available.push({ kind: "local", id: "desktop" });
		f.board.projects.push(...["local-1", "local-2"].map((id) => ({ source: { kind: "local", id: "desktop" }, value: { id, name: id } })));
		f.configForHost.mockReturnValue({ hostId: "desktop", host: "desktop.local", httpPort: 3000, password: "paired" } as never);
		const agents = [{ id: "codex", label: "Codex" }];
		vi.mocked(getAgents).mockResolvedValue({ supported: agents, installed: agents, authorized: agents });
		vi.mocked(getSettings).mockResolvedValue({ chatHarnesses: ["codex"] } as never);
		vi.mocked(getAgentModels).mockResolvedValue({ models: [] } as never);
		vi.mocked(getProject).mockImplementation(async (_config, id) => ({ id, name: id, kind: "single_repo", config: {} } as never));
		let finish!: () => void;
		f.spawn.mockImplementationOnce(() => new Promise((resolve, reject) => {
			finish = () => outcome === "success" ? resolve({ id: "local-worker", projectId: "local-1" }) : reject(new Error("Old Local failure"));
		}));
		await mount();
		await act(async () => controls().onSelectDestination({ kind: "local", id: "desktop" }));
		await act(async () => controls().onSelectProject("local-1"));
		let pending!: Promise<void>; await act(async () => { pending = controls().onSpawn(); });
		await act(async () => controls().onSelectProject("local-2"));
		await act(async () => renderer!.root.findByType("Prompt" as never).props.onChangeText("Keep Local draft"));
		await act(async () => { finish(); await pending; });
		expect(controls().projectId).toBe("local-2"); expect(controls().busy).toBe(false);
		expect(renderer!.root.findByType("Prompt" as never).props.value).toBe("Keep Local draft");
		expect(f.back).not.toHaveBeenCalled(); expect(f.callbacks).toHaveLength(0);
		expect(renderer!.root.findAllByType("Text" as never).map((node) => node.props.children).join(" ")).not.toContain("Old Local failure");
		expect(f.client.createSession).not.toHaveBeenCalled();
	});
	it("restores the pending worker and uses its original request ID", async () => {
		await reserveCloudOperation("worker-create/p1", prepareCloudOperation(undefined, "account-one", { projectId: "p1", kind: "worker", harness: "codex", prompt: "Inspect", displayName: "Inspect", mode: "trusted", deniedCommands: [], model: "frozen", reasoningEffort: "low" }, () => "original"));
		await mount(); expect(controls().modelLabel).toBe("frozen");
		await act(async () => controls().onSpawn());
		expect(f.spawn).toHaveBeenCalledWith({ kind: "cloud", id: "o1" }, expect.objectContaining({ prompt: "Inspect", model: "frozen", reasoningEffort: "low", clientRequestId: "original" }));
	});
	it("opens an accepted worker without creating again", async () => {
		const operation = { ...prepareCloudOperation(undefined, "account-one", { projectId: "p1", kind: "worker", harness: "codex", prompt: "Inspect", mode: "trusted", deniedCommands: [] }, () => "original"), acceptedId: "accepted" };
		await reserveCloudOperation("worker-create/p1", operation);
		f.client.getProject.mockRejectedValue(new Error("Project settings unavailable"));
		f.client.listUserProviderConnections.mockRejectedValue(new Error("Agent settings unavailable"));
		await mount();
		expect(controls().submitLabel).toBe("Open worker"); expect(controls().disabled).toBe(false);
		await act(async () => controls().onSpawn());
		expect(f.spawn).not.toHaveBeenCalled(); f.callbacks.forEach((fn) => fn());
		expect(f.push).toHaveBeenCalledWith(expect.objectContaining({ params: expect.objectContaining({ id: "accepted" }) }));
	});
	it("does not navigate with a response from a replaced account", async () => {
		let resolve!: (v: unknown) => void; f.spawn.mockImplementation(() => new Promise((done) => { resolve = done; }));
		await mount(); await act(async () => controls().onSpawn()); f.current = false;
		await act(async () => resolve({ id: "late", projectId: "p1" }));
		expect(f.back).not.toHaveBeenCalled(); expect(f.callbacks).toHaveLength(0);
	});
});
