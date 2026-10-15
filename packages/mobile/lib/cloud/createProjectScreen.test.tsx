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
}));
const storage = vi.hoisted(() => ({ data: new Map<string, string>(), setItem: vi.fn() }));
vi.mock("@react-native-async-storage/async-storage", () => ({ default: {
	getItem: async (key: string) => storage.data.get(key) ?? null,
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
vi.mock("../haptics", () => ({ haptics: { tap: vi.fn(), select: vi.fn() } }));
vi.mock("../openGitHub", () => ({ openGitHub: vi.fn() }));
vi.mock("../RouteErrorBoundary", () => ({ SheetErrorBoundary: "SheetErrorBoundary" }));
vi.mock("../harnessLogoAssets", () => ({ logoFor: (harness: string) => `asset-${harness}` }));

import CreateCloudProject from "../../app/create-project";
import { openGitHub } from "../openGitHub";
import { CloudApiError } from "@aoagents/cloud-client";

let renderer: ReactTestRenderer | undefined;

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
	state.sessionEpoch = 0;
	state.accountId = "user-1"; state.baseUrl = "https://cloud.test";
	storage.data.clear();
	storage.setItem.mockReset().mockImplementation(async (key: string, value: string) => { storage.data.set(key, value); });
	vi.clearAllMocks();
	state.listGitHubInstallations.mockResolvedValue([]);
	state.listGitHubRepositories.mockResolvedValue({ items: [], page: { hasMore: false } });
	state.listUserProviderConnections.mockResolvedValue([]);
	state.getOrgCoderConfig.mockResolvedValue({ configured: false });
	state.listCoderTemplates.mockResolvedValue({ templates: [] });
	state.getCoderTemplateParameters.mockResolvedValue({ parameters: [] });
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
	it("creates a managed Coder project with Default and explicit provider without a BYO template", async () => {
		state.offering = { providers: ["coder"], defaultProvider: "coder", kind: "b2b", source: "server" };
		const root = await manualReview();
		await act(async () => { await root.findByProps({ title: "Create project" }).props.onPress(); });
		expect(state.createProject).toHaveBeenCalledWith("org-1", expect.objectContaining({ config: expect.objectContaining({ sandboxProvider: "coder" }) }), expect.anything());
		expect((state.createProject.mock.calls[0]?.[1] as { config: { coder?: unknown } }).config.coder).toBeUndefined();
	});
	it("offers Retry instead of reporting missing credentials after a readiness request fails", async () => {
		state.signedIn = true; state.orgId = "org-1"; state.orgLoading = false;
		state.listUserProviderConnections.mockRejectedValueOnce(new Error("Credential service unavailable"));
		const root = await mount();
		expect(root.findByProps({ title: "Retry" })).toBeDefined();
		expect(root.findAllByType("Text" as never).map((node) => node.props.children)).toContain("Credential service unavailable");
	});
	it("keeps account resolution loading instead of showing a false sign-in error", async () => {
		const root = await mount();
		expect(root.findAll((node) => String(node.type) === "ActivityIndicator")).toHaveLength(1);
		expect(root.findAll((node) => String(node.type) === "Text").map((node) => node.props.children)).not.toContain("Sign in to Orchestrator Cloud before adding a project.");
		expect(state.listUserProviderConnections).not.toHaveBeenCalled();
	});

	it("keeps the header outside the scroll area in a native sheet container", async () => {
		const root = await mount();
		const sheet = root.children[0];
		expect(typeof sheet).not.toBe("string");
		if (typeof sheet === "string") return;
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
