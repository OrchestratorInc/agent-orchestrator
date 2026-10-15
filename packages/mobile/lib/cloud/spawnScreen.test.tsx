import { act, create, type ReactTestRenderer } from "react-test-renderer";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
const f = vi.hoisted(() => ({
	storage: new Map<string, string>(), current: true, push: vi.fn(), back: vi.fn(), callbacks: [] as Array<() => void>, spawn: vi.fn(),
	project: { id: "p1", displayName: "Demo", config: { worker: { agent: "codex", agentConfig: { model: "configured", reasoningEffort: "high" } } } },
	client: { getProject: vi.fn(), listUserProviderConnections: vi.fn() },
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
vi.mock("react-native", () => ({ View: "View", Text: "Text", TextInput: "TextInput", ScrollView: "ScrollView", Pressable: "Pressable", Platform: { OS: "ios" }, StyleSheet: { create: (v: unknown) => v }, InteractionManager: { runAfterInteractions: (fn: () => void) => f.callbacks.push(fn) }, Alert: { alert: vi.fn() } }));
vi.mock("react-native-keyboard-controller", () => ({ KeyboardStickyView: "KeyboardStickyView", useKeyboardState: () => 0 }));
vi.mock("@expo/ui/community/bottom-sheet", () => ({ default: "BottomSheet", BottomSheetView: "BottomSheetView" }));
vi.mock("expo-document-picker", () => ({ getDocumentAsync: vi.fn() }));
vi.mock("expo-file-system", () => ({ File: class {} }));
vi.mock("../icons", () => ({ Feather: "Feather" }));
vi.mock("../api", () => ({ ApiError: class extends Error {}, getAgentModels: vi.fn(), getAgents: vi.fn(), getProject: vi.fn(), getSettings: vi.fn() }));
vi.mock("../store", () => ({ useApp: () => ({ scopedBoard: f.board, availableSources: f.available, sourceFor: f.sourceFor, spawnOn: f.spawn, configForHost: f.configForHost, hostStates: f.hostStates, configResolved: true, switchHost: vi.fn(), setActiveProject: vi.fn() }) }));
vi.mock("./authStore", () => ({ useCloudAuth: () => ({ client: f.client, orgId: "o1", isCurrentSession: f.isCurrent }) }));
vi.mock("../ThemeProvider", () => ({ useTheme: () => ({}), useThemedStyles: (fn: (v: unknown) => unknown) => fn({}) }));
vi.mock("../haptics", () => ({ haptics: { tap: vi.fn(), success: vi.fn(), error: vi.fn() } }));
vi.mock("../voice/useVoiceInput", () => ({ useVoiceInput: () => ({ state: "idle", mode: "push", pressIn: vi.fn(), pressOut: vi.fn() }) }));
vi.mock("../spawn-prompt-input", () => ({ SpawnPromptInput: "Prompt" }));
vi.mock("../spawn-composer-controls", () => ({ SpawnComposerControls: "Controls" }));
vi.mock("../ui", () => ({ Button: "Button" }));
vi.mock("../RouteErrorBoundary", () => ({ SheetErrorBoundary: "Boundary" }));
import SpawnModal from "../../app/spawn";
import { prepareCloudOperation, reserveCloudOperation } from "./operation";
let renderer: ReactTestRenderer | undefined;
const mount = async () => { await act(async () => { renderer = create(<SpawnModal />); }); };
const controls = () => renderer!.root.findByType("Controls" as never).props;
beforeEach(() => {
	vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true);
	vi.spyOn(console, "error").mockImplementation(() => {});
	f.storage.clear(); f.current = true; f.callbacks = []; f.push.mockReset(); f.back.mockReset(); f.spawn.mockReset().mockResolvedValue({ id: "s1", projectId: "p1" });
	f.sourceFor.mockImplementation(() => f.source); f.isCurrent.mockImplementation(() => f.current);
	f.available = [{ kind: "cloud", id: "o1" }]; f.hostStates = [];
	f.client.getProject.mockReset().mockResolvedValue({ project: f.project });
	f.client.listUserProviderConnections.mockReset().mockResolvedValue([{ provider: "codex", label: "default", validationState: "valid" }]);
});
afterEach(async () => { if (renderer) await act(async () => renderer!.unmount()); renderer = undefined; vi.restoreAllMocks(); vi.unstubAllGlobals(); });
describe("Cloud spawn sheet", () => {
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
	it("restores the pending worker and uses its original request ID", async () => {
		await reserveCloudOperation("worker-create/p1", prepareCloudOperation(undefined, "account-one", { projectId: "p1", kind: "worker", harness: "codex", prompt: "Inspect", displayName: "Inspect", mode: "trusted", deniedCommands: [], model: "frozen", reasoningEffort: "low" }, () => "original"));
		await mount(); expect(controls().modelLabel).toBe("frozen");
		await act(async () => controls().onSpawn());
		expect(f.spawn).toHaveBeenCalledWith({ kind: "cloud", id: "o1" }, expect.objectContaining({ prompt: "Inspect", model: "frozen", reasoningEffort: "low", clientRequestId: "original" }));
	});
	it("opens an accepted worker without creating again", async () => {
		const operation = { ...prepareCloudOperation(undefined, "account-one", { projectId: "p1", kind: "worker", harness: "codex", prompt: "Inspect", mode: "trusted", deniedCommands: [] }, () => "original"), acceptedId: "accepted" };
		await reserveCloudOperation("worker-create/p1", operation);
		await mount(); const open = renderer!.root.findAllByType("Button" as never).find((node) => node.props.title === "Open worker");
		expect(open).toBeDefined(); await act(async () => open!.props.onPress());
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
