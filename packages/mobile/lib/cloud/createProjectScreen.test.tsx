import { act, create, type ReactTestRenderer } from "react-test-renderer";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const state = vi.hoisted(() => ({
	replace: vi.fn(),
	refreshSource: vi.fn(async () => {}),
	environment: "cloud" as "cloud" | "local",
	signedIn: null as boolean | null,
	orgId: null as string | null,
	orgLoading: true,
	accountId: "user-1",
	baseUrl: "https://cloud.test",
	sessionEpoch: 0,
	offering: null as { providers: string[]; defaultProvider: string; kind: string; source: string } | null,
	orgError: null as string | null,
	listUserProviderConnections: vi.fn(async () => []),
	listProviderConnections: vi.fn(async () => []),
	listGitHubInstallations: vi.fn(async () => [] as unknown[]),
	listGitHubRepositories: vi.fn(async () => ({ items: [] as unknown[], page: { hasMore: false } })),
	startGitHubInstallation: vi.fn(async () => ({ installationUrl: "https://github.com/apps/ao/installations/new" })),
	syncGitHubInstallation: vi.fn(async () => ({})),
	createProjectFromGitHub: vi.fn(async (_org: string, _input: unknown, _options: unknown) => ({ project: { id: "project-1" } })),
	createProject: vi.fn(async (_org: string, _input: unknown, _options: unknown) => ({ project: { id: "project-2" } })),
	getOrgCoderConfig: vi.fn(async () => ({ configured: false })),
	listCoderTemplates: vi.fn(async () => ({ templates: [] })),
	getCoderTemplateParameters: vi.fn(async () => ({ parameters: [] })),
	listProjects: vi.fn(async () => ({ items: [], page: { hasMore: false } })),
	validateSavedRepositoryAccess: vi.fn(async () => ({ writeAccess: true })),
	putGitHubPAT: vi.fn(async () => ({})),
	hardwareBack: null as (() => boolean) | null,
}));
const storage = vi.hoisted(() => ({ data: new Map<string, string>(), getItem: vi.fn(), setItem: vi.fn() }));
vi.mock("@react-native-async-storage/async-storage", () => ({ default: {
	getItem: storage.getItem,
	setItem: storage.setItem,
	removeItem: async (key: string) => { storage.data.delete(key); },
	getAllKeys: async () => [...storage.data.keys()],
} }));
vi.mock("@expo/ui", async () => {
	const React = await import("react");
	return { Host: "Host", Picker: Object.assign(({ children, ...props }: { children?: React.ReactNode }) => React.createElement("Picker", props, children), { Item: "PickerItem" }) };
});
vi.mock("@expo/ui/community/menu", () => ({ MenuView: "MenuView" }));

vi.mock("react-native", () => ({
	ActivityIndicator: "ActivityIndicator",
	Image: "Image",
	KeyboardAvoidingView: "KeyboardAvoidingView",
	Platform: { OS: "ios" },
	Pressable: "Pressable",
	ScrollView: "ScrollView",
	Keyboard: { dismiss: vi.fn() },
	BackHandler: { addEventListener: (_event: string, listener: () => boolean) => { state.hardwareBack = listener; return { remove: () => { state.hardwareBack = null; } }; } },
	StyleSheet: { create: (styles: unknown) => styles },
	Text: "Text",
	TextInput: "TextInput",
	View: "View",
	AppState: { addEventListener: () => ({ remove: () => {} }) },
}));
vi.mock("react-native-safe-area-context", () => ({ useSafeAreaInsets: () => ({ bottom: 0 }) }));
vi.mock("expo-router", () => ({ useRouter: () => ({ back: vi.fn(), replace: state.replace }) }));
vi.mock("expo-crypto", () => ({ randomUUID: () => "test-key" }));
vi.mock("../icons", () => ({ Feather: "Feather" }));
vi.mock("@expo/vector-icons", () => ({ Feather: "Feather" }));
vi.mock("../ThemeProvider", () => {
	const theme = Object.fromEntries(["bgSurface", "blue", "borderSubtle", "textPrimary", "textSecondary", "textTertiary", "borderDefault", "bgElevated", "amber", "red", "tintBlue"].map((key) => [key, "#000"]));
	return { useTheme: () => theme, useThemeState: () => ({ scheme: "dark" }), useThemedStyles: (makeStyles: (theme: unknown) => unknown) => makeStyles(theme) };
});
vi.mock("./authStore", () => ({ useCloudAuth: () => { const epoch = state.sessionEpoch; return { ...state, client: state, isCurrentSession: () => state.sessionEpoch === epoch }; } }));
vi.mock("../store", () => ({ useApp: () => ({ refreshSource: state.refreshSource }) }));
vi.mock("../ui", () => ({ Button: "Button", HeaderIconButton: "HeaderIconButton" }));
vi.mock("./CloudAgentConnections", () => ({ CloudAgentConnections: "AgentConnections" }));
vi.mock("../haptics", () => ({ haptics: { tap: vi.fn(), select: vi.fn() } }));
vi.mock("../openGitHub", () => ({ openGitHub: vi.fn() }));
vi.mock("../RouteErrorBoundary", () => ({ SheetErrorBoundary: "SheetErrorBoundary" }));
vi.mock("../harnessLogoAssets", () => ({ logoFor: (harness: string) => `asset-${harness}` }));

import CreateCloudProject from "../../app/create-project";
import { openGitHub } from "../openGitHub";
import { CloudApiError } from "@aoagents/cloud-client";

let renderer: ReactTestRenderer | undefined;
let sessionEpoch = 0;

function deferred<T>() {
	let resolve!: (value: T) => void;
	let reject!: (reason: unknown) => void;
	const promise = new Promise<T>((done, fail) => { resolve = done; reject = fail; });
	return { promise, resolve, reject };
}

function connectedRepository() {
	state.signedIn = true; state.orgId = "org-1"; state.orgLoading = false;
	state.listGitHubInstallations.mockResolvedValue([{ id: "install-1", accountLogin: "me", status: "active", syncStatus: "ready" }] as never);
	state.listGitHubRepositories.mockResolvedValue({ items: [
		{ githubRepositoryId: "1", name: "alpha", fullName: "me/alpha", htmlUrl: "https://github.com/me/alpha", defaultBranch: "main", access: "active", isArchived: false },
	], page: { hasMore: false } } as never);
	state.listUserProviderConnections.mockResolvedValue([{ provider: "codex", label: "default", validationState: "valid" }] as never);
}

async function selectRepository(root: ReactTestRenderer["root"]) {
	await act(async () => root.findByProps({ accessibilityLabel: "Select a repository", accessibilityRole: "combobox" }).props.onPress());
	await act(async () => root.findByProps({ accessibilityLabel: "Search repositories" }).props.onChangeText("alpha"));
	await act(async () => root.findByProps({ accessibilityLabel: "Select me/alpha" }).props.onPress());
	await act(async () => root.findByProps({ title: "Continue" }).props.onPress());
}

async function mount() {
	await act(async () => { renderer = create(<CreateCloudProject />); });
	return renderer!.root;
}

beforeEach(() => {
	vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true);
	const error = console.error;
	vi.spyOn(console, "error").mockImplementation((...args) => {
		if (String(args[0]).startsWith("react-test-renderer is deprecated")) return;
		error(...args);
	});
	state.environment = "cloud";
	state.signedIn = null;
	state.orgId = null;
	state.orgLoading = true;
	state.orgError = null;
	state.offering = null;
	state.hardwareBack = null;
	// Leave room for tests that sign in again within the same case.
	state.sessionEpoch = sessionEpoch += 10;
	state.accountId = "user-1"; state.baseUrl = "https://cloud.test";
	storage.data.clear();
	storage.getItem.mockReset().mockImplementation(async (key: string) => storage.data.get(key) ?? null);
	storage.setItem.mockReset().mockImplementation(async (key: string, value: string) => { storage.data.set(key, value); });
	vi.clearAllMocks();
	state.listGitHubInstallations.mockResolvedValue([]);
	state.listGitHubRepositories.mockResolvedValue({ items: [], page: { hasMore: false } });
	state.listUserProviderConnections.mockResolvedValue([]);
	state.getOrgCoderConfig.mockReset().mockResolvedValue({ configured: false });
	state.listCoderTemplates.mockReset().mockResolvedValue({ templates: [] });
	state.getCoderTemplateParameters.mockReset().mockResolvedValue({ parameters: [] });
	state.listProjects.mockResolvedValue({ items: [], page: { hasMore: false } });
});

afterEach(async () => {
	if (renderer) await act(async () => renderer?.unmount());
	renderer = undefined;
	vi.useRealTimers();
	vi.restoreAllMocks();
	vi.unstubAllGlobals();
});

describe("Add Cloud project sheet", () => {
	it("uses task-style in-sheet choices and keeps worker and orchestrator selections independent", async () => {
		connectedRepository();
		state.listUserProviderConnections.mockResolvedValue([
			{ provider: "codex", label: "default", validationState: "valid" },
			{ provider: "claude-code", label: "default", validationState: "valid" },
		] as never);
		const root = await mount(); await selectRepository(root);
		await act(async () => root.findByProps({ accessibilityLabel: "Project name" }).props.onChangeText("My draft"));
		await act(async () => root.findByProps({ testID: "Worker agent" }).props.onPress());
		expect(root.findAllByProps({ accessibilityLabel: "Project name" })).toHaveLength(0);
		expect(root.findAllByProps({ title: "Create project" })).toHaveLength(0);
		expect(root.findAllByType("MenuView" as never)).toHaveLength(0);
		const claude = root.findAllByType("Pressable" as never).find(node => node.props.accessibilityState && node.findAllByType("Text" as never).some(text => text.props.children === "Claude Code"))!;
		expect(claude.props.accessibilityState.selected).toBe(false);
		expect(claude.findByType("Image" as never).props.source).toBe("asset-claude-code");
		await act(async () => claude.props.onPress());
		expect(root.findByProps({ accessibilityLabel: "Project name" }).props.value).toBe("My draft");
		expect(root.findByProps({ testID: "Worker agent" }).props.accessibilityLabel).toBe("Worker agent: Claude Code");
		expect(root.findByProps({ testID: "Orchestrator agent" }).props.accessibilityLabel).toBe("Orchestrator agent: Codex");
		await act(async () => root.findByProps({ testID: "Orchestrator agent" }).props.onPress());
		const codex = root.findAllByType("Pressable" as never).find(node => node.props.accessibilityState?.selected)!;
		expect(codex.findByType("Feather" as never).props.name).toBe("check");
		await act(async () => codex.props.onPress());
		await act(async () => root.findByProps({ title: "Create project" }).props.onPress());
		expect(state.createProjectFromGitHub).toHaveBeenCalledWith("org-1", expect.objectContaining({ displayName: "My draft", config: { worker: { agent: "claude-code" }, orchestrator: { agent: "codex" } } }), expect.anything());
	});

	it.each(["header", "hardware"])("returns from agent choices using %s Back without losing the project draft", async (back) => {
		connectedRepository(); const root = await mount(); await selectRepository(root);
		await act(async () => root.findByProps({ accessibilityLabel: "Project name" }).props.onChangeText("Keep this draft"));
		await act(async () => root.findByProps({ testID: "Orchestrator agent" }).props.onPress());
		if (back === "header") await act(async () => root.findByProps({ label: "Back" }).props.onPress());
		else await act(async () => { expect(state.hardwareBack?.()).toBe(true); });
		expect(root.findByProps({ accessibilityLabel: "Project name" }).props.value).toBe("Keep this draft");
		expect(root.findByProps({ title: "Create project" })).toBeDefined();
	});

	it("ignores a queued worker choice after opening the orchestrator picker", async () => {
		connectedRepository();
		state.listUserProviderConnections.mockResolvedValue([
			{ provider: "codex", label: "default", validationState: "valid" },
			{ provider: "claude-code", label: "default", validationState: "valid" },
		] as never);
		const root = await mount(); await selectRepository(root);
		await act(async () => root.findByProps({ testID: "Worker agent" }).props.onPress());
		const queued = root.findAllByType("Pressable" as never).find(node => node.findAllByType("Text" as never).some(text => text.props.children === "Claude Code"))!.props.onPress;
		await act(async () => root.findByProps({ label: "Back" }).props.onPress());
		await act(async () => root.findByProps({ testID: "Orchestrator agent" }).props.onPress());
		await act(async () => queued());
		expect(root.findAllByType("Text" as never).map(node => node.props.children)).toContain("Orchestrator agent");
		await act(async () => root.findByProps({ label: "Back" }).props.onPress());
		expect(root.findByProps({ testID: "Orchestrator agent" }).props.accessibilityLabel).toBe("Orchestrator agent: Codex");
		expect(root.findByProps({ testID: "Worker agent" }).props.accessibilityLabel).toBe("Worker agent: Codex");
	});

	it("shows the repository form while GitHub and coding-agent reads are still pending", async () => {
		connectedRepository();
		state.listGitHubInstallations.mockReturnValueOnce(new Promise(() => {}));
		state.listUserProviderConnections.mockReturnValueOnce(new Promise(() => {}));
		const root = await mount();
		expect(root.findAllByType("Text" as never).map(node => node.props.children)).toContain("Choose a repository");
		expect(root.findByProps({ accessibilityLabel: "Loading GitHub connection" })).toBeDefined();
		expect(root.findAllByProps({ title: "Connect GitHub" })).toHaveLength(0);
	});

	it("lets the repository stage finish without waiting for coding agents", async () => {
		connectedRepository();
		const agents = deferred<never[]>();
		state.listUserProviderConnections.mockReturnValueOnce(agents.promise);
		const root = await mount();
		await selectRepository(root);
		expect(root.findByProps({ accessibilityLabel: "Project name" }).props.value).toBe("alpha");
		expect(root.findByProps({ accessibilityLabel: "Loading coding agents" })).toBeDefined();
		expect(root.findByProps({ title: "Create project" }).props.disabled).toBe(true);
		await act(async () => agents.resolve([{ provider: "codex", label: "default", validationState: "valid" }] as never));
		expect(root.findByProps({ title: "Create project" }).props.disabled).toBe(false);
	});

	it("lets manual repository validation finish while coding-agent settings are pending", async () => {
		connectedRepository();
		state.listGitHubInstallations.mockResolvedValue([]);
		state.listUserProviderConnections.mockReturnValueOnce(new Promise(() => {}));
		const root = await mount();
		expect(root.findByProps({ title: "Set up manually with a token" }).props.disabled).toBe(false);
		await act(async () => root.findByProps({ title: "Set up manually with a token" }).props.onPress());
		await act(async () => {
			root.findByProps({ accessibilityLabel: "Repository URL" }).props.onChangeText("https://github.com/me/manual");
			root.findByProps({ accessibilityLabel: "Project name" }).props.onChangeText("Manual");
		});
		expect(root.findByProps({ title: "Check repository access" }).props.disabled).toBe(false);
		await act(async () => root.findByProps({ title: "Check repository access" }).props.onPress());
		expect(root.findByProps({ accessibilityLabel: "Loading coding agents" })).toBeDefined();
	});

	it("keeps the initial GitHub read alive when visiting manual setup and returning", async () => {
		connectedRepository();
		state.listUserProviderConnections.mockResolvedValue([{ provider: "github", validationState: "valid" }] as never);
		const installations = deferred<unknown[]>();
		state.listGitHubInstallations.mockReturnValueOnce(installations.promise);
		const root = await mount();
		await act(async () => root.findByProps({ title: "Set up manually with a token" }).props.onPress());
		await act(async () => root.findByProps({ label: "Back" }).props.onPress());
		await act(async () => installations.resolve([{ id: "install-1", accountLogin: "me", status: "active", syncStatus: "ready" }]));
		expect(root.findAllByProps({ accessibilityLabel: "Select a repository", accessibilityRole: "combobox" })).toHaveLength(1);
	});

	it("reuses recent GitHub and coding-agent results when the sheet reopens", async () => {
		connectedRepository();
		await mount();
		await act(async () => renderer!.unmount()); renderer = undefined;
		const root = await mount();
		expect(root.findByProps({ accessibilityLabel: "Select a repository", accessibilityRole: "combobox" }).props.accessibilityState.disabled).toBe(false);
		await selectRepository(root);
		expect(root.findByProps({ title: "Create project" }).props.disabled).toBe(false);
		expect(state.listGitHubInstallations).toHaveBeenCalledTimes(1);
		expect(state.listGitHubRepositories).toHaveBeenCalledTimes(1);
		expect(state.listUserProviderConnections).toHaveBeenCalledTimes(1);
	});

	it("keeps cached repositories usable while stale data refreshes in the background", async () => {
		connectedRepository();
		const now = vi.spyOn(Date, "now").mockReturnValue(100_000);
		await mount();
		await act(async () => renderer!.unmount()); renderer = undefined;
		now.mockReturnValue(170_000);
		state.listGitHubRepositories.mockReturnValueOnce(new Promise(() => {}));
		state.listUserProviderConnections.mockReturnValueOnce(new Promise(() => {}));
		const root = await mount();
		expect(root.findByProps({ accessibilityLabel: "Select a repository", accessibilityRole: "combobox" }).props.accessibilityState.disabled).toBe(false);
		await selectRepository(root);
		expect(root.findByProps({ testID: "Worker agent" }).findAllByType("Text" as never).map(node => node.props.children)).toContain("Codex");
		expect(state.listGitHubRepositories).toHaveBeenCalledTimes(2);
		expect(state.listUserProviderConnections).toHaveBeenCalledTimes(2);
	});

	it("shares an in-flight background GitHub refresh with an explicit refresh", async () => {
		connectedRepository();
		const now = vi.spyOn(Date, "now").mockReturnValue(100_000);
		await mount();
		await act(async () => renderer!.unmount()); renderer = undefined;
		now.mockReturnValue(170_000);
		const repositories = deferred<{ items: unknown[]; page: { hasMore: boolean } }>();
		state.listGitHubRepositories.mockReturnValueOnce(repositories.promise);
		const root = await mount();
		await act(async () => root.findByProps({ title: "Refresh repositories" }).props.onPress());
		await act(async () => repositories.resolve({ items: [], page: { hasMore: false } }));
		expect(state.listGitHubRepositories).toHaveBeenCalledTimes(2);
		expect(root.findAllByProps({ accessibilityLabel: "Select a repository", accessibilityRole: "combobox" })).toHaveLength(0);
	});

	it("retires a pre-sync repository read so successful sync gets post-mutation grants", async () => {
		connectedRepository();
		const ready = { id: "ready", accountLogin: "me", status: "active", syncStatus: "ready" };
		const failed = { id: "failed", accountLogin: "team", status: "active", syncStatus: "failed" };
		state.listGitHubInstallations.mockResolvedValue([ready, failed] as never);
		const now = vi.spyOn(Date, "now").mockReturnValue(100_000);
		await mount();
		await act(async () => renderer!.unmount()); renderer = undefined;
		now.mockReturnValue(170_000);
		const oldRepositories = deferred<{ items: unknown[]; page: { hasMore: boolean } }>();
		state.listGitHubRepositories.mockReturnValueOnce(oldRepositories.promise);
		const root = await mount();
		const sync = deferred<object>();
		const duringSync = deferred<{ items: unknown[]; page: { hasMore: boolean } }>();
		state.syncGitHubInstallation.mockReturnValueOnce(sync.promise);
		state.listGitHubRepositories.mockReturnValueOnce(duringSync.promise);
		await act(async () => root.findByProps({ title: "Retry repository sync" }).props.onPress());
		// A foreground/manual read may begin before the mutation finishes.
		await act(async () => root.findByProps({ title: "Refresh repositories" }).props.onPress());
		state.listGitHubInstallations.mockResolvedValue([ready, { ...failed, syncStatus: "ready" }] as never);
		state.listGitHubRepositories.mockResolvedValue({ items: [
			{ githubRepositoryId: "2", name: "beta", fullName: "team/beta", htmlUrl: "https://github.com/team/beta", defaultBranch: "main", access: "active", isArchived: false },
		], page: { hasMore: false } } as never);
		await act(async () => sync.resolve({}));
		await act(async () => duringSync.resolve({ items: [], page: { hasMore: false } }));
		await act(async () => oldRepositories.resolve({ items: [], page: { hasMore: false } }));
		expect(root.findAllByProps({ title: "Retry repository sync" })).toHaveLength(0);
		await act(async () => root.findByProps({ accessibilityLabel: "Select a repository", accessibilityRole: "combobox" }).props.onPress());
		await act(async () => root.findByProps({ accessibilityLabel: "Search repositories" }).props.onChangeText("beta"));
		expect(root.findByProps({ accessibilityLabel: "Select team/beta" })).toBeDefined();
	});

	it("replaces a cached default agent that is no longer connected after background refresh", async () => {
		connectedRepository();
		const now = vi.spyOn(Date, "now").mockReturnValue(100_000);
		await mount();
		await act(async () => renderer!.unmount()); renderer = undefined;
		now.mockReturnValue(170_000);
		const connections = deferred<never[]>();
		state.listUserProviderConnections.mockReturnValueOnce(connections.promise);
		const root = await mount();
		await selectRepository(root);
		await act(async () => connections.resolve([{ provider: "claude-code", label: "default", validationState: "valid" }] as never));
		expect(root.findByProps({ testID: "Worker agent" }).findAllByType("Text" as never).map(node => node.props.children)).toContain("Claude Code");
	});

	it("does not let a pre-save settings response undo a newly saved GitHub token", async () => {
		connectedRepository();
		state.baseUrl = "https://staging-api.aoagents.dev";
		state.listUserProviderConnections.mockResolvedValue([
			{ provider: "github", label: "default", validationState: "valid" },
			{ provider: "codex", label: "default", validationState: "valid" },
		] as never);
		const now = vi.spyOn(Date, "now").mockReturnValue(100_000);
		await mount();
		await act(async () => renderer!.unmount()); renderer = undefined;
		now.mockReturnValue(170_000);
		const oldSettings = deferred<never[]>();
		state.listUserProviderConnections.mockReturnValueOnce(oldSettings.promise);
		const root = await mount();
		await act(async () => root.findByProps({ title: "Change GitHub token" }).props.onPress());
		await act(async () => root.findByProps({ accessibilityLabel: "GitHub access token" }).props.onChangeText("synthetic-new-token"));
		await act(async () => root.findByProps({ title: "Save token and continue" }).props.onPress());
		await act(async () => oldSettings.resolve([]));
		expect(root.findAllByProps({ accessibilityLabel: "Repository URL" })).toHaveLength(1);
		await act(async () => renderer!.unmount()); renderer = undefined;
		const reopened = await mount();
		expect(reopened.findAllByProps({ accessibilityLabel: "Repository URL" })).toHaveLength(1);
	});

	it.each(["account", "workspace", "deployment", "login"])("does not reuse setup metadata across a different %s", async (boundary) => {
		connectedRepository();
		await mount();
		await act(async () => renderer!.unmount()); renderer = undefined;
		if (boundary === "account") state.accountId = "user-2";
		if (boundary === "workspace") state.orgId = "org-2";
		if (boundary === "deployment") state.baseUrl = "https://other-cloud.test";
		if (boundary === "login") state.sessionEpoch++;
		state.listGitHubInstallations.mockReturnValueOnce(new Promise(() => {}));
		state.listUserProviderConnections.mockReturnValueOnce(new Promise(() => {}));
		const root = await mount();
		expect(root.findAllByProps({ accessibilityLabel: "Select a repository", accessibilityRole: "combobox" })).toHaveLength(0);
		expect(root.findAllByType("Text" as never).map(node => node.props.children)).toContain("Choose a repository");
	});

	it("keeps GitHub search and the project draft usable when agent settings fail", async () => {
		connectedRepository();
		state.listUserProviderConnections.mockRejectedValueOnce(new Error("Agent settings unavailable"));
		const root = await mount();
		await selectRepository(root);
		expect(root.findByProps({ accessibilityLabel: "Project name" }).props.value).toBe("alpha");
		expect(root.findAllByType("Text" as never).map(node => node.props.children)).toContain("Agent settings unavailable");
		expect(root.findByProps({ title: "Create project" }).props.disabled).toBe(true);
		await act(async () => root.findByProps({ title: "Refresh connected agents" }).props.onPress());
		expect(root.findByProps({ accessibilityLabel: "Project name" }).props.value).toBe("alpha");
		expect(root.findByProps({ title: "Create project" }).props.disabled).toBe(false);
	});

	it("does not start a new create before the previous-request journal has loaded", async () => {
		connectedRepository();
		const journal = deferred<string | null>();
		storage.getItem.mockReturnValueOnce(journal.promise);
		const root = await mount();
		await selectRepository(root);
		expect(root.findByProps({ title: "Create project" }).props.disabled).toBe(true);
		await act(async () => root.findByProps({ title: "Create project" }).props.onPress());
		expect(state.createProjectFromGitHub).not.toHaveBeenCalled();
		await act(async () => journal.resolve(null));
		expect(root.findByProps({ title: "Create project" }).props.disabled).toBe(false);
	});

	it("keeps the draft but blocks creating after journal failure until an inline retry succeeds", async () => {
		connectedRepository();
		storage.getItem.mockRejectedValueOnce(new Error("Local request storage unavailable"));
		const root = await mount();
		await selectRepository(root);
		expect(root.findByProps({ accessibilityLabel: "Project name" }).props.value).toBe("alpha");
		expect(root.findByProps({ title: "Create project" }).props.disabled).toBe(true);
		expect(root.findAllByType("Text" as never).map(node => node.props.children)).toContain("Local request storage unavailable");
		await act(async () => root.findByProps({ title: "Retry request recovery" }).props.onPress());
		expect(root.findByProps({ accessibilityLabel: "Project name" }).props.value).toBe("alpha");
		expect(root.findByProps({ title: "Create project" }).props.disabled).toBe(false);
	});

	it("does not stop GitHub approval polling when retrying only request recovery", async () => {
		vi.useFakeTimers();
		state.signedIn = true; state.orgId = "org-1"; state.orgLoading = false;
		await mount();
		await act(async () => renderer!.unmount()); renderer = undefined;
		storage.getItem.mockRejectedValueOnce(new Error("Journal unavailable"));
		const root = await mount();
		await act(async () => root.findByProps({ title: "Connect GitHub" }).props.onPress());
		expect(root.findByProps({ title: "Stop waiting" })).toBeDefined();
		await act(async () => root.findByProps({ title: "Retry request recovery" }).props.onPress());
		state.listGitHubInstallations.mockResolvedValue([{ id: "installed", accountLogin: "me", status: "active", syncStatus: "ready", updatedAt: "2026-10-20T00:00:00Z" }] as never);
		await act(async () => { await vi.advanceTimersByTimeAsync(2600); });
		expect(root.findAllByProps({ title: "Stop waiting" })).toHaveLength(0);
		expect(root.findAllByType("Text" as never).map(node => node.props.children)).toContain("Connected to me");
	});

	it("refreshes repositories explicitly even when the cached result is fresh", async () => {
		connectedRepository();
		const root = await mount();
		state.listGitHubRepositories.mockResolvedValue({ items: [
			{ githubRepositoryId: "2", name: "beta", fullName: "me/beta", htmlUrl: "https://github.com/me/beta", defaultBranch: "main", access: "active", isArchived: false },
		], page: { hasMore: false } } as never);
		await act(async () => root.findByProps({ title: "Refresh repositories" }).props.onPress());
		await act(async () => root.findByProps({ accessibilityLabel: "Select a repository", accessibilityRole: "combobox" }).props.onPress());
		await act(async () => root.findByProps({ accessibilityLabel: "Search repositories" }).props.onChangeText("beta"));
		expect(root.findByProps({ accessibilityLabel: "Select me/beta" })).toBeDefined();
		expect(state.listGitHubRepositories).toHaveBeenCalledTimes(2);
	});

	it("does not populate a reopened sheet from responses arriving after it closed", async () => {
		connectedRepository();
		const repositories = deferred<{ items: unknown[]; page: { hasMore: boolean } }>();
		const connections = deferred<never[]>();
		state.listGitHubRepositories.mockReturnValueOnce(repositories.promise);
		state.listUserProviderConnections.mockReturnValueOnce(connections.promise);
		await mount();
		await act(async () => renderer!.unmount()); renderer = undefined;
		await act(async () => {
			repositories.resolve({ items: [{ githubRepositoryId: "old", name: "old", fullName: "me/old", access: "active", isArchived: false }], page: { hasMore: false } });
			connections.resolve([{ provider: "claude-code", label: "default", validationState: "valid" }] as never);
		});
		state.listGitHubRepositories.mockResolvedValue({ items: [], page: { hasMore: false } });
		const root = await mount();
		expect(root.findAllByProps({ accessibilityLabel: "Select a repository", accessibilityRole: "combobox" })).toHaveLength(0);
		expect(state.listGitHubRepositories).toHaveBeenCalledTimes(2);
		expect(state.listUserProviderConnections).toHaveBeenCalledTimes(2);
	});
	it("clears manual credentials and repository state on account replacement", async () => {
		state.signedIn = true; state.orgId = "org-1"; state.orgLoading = false;
		const root = await mount();
		await act(async () => root.findByProps({ title: "Set up manually with a token" }).props.onPress());
		act(() => root.findByProps({ accessibilityLabel: "GitHub access token" }).props.onChangeText("synthetic-secret-a"));
		state.accountId = "user-2"; state.sessionEpoch++;
		await act(async () => renderer!.update(<CreateCloudProject />));
		await act(async () => root.findByProps({ title: "Set up manually with a token" }).props.onPress());
		expect(root.findByProps({ accessibilityLabel: "GitHub access token" }).props.value).toBe("");
	});
	it("ignores a late old-account PAT save rather than advancing the new form", async () => {
		state.signedIn = true; state.orgId = "org-1"; state.orgLoading = false;
		const root = await mount(); await act(async () => root.findByProps({ title: "Set up manually with a token" }).props.onPress());
		act(() => root.findByProps({ accessibilityLabel: "GitHub access token" }).props.onChangeText("synthetic-secret-a"));
		let resolve!: (value: object) => void; state.putGitHubPAT.mockImplementationOnce(() => new Promise((done) => { resolve = done; }));
		let saving!: Promise<void>; await act(async () => { saving = root.findByProps({ title: "Save token and continue" }).props.onPress(); });
		state.accountId = "user-2"; state.sessionEpoch++;
		await act(async () => renderer!.update(<CreateCloudProject />));
		await act(async () => { resolve({}); await saving; });
		expect(root.findAllByProps({ accessibilityLabel: "Repository URL" })).toHaveLength(0);
	});
	it("uses manual import directly for the verified staging deployment", async () => {
		state.signedIn = true; state.orgId = "org-1"; state.orgLoading = false; state.baseUrl = "https://staging-api.aoagents.dev";
		const root = await mount();
		expect(root.findAllByProps({ title: "Connect GitHub" })).toHaveLength(0);
		expect(root.findAllByProps({ accessibilityLabel: "GitHub access token" })).toHaveLength(1);
		expect(state.startGitHubInstallation).not.toHaveBeenCalled();
	});
	async function manualReview() {
		state.signedIn = true; state.orgId = "org-1"; state.orgLoading = false;
		state.listUserProviderConnections.mockResolvedValue([
			{ provider: "github", label: "default", validationState: "valid" },
			{ provider: "codex", label: "default", validationState: "valid" },
		] as never);
		const root = await mount();
		await act(async () => { root.findByProps({ title: "Set up manually with a token" }).props.onPress(); });
		await act(async () => {
			root.findByProps({ accessibilityLabel: "Repository URL" }).props.onChangeText("https://github.com/me/manual");
			root.findByProps({ accessibilityLabel: "Project name" }).props.onChangeText("Manual");
		});
		await act(async () => { await root.findByProps({ title: "Check repository access" }).props.onPress(); });
		return root;
	}
	it("keeps a saved project retry payload and request key through agent connection", async () => {
		const root = await manualReview();
		state.createProject.mockRejectedValueOnce(new TypeError("Response lost"));
		await act(async () => root.findByProps({ title: "Create project" }).props.onPress());
		const original = state.createProject.mock.calls[0];
		await act(async () => root.findByProps({ title: "Connect coding agents" }).props.onPress());
		const setup = root.findByType("AgentConnections" as never);
		state.listUserProviderConnections.mockResolvedValue([{ provider: "claude-code", label: "default", validationState: "valid" }, { provider: "codex", label: "default", validationState: "valid" }] as never);
		await act(async () => setup.props.onChanged());
		expect(state.createProject).toHaveBeenCalledOnce();
		await act(async () => setup.props.onBack());
		await act(async () => root.findByProps({ title: "Retry creation" }).props.onPress());
		expect(state.createProject.mock.calls[1].slice(0, 2)).toEqual(original.slice(0, 2));
		expect(state.createProject.mock.calls[1][2]).toMatchObject({ idempotencyKey: "test-key" });
	});
	it("keeps accepted creation separate from failed board refresh", async () => {
		const root = await manualReview();
		state.refreshSource.mockRejectedValueOnce(new Error("Board unavailable"));
		await act(async () => { await root.findByProps({ title: "Create project" }).props.onPress(); });
		expect(state.createProject).toHaveBeenCalledOnce();
		expect(state.replace).toHaveBeenCalledWith(expect.objectContaining({ params: expect.objectContaining({ id: "project-2" }) }));
	});
	it("stops before the API write when saving the retry record fails", async () => {
		const root = await manualReview();
		storage.setItem.mockRejectedValueOnce(new Error("Storage unavailable"));
		await act(async () => { await root.findByProps({ title: "Create project" }).props.onPress(); });
		expect(state.createProject).not.toHaveBeenCalled();
		expect(root.findAllByType("Text" as never).map((node) => node.props.children)).toContain("Storage unavailable");
	});
	it("does not navigate after the form closes while a create is in flight", async () => {
		const root = await manualReview();
		let finish!: (value: { project: { id: string } }) => void;
		state.createProject.mockImplementationOnce(() => new Promise((resolve) => { finish = resolve; }));
		await act(async () => { void root.findByProps({ title: "Create project" }).props.onPress(); });
		await act(async () => { renderer!.unmount(); }); renderer = undefined;
		await act(async () => { finish({ project: { id: "late" } }); });
		expect(state.replace).not.toHaveBeenCalled();
	});
	it("restores an uncertain request on remount and retries the same frozen request", async () => {
		const root = await manualReview();
		state.createProject.mockRejectedValueOnce(new Error("Request timed out"));
		await act(async () => { await root.findByProps({ title: "Create project" }).props.onPress(); });
		const first = state.createProject.mock.calls[0];
		await act(async () => { renderer!.unmount(); }); renderer = undefined;
		const reopened = await mount();
		await act(async () => { await reopened.findByProps({ title: "Retry creation" }).props.onPress(); });
		expect(state.createProject.mock.calls[1]?.slice(0, 2)).toEqual(first?.slice(0, 2));
		expect(state.createProject.mock.calls[1]?.[2]).toMatchObject({ idempotencyKey: "test-key" });
	});
	it("requires a BYO Coder template but allows managed Default", async () => {
		state.offering = { providers: ["coder"], defaultProvider: "coder", kind: "b2b", source: "server" };
		state.getOrgCoderConfig.mockResolvedValueOnce({ configured: true } as never);
		state.listCoderTemplates.mockResolvedValueOnce({ templates: [{ id: "template-1", name: "dev", displayName: "Dev", parameters: [] }] } as never);
		const root = await manualReview();
		expect(root.findByProps({ title: "Create project" }).props.disabled).toBe(true);
		await act(async () => { root.findByProps({ testID: "Coder template" }).props.onValueChange("template-1"); });
		expect(root.findByProps({ title: "Create project" }).props.disabled).toBe(false);
	});
	it("requires a managed Coder template when the deployment offers choices and submits the selection", async () => {
		state.offering = { providers: ["coder"], defaultProvider: "coder", kind: "b2b", source: "server" };
		state.listCoderTemplates.mockResolvedValueOnce({ templates: [{ id: "managed-template", name: "managed", displayName: "Managed", parameters: [] }] } as never);
		const root = await manualReview();
		expect(root.findByProps({ title: "Create project" }).props.disabled).toBe(true);
		await act(async () => root.findByProps({ testID: "Coder template" }).props.onValueChange("managed-template"));
		expect(root.findByProps({ title: "Create project" }).props.disabled).toBe(false);
		await act(async () => root.findByProps({ title: "Create project" }).props.onPress());
		expect((state.createProject.mock.calls[0]?.[1] as { config: object }).config).toMatchObject({ coder: { templateId: "managed-template" } });
	});
	it("opens an accepted project after remount without sending another create request", async () => {
		const root = await manualReview();
		state.replace.mockImplementationOnce(() => { throw new Error("Navigation unavailable"); });
		await act(async () => { await root.findByProps({ title: "Create project" }).props.onPress(); });
		await act(async () => { renderer!.unmount(); }); renderer = undefined;
		const reopened = await mount();
		await act(async () => { await reopened.findByProps({ title: "Open project" }).props.onPress(); });
		expect(state.createProject).toHaveBeenCalledOnce();
		expect(state.replace).toHaveBeenLastCalledWith(expect.objectContaining({ params: expect.objectContaining({ id: "project-2" }) }));
	});
	it("opens an accepted project even while GitHub and coding-agent requests hang", async () => {
		const root = await manualReview();
		state.replace.mockImplementationOnce(() => { throw new Error("Navigation unavailable"); });
		await act(async () => root.findByProps({ title: "Create project" }).props.onPress());
		await act(async () => renderer!.unmount()); renderer = undefined;
		state.sessionEpoch++;
		state.listGitHubInstallations.mockReturnValueOnce(new Promise(() => {}));
		state.listUserProviderConnections.mockReturnValueOnce(new Promise(() => {}));
		const reopened = await mount();
		await act(async () => reopened.findByProps({ title: "Open project" }).props.onPress());
		expect(state.createProject).toHaveBeenCalledTimes(1);
		expect(state.replace).toHaveBeenLastCalledWith(expect.objectContaining({ params: expect.objectContaining({ id: "project-2" }) }));
	});
	it("does not overwrite a restored request's frozen agents when settings arrive later", async () => {
		const root = await manualReview();
		state.createProject.mockRejectedValueOnce(new Error("Request timed out"));
		await act(async () => root.findByProps({ title: "Create project" }).props.onPress());
		await act(async () => renderer!.unmount()); renderer = undefined;
		state.sessionEpoch++;
		const connections = deferred<never[]>();
		state.listUserProviderConnections.mockReturnValueOnce(connections.promise);
		const reopened = await mount();
		await act(async () => connections.resolve([
			{ provider: "claude-code", label: "default", validationState: "valid" },
			{ provider: "codex", label: "default", validationState: "valid" },
		] as never));
		expect(reopened.findByProps({ testID: "Worker agent" }).findAllByType("Text" as never).map(node => node.props.children)).toContain("Codex");
		await act(async () => reopened.findByProps({ title: "Retry creation" }).props.onPress());
		expect(state.createProject.mock.calls[1]?.[1]).toMatchObject({ config: { worker: { agent: "codex" }, orchestrator: { agent: "codex" } } });
	});
	it("does not adopt an old account's create response", async () => {
		const root = await manualReview();
		let finish!: (value: { project: { id: string } }) => void;
		state.createProject.mockImplementationOnce(() => new Promise((resolve) => { finish = resolve; }));
		await act(async () => { void root.findByProps({ title: "Create project" }).props.onPress(); });
		state.sessionEpoch += 1;
		await act(async () => { finish({ project: { id: "old-account-project" } }); });
		expect(state.replace).not.toHaveBeenCalled();
	});
	it("opens the matching existing project on a later page after repository conflict", async () => {
		const root = await manualReview();
		state.createProject.mockRejectedValueOnce(new CloudApiError(409, { error: "Conflict", code: "project_repository_exists", message: "Project exists", requestId: "request-1" }));
		state.listProjects.mockResolvedValueOnce({ items: [{ id: "other", repositoryUrl: "https://github.com/me/other" }], page: { hasMore: true, nextCursor: "next" } } as never)
			.mockResolvedValueOnce({ items: [{ id: "existing", repositoryUrl: "https://github.com/me/manual.git" }], page: { hasMore: false } } as never);
		await act(async () => { await root.findByProps({ title: "Create project" }).props.onPress(); });
		expect(state.createProject).toHaveBeenCalledOnce();
		expect(state.replace).toHaveBeenCalledWith(expect.objectContaining({ params: expect.objectContaining({ id: "existing" }) }));
	});
	it.each(["freestyle", "coder"])("inherits the %s Settings default without offering a provider choice", async (defaultProvider) => {
		state.offering = { providers: ["freestyle", "coder"], defaultProvider, kind: "both", source: "server" };
		const root = await manualReview();
		expect(root.findAllByProps({ testID: "Cloud provider" })).toHaveLength(0);
		if (defaultProvider === "coder") expect(root.findByProps({ testID: "Coder template" })).toBeDefined();
		else expect(root.findAllByProps({ testID: "Coder template" })).toHaveLength(0);
		await act(async () => root.findByProps({ title: "Create project" }).props.onPress());
		expect(state.createProject).toHaveBeenCalledWith("org-1", expect.objectContaining({ config: expect.objectContaining({ sandboxProvider: defaultProvider }) }), expect.anything());
	});
	it("uses a refreshed organization default without losing an open project draft", async () => {
		state.offering = { providers: ["freestyle", "coder"], defaultProvider: "freestyle", kind: "both", source: "server" };
		const root = await manualReview();
		state.offering = { ...state.offering, defaultProvider: "coder" };
		await act(async () => { renderer!.update(<CreateCloudProject />); });
		expect(root.findByProps({ title: "Create project" }).props.disabled).toBe(false);
		await act(async () => root.findByProps({ title: "Create project" }).props.onPress());
		expect(state.createProject).toHaveBeenCalledWith("org-1", expect.objectContaining({ config: expect.objectContaining({ sandboxProvider: "coder" }) }), expect.anything());
	});
	it("leaves the provider to a legacy control plane", async () => {
		state.offering = { providers: ["freestyle", "coder"], defaultProvider: "freestyle", kind: "both", source: "fallback" };
		const root = await manualReview();
		expect(root.findAllByProps({ testID: "Cloud provider" })).toHaveLength(0);
		await act(async () => root.findByProps({ title: "Create project" }).props.onPress());
		expect((state.createProject.mock.calls[0]?.[1] as { config: object }).config).not.toHaveProperty("sandboxProvider");
	});
	it.each([
		{ name: "recorded", config: { sandboxProvider: "coder", coder: { templateId: "template-1" } } },
		{ name: "legacy", config: { coder: { templateId: "template-1" } } },
	])("uses a frozen $name Coder request for compute checks and retries it unchanged", async ({ config }) => {
		connectedRepository();
		state.offering = { providers: ["freestyle"], defaultProvider: "freestyle", kind: "b2c", source: "server" };
		state.getOrgCoderConfig.mockRejectedValueOnce(new Error("Coder temporarily unavailable"));
		state.listCoderTemplates.mockResolvedValue({ templates: [{ id: "template-1", name: "dev", displayName: "Dev", parameters: [] }] } as never);
		const payload = {
			kind: "manual", repositoryUrl: "https://github.com/me/manual",
			input: { displayName: "Manual", repositoryUrl: "https://github.com/me/manual", defaultBranch: "main", config: {
				worker: { agent: "codex" }, orchestrator: { agent: "codex" }, ...config,
			} },
		};
		const scopeKey = '["https://cloud.test","user-1","org-1"]';
		storage.data.set("ao.cloud.operations.v1." + encodeURIComponent(scopeKey), JSON.stringify({
			version: 1, scopeKey, operations: { "project-create/https://github.com/me/manual": {
				scopeKey, key: "frozen-provider-key", serialized: JSON.stringify(payload), payload,
			} },
		}));
		const root = await mount();
		expect(root.findByProps({ title: "Retry creation" }).props.disabled).toBe(true);
		expect(root.findAllByType("Text" as never).map((node) => node.props.children)).toContain("Coder temporarily unavailable");
		await act(async () => root.findByProps({ title: "Retry Coder settings" }).props.onPress());
		expect(root.findByProps({ testID: "Coder template" }).props.selectedValue).toBe("template-1");
		expect(root.findByProps({ title: "Retry creation" }).props.disabled).toBe(false);
		await act(async () => root.findByProps({ title: "Retry creation" }).props.onPress());
		expect(state.createProject).toHaveBeenCalledWith("org-1", payload.input, expect.objectContaining({ idempotencyKey: "frozen-provider-key" }));
	});
	it("creates a managed Coder project with the deployment provider without a BYO template", async () => {
		state.offering = { providers: ["coder"], defaultProvider: "coder", kind: "b2b", source: "server" };
		const root = await manualReview();
		await act(async () => { await root.findByProps({ title: "Create project" }).props.onPress(); });
		expect(state.createProject).toHaveBeenCalledOnce();
		expect((state.createProject.mock.calls[0]?.[1] as { config: object }).config).not.toHaveProperty("sandboxProvider");
		expect((state.createProject.mock.calls[0]?.[1] as { config: { coder?: unknown } }).config.coder).toBeUndefined();
	});
	it("offers an inline agent retry instead of reporting missing credentials after a readiness request fails", async () => {
		connectedRepository();
		state.listUserProviderConnections.mockRejectedValueOnce(new Error("Credential service unavailable"));
		const root = await mount();
		await selectRepository(root);
		expect(root.findByProps({ title: "Refresh connected agents" })).toBeDefined();
		expect(root.findAllByType("Text" as never).map((node) => node.props.children)).toContain("Credential service unavailable");
	});
	it("keeps account resolution loading instead of showing a false sign-in error", async () => {
		const root = await mount();
		expect(root.findByProps({ accessibilityLabel: "Loading Cloud workspace" })).toBeDefined();
		expect(root.findAllByType("Text" as never).map(node => node.props.children)).toContain("Choose a repository");
		expect(root.findAll((node) => String(node.type) === "Text").map((node) => node.props.children)).not.toContain("Sign in to Orchestrator Cloud before adding a project.");
		expect(state.listUserProviderConnections).not.toHaveBeenCalled();
	});

	it("keeps the header outside the scroll area in a native sheet container", async () => {
		const root = await mount();
		const sheet = root.findByProps({ collapsable: false });
		expect(sheet.type).toBe("View");
		expect(sheet.props.collapsable).toBe(false);
		expect(sheet.findAll((node) => String(node.type) === "ScrollView")).toHaveLength(1);
		expect(sheet.children[0]).toMatchObject({ type: "View" });
		expect(sheet.children[1]).toMatchObject({ type: "ScrollView" });
	});

	it("shows the agent marks beside both worker and orchestrator choices", async () => {
		state.signedIn = true;
		state.orgId = "org-1";
		state.orgLoading = false;
		state.listUserProviderConnections.mockResolvedValueOnce([
			{ provider: "github", validationState: "valid" },
			{ provider: "claude-code", label: "default", validationState: "valid" },
		] as never);
		state.listProviderConnections.mockResolvedValueOnce([{ provider: "codex", validationState: "valid" }] as never);
		const root = await mount();
		await act(async () => { root.findByProps({ title: "Set up manually with a token" }).props.onPress(); });
		await act(async () => {
			root.findByProps({ accessibilityLabel: "Repository URL" }).props.onChangeText("https://github.com/owner/repo");
			root.findByProps({ accessibilityLabel: "Project name" }).props.onChangeText("Tappy");
		});
		await act(async () => { root.findByProps({ title: "Check repository access" }).props.onPress(); });
		const marks = root.findAll((node) => String(node.type) === "Image").map((node) => node.props.source);
		expect(marks).toEqual(["asset-claude-code", "asset-claude-code"]);
		for (const label of ["Worker agent", "Orchestrator agent"]) {
			const selector = root.findByProps({ testID: label });
			expect(selector.findAllByType("Image" as never)).toHaveLength(1);
			expect(selector.findAllByType("Text" as never).map(node => node.props.children)).toContain("Claude Code");
		}
	});

	it("connects GitHub using the selected Cloud environment and offers manual setup", async () => {
		state.signedIn = true;
		state.orgId = "org-1";
		state.orgLoading = false;
		const root = await mount();
		expect(root.findByProps({ title: "Connect GitHub" })).toBeDefined();
		await act(async () => { await root.findByProps({ title: "Connect GitHub" }).props.onPress(); });
		expect(state.startGitHubInstallation).toHaveBeenCalledWith("org-1", expect.anything());
		expect(openGitHub).toHaveBeenCalledWith("https://github.com/apps/ao/installations/new", { fromSheet: true, throwOnFailure: true });
		await act(async () => { root.findByProps({ title: "Set up manually with a token" }).props.onPress(); });
		expect(root.findByProps({ accessibilityLabel: "GitHub access token" })).toBeDefined();
	});

	it("searches granted repositories and creates an App-backed project", async () => {
		state.environment = "local"; // The old global preference must not gate a signed-in Cloud import.
		state.signedIn = true;
		state.orgId = "org-1";
		state.orgLoading = false;
		state.listGitHubInstallations.mockResolvedValue([{ id: "install-1", status: "active", syncStatus: "ready" }] as never);
		state.listGitHubRepositories.mockResolvedValue({ items: [
			{ githubRepositoryId: "1", name: "alpha", fullName: "me/alpha", htmlUrl: "https://github.com/me/alpha", defaultBranch: "main", access: "active", isArchived: false },
			{ githubRepositoryId: "2", name: "beta", fullName: "me/beta", htmlUrl: "https://github.com/me/beta", defaultBranch: "develop", access: "active", isArchived: false },
		], page: { hasMore: false } } as never);
		state.listUserProviderConnections.mockResolvedValue([{ provider: "claude-code", label: "default", validationState: "valid" }] as never);
		const root = await mount();
		expect(root.findAllByProps({ accessibilityRole: "radio" })).toHaveLength(0);
		await act(async () => { root.findByProps({ accessibilityRole: "combobox", accessibilityLabel: "Select a repository" }).props.onPress(); });
		expect(root.findAllByProps({ accessibilityRole: "radio" })).toHaveLength(0);
		await act(async () => { root.findByProps({ accessibilityLabel: "Search repositories" }).props.onChangeText("beta"); });
		expect(root.findAllByProps({ accessibilityLabel: "Select me/alpha" })).toHaveLength(0);
		await act(async () => { root.findByProps({ accessibilityLabel: "Select me/beta" }).props.onPress(); });
		expect(root.findAllByProps({ accessibilityLabel: "Search repositories" })).toHaveLength(0);
		expect(root.findAllByProps({ accessibilityRole: "radio" })).toHaveLength(0);
		expect(root.findByProps({ accessibilityRole: "combobox", accessibilityLabel: "Select a repository" }).props.accessibilityValue).toEqual({ text: "me/beta" });
		await act(async () => { root.findByProps({ title: "Continue" }).props.onPress(); });
		await act(async () => { await root.findByProps({ title: "Create project" }).props.onPress(); });
		expect(state.createProjectFromGitHub).toHaveBeenCalledWith("org-1", {
			githubRepositoryId: "2",
			displayName: "beta",
			config: { worker: { agent: "claude-code" }, orchestrator: { agent: "claude-code" } },
		}, expect.objectContaining({ idempotencyKey: "test-key", signal: expect.any(AbortSignal) }));
		expect(state.refreshSource).toHaveBeenCalledWith({ kind: "cloud", id: "org-1" });
		expect(state.replace).toHaveBeenCalledWith({ pathname: "/project/[id]", params: { id: "project-1", source: "cloud", sourceId: "org-1" } });
	});

	it("keeps the repository selection while a replacement search has no matches or is dismissed", async () => {
		state.signedIn = true; state.orgId = "org-1"; state.orgLoading = false;
		state.listGitHubInstallations.mockResolvedValue([{ id: "install-1", status: "active", syncStatus: "ready" }] as never);
		state.listGitHubRepositories.mockResolvedValue({ items: [
			{ githubRepositoryId: "1", name: "alpha", fullName: "me/alpha", htmlUrl: "https://github.com/me/alpha", defaultBranch: "main", access: "active", isArchived: false, isPrivate: true },
		], page: { hasMore: false } } as never);
		const root = await mount();
		const picker = () => root.findByProps({ accessibilityRole: "combobox", accessibilityLabel: "Select a repository" });
		await act(async () => picker().props.onPress());
		await act(async () => root.findByProps({ accessibilityLabel: "Search repositories" }).props.onChangeText("   "));
		expect(root.findAllByProps({ accessibilityRole: "radio" })).toHaveLength(0);
		await act(async () => root.findByProps({ accessibilityLabel: "Search repositories" }).props.onChangeText(" ALPHA "));
		await act(async () => root.findByProps({ accessibilityLabel: "Select me/alpha" }).props.onPress());
		expect(picker().findAllByProps({ name: "lock" })).toHaveLength(1);
		await act(async () => picker().props.onPress());
		await act(async () => root.findByProps({ accessibilityLabel: "Search repositories" }).props.onChangeText("missing"));
		expect(root.findAllByType("Text" as never).map(node => node.props.children)).toContain("No matching repositories");
		await act(async () => root.findByProps({ label: "Back" }).props.onPress());
		expect(root.findAllByProps({ accessibilityLabel: "Search repositories" })).toHaveLength(0);
		expect(picker().props.accessibilityValue).toEqual({ text: "me/alpha" });
		expect(root.findByProps({ title: "Continue" }).props.disabled).toBe(false);
		await act(async () => picker().props.onPress());
		expect(root.findByProps({ accessibilityLabel: "Search repositories" }).props.value).toBe("");
		expect(root.findAllByProps({ accessibilityRole: "radio" })).toHaveLength(0);
	});

	it("asks Cloud to sync a newly connected installation before listing repositories", async () => {
		state.signedIn = true;
		state.orgId = "org-1";
		state.orgLoading = false;
		state.listGitHubInstallations
			.mockResolvedValueOnce([{ id: "install-1", accountLogin: "me", status: "active", syncStatus: "pending" }] as never)
			.mockResolvedValueOnce([{ id: "install-1", accountLogin: "me", status: "active", syncStatus: "ready" }] as never);
		await mount();
		expect(state.syncGitHubInstallation).toHaveBeenCalledWith("org-1", "install-1", expect.anything());
		expect(state.listGitHubRepositories).toHaveBeenCalledWith("org-1", expect.objectContaining({ limit: 100 }));
	});

	it("does not call a GitHub installation ready while repository sync is pending", async () => {
		state.signedIn = true;
		state.orgId = "org-1";
		state.orgLoading = false;
		state.listGitHubInstallations.mockResolvedValue([{ id: "install-1", accountLogin: "me", status: "active", syncStatus: "pending" }] as never);
		const root = await mount();
		const text = root.findAll((node) => String(node.type) === "Text").map((node) => node.props.children);
		expect(text).toContain("Syncing repositories from me");
		expect(text).not.toContain("Connected to me");
	});

	it("lets a connected account grant more repositories from the empty state", async () => {
		state.signedIn = true;
		state.orgId = "org-1";
		state.orgLoading = false;
		state.listGitHubInstallations.mockResolvedValue([{ id: "install-1", accountLogin: "me", status: "active", syncStatus: "ready" }] as never);
		const root = await mount();
		await act(async () => { await root.findByProps({ title: "Manage GitHub access" }).props.onPress(); });
		expect(state.startGitHubInstallation).toHaveBeenCalledWith("org-1", expect.anything());
		expect(openGitHub).toHaveBeenCalledWith("https://github.com/apps/ao/installations/new", { fromSheet: true, throwOnFailure: true });
	});

	it("lets the user stop waiting when GitHub approval is not finished", async () => {
		state.signedIn = true;
		state.orgId = "org-1";
		state.orgLoading = false;
		vi.mocked(openGitHub).mockImplementationOnce(() => new Promise(() => {}));
		const root = await mount();
		await act(async () => { void root.findByProps({ title: "Connect GitHub" }).props.onPress(); });
		await act(async () => { root.findByProps({ title: "Stop waiting" }).props.onPress(); });
		expect(root.findByProps({ title: "Connect GitHub" })).toBeDefined();
	});

	it("shows when AO is opening GitHub before the browser starts", async () => {
		state.signedIn = true;
		state.orgId = "org-1";
		state.orgLoading = false;
		state.startGitHubInstallation.mockImplementationOnce(() => new Promise(() => {}));
		const root = await mount();
		await act(async () => { void root.findByProps({ title: "Connect GitHub" }).props.onPress(); });
		expect(root.findByProps({ title: "Opening GitHub…" })).toBeDefined();
	});

	it("keeps waiting for a changed installation when configuring existing GitHub access", async () => {
		state.signedIn = true;
		state.orgId = "org-1";
		state.orgLoading = false;
		state.listGitHubInstallations.mockResolvedValue([{ id: "install-1", accountLogin: "me", status: "active", syncStatus: "ready", updatedAt: "2026-09-28T00:00:00Z" }] as never);
		vi.mocked(openGitHub).mockImplementationOnce(() => new Promise(() => {}));
		const root = await mount();
		vi.useFakeTimers();
		await act(async () => { void root.findByProps({ title: "Manage GitHub access" }).props.onPress(); });
		await act(async () => { await vi.advanceTimersByTimeAsync(2600); });
		expect(root.findByProps({ title: "Stop waiting" })).toBeDefined();
		expect(root.findAll((node) => String(node.type) === "Feather" && node.props.name === "check-circle")).toHaveLength(0);
	});

	it("shows repository sync after approval creates a pending installation", async () => {
		vi.useFakeTimers();
		state.signedIn = true;
		state.orgId = "org-1";
		state.orgLoading = false;
		const pending = [{ id: "install-2", accountLogin: "me", status: "active", syncStatus: "pending", updatedAt: "2026-09-28T00:01:00Z" }];
		state.listGitHubInstallations
			.mockResolvedValueOnce([])
			.mockResolvedValueOnce([])
			.mockResolvedValue(pending as never);
		const root = await mount();
		await act(async () => { await root.findByProps({ title: "Connect GitHub" }).props.onPress(); });
		// The foreground refresh joins an in-flight read; the next single-flight
		// poll observes the installation created while the browser was opening.
		await act(async () => { await vi.advanceTimersByTimeAsync(2600); });
		const text = root.findAll((node) => String(node.type) === "Text").map((node) => node.props.children);
		expect(text).toContain("Syncing repositories from me");
		expect(text).not.toContain("Waiting for GitHub approval…");
	});

	it("syncs a pending installation even when another installation is ready", async () => {
		state.signedIn = true; state.orgId = "org-1"; state.orgLoading = false;
		state.listGitHubInstallations.mockResolvedValueOnce([
			{ id: "ready", accountLogin: "one", status: "active", syncStatus: "ready" },
			{ id: "pending", accountLogin: "two", status: "active", syncStatus: "pending" },
		] as never).mockResolvedValue([
			{ id: "ready", accountLogin: "one", status: "active", syncStatus: "ready" },
			{ id: "pending", accountLogin: "two", status: "active", syncStatus: "ready" },
		] as never);
		await mount();
		expect(state.syncGitHubInstallation).toHaveBeenCalledWith("org-1", "pending", expect.anything());
	});

	it("keeps the manual token and URL path available when GitHub App is unavailable", async () => {
		state.signedIn = true;
		state.orgId = "org-1";
		state.orgLoading = false;
		state.listGitHubInstallations.mockRejectedValueOnce(new Error("GitHub App unavailable"));
		state.listUserProviderConnections.mockResolvedValueOnce([{ provider: "github", validationState: "valid" }] as never);
		const root = await mount();
		expect(root.findAllByProps({ accessibilityRole: "alert" })).toHaveLength(1);
		await act(async () => { root.findByProps({ title: "Set up manually with a token" }).props.onPress(); });
		expect(root.findByProps({ accessibilityLabel: "Repository URL" })).toBeDefined();
	});

	it("connects coding agents without losing repository settings or creating the project", async () => {
		state.signedIn = true; state.orgId = "org-1"; state.orgLoading = false;
		state.listUserProviderConnections.mockResolvedValue([{ provider: "github", validationState: "valid" }, { provider: "codex", label: "default", validationState: "valid" }] as never);
		const root = await mount();
		await act(async () => root.findByProps({ title: "Set up manually with a token" }).props.onPress());
		await act(async () => {
			root.findByProps({ accessibilityLabel: "Repository URL" }).props.onChangeText("https://github.com/me/keep-draft");
			root.findByProps({ accessibilityLabel: "Project name" }).props.onChangeText("Keep draft");
			root.findByProps({ accessibilityLabel: "Default branch" }).props.onChangeText("develop");
		});
		await act(async () => root.findByProps({ title: "Check repository access" }).props.onPress());
		await act(async () => root.findByProps({ title: "Connect coding agents" }).props.onPress());
		const setup = root.findByType("AgentConnections" as never);
		const before = state.listUserProviderConnections.mock.calls.length;
		await act(async () => setup.props.onChanged());
		expect(state.listUserProviderConnections.mock.calls.length).toBeGreaterThan(before);
		expect(state.createProject).not.toHaveBeenCalled(); expect(state.createProjectFromGitHub).not.toHaveBeenCalled();
		await act(async () => setup.props.onBack());
		await act(async () => root.findByProps({ title: "Create project" }).props.onPress());
		expect(state.createProject).toHaveBeenCalledWith("org-1", expect.objectContaining({ repositoryUrl: "https://github.com/me/keep-draft", displayName: "Keep draft", defaultBranch: "develop", config: expect.objectContaining({ worker: expect.objectContaining({ agent: "codex" }) }) }), expect.anything());
	});

	it("uses the existing manual project endpoint for a saved-token import", async () => {
		state.signedIn = true;
		state.orgId = "org-1";
		state.orgLoading = false;
		state.listUserProviderConnections.mockResolvedValue([
			{ provider: "github", validationState: "valid" },
			{ provider: "codex", label: "default", validationState: "valid" },
		] as never);
		const root = await mount();
		await act(async () => { root.findByProps({ title: "Set up manually with a token" }).props.onPress(); });
		await act(async () => {
			root.findByProps({ accessibilityLabel: "Repository URL" }).props.onChangeText("https://github.com/me/manual");
			root.findByProps({ accessibilityLabel: "Project name" }).props.onChangeText("Manual");
		});
		await act(async () => { await root.findByProps({ title: "Check repository access" }).props.onPress(); });
		await act(async () => { await root.findByProps({ title: "Create project" }).props.onPress(); });
		expect(state.createProject).toHaveBeenCalledWith("org-1", expect.objectContaining({ repositoryUrl: "https://github.com/me/manual", displayName: "Manual" }), expect.objectContaining({ idempotencyKey: "test-key", signal: expect.any(AbortSignal) }));
		expect(state.createProjectFromGitHub).not.toHaveBeenCalled();
	});
});
