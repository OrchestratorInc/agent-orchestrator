import { act, create, type ReactTestRenderer } from "react-test-renderer";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const state = vi.hoisted(() => ({
	environment: "cloud" as "cloud" | "local",
	signedIn: null as boolean | null,
	orgId: null as string | null,
	orgLoading: true,
	orgError: null as string | null,
	listUserProviderConnections: vi.fn(async () => []),
	listProviderConnections: vi.fn(async () => []),
	listGitHubInstallations: vi.fn(async () => [] as unknown[]),
	listGitHubRepositories: vi.fn(async () => ({ items: [] as unknown[], page: { hasMore: false } })),
	startGitHubInstallation: vi.fn(async () => ({ installationUrl: "https://github.com/apps/ao/installations/new" })),
	syncGitHubInstallation: vi.fn(async () => ({})),
	createProjectFromGitHub: vi.fn(async () => ({ project: { id: "project-1" } })),
	createProject: vi.fn(async () => ({ project: { id: "project-2" } })),
	validateSavedRepositoryAccess: vi.fn(async () => ({ writeAccess: true })),
}));

vi.mock("react-native", () => ({
	ActivityIndicator: "ActivityIndicator",
	Image: "Image",
	KeyboardAvoidingView: "KeyboardAvoidingView",
	Platform: { OS: "ios" },
	Pressable: "Pressable",
	ScrollView: "ScrollView",
	StyleSheet: { create: (styles: unknown) => styles },
	Text: "Text",
	TextInput: "TextInput",
	View: "View",
}));
vi.mock("react-native-safe-area-context", () => ({ useSafeAreaInsets: () => ({ bottom: 0 }) }));
vi.mock("expo-router", () => ({ useRouter: () => ({ back: vi.fn(), replace: vi.fn() }) }));
vi.mock("expo-crypto", () => ({ randomUUID: () => "test-key" }));
vi.mock("@expo/vector-icons", () => ({ Feather: "Feather" }));
vi.mock("../ThemeProvider", () => {
	const theme = Object.fromEntries(["bgSurface", "blue", "borderSubtle", "textPrimary", "textSecondary", "textTertiary", "borderDefault", "bgElevated", "amber", "red", "tintBlue"].map((key) => [key, "#000"]));
	return { useTheme: () => theme, useThemedStyles: (makeStyles: (theme: unknown) => unknown) => makeStyles(theme) };
});
vi.mock("./authStore", () => ({ useCloudAuth: () => ({ ...state, client: state }) }));
vi.mock("../store", () => ({ useApp: () => ({ environment: state.environment, refresh: vi.fn(), setActiveProject: vi.fn() }) }));
vi.mock("../ui", () => ({ Button: "Button", HeaderIconButton: "HeaderIconButton" }));
vi.mock("../haptics", () => ({ haptics: { tap: vi.fn(), select: vi.fn() } }));
vi.mock("../openGitHub", () => ({ openGitHub: vi.fn() }));
vi.mock("../RouteErrorBoundary", () => ({ SheetErrorBoundary: "SheetErrorBoundary" }));
vi.mock("../harnessLogoAssets", () => ({ logoFor: (harness: string) => `asset-${harness}` }));

import CreateCloudProject from "../../app/create-project";
import { openGitHub } from "../openGitHub";

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
	vi.clearAllMocks();
	state.listGitHubInstallations.mockResolvedValue([]);
	state.listGitHubRepositories.mockResolvedValue({ items: [], page: { hasMore: false } });
});

afterEach(async () => {
	if (renderer) await act(async () => renderer?.unmount());
	renderer = undefined;
	vi.useRealTimers();
	vi.restoreAllMocks();
	vi.unstubAllGlobals();
});

describe("Add Cloud project sheet", () => {
	it("keeps account resolution loading instead of showing a false sign-in error", async () => {
		const root = await mount();
		expect(root.findAll((node) => String(node.type) === "ActivityIndicator")).toHaveLength(1);
		expect(root.findAll((node) => String(node.type) === "Text").map((node) => node.props.children)).not.toContain("Sign in to AO Cloud before adding a project.");
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
			{ provider: "claude-code", validationState: "valid" },
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
		expect(marks).toEqual(["asset-claude-code", "asset-codex", "asset-claude-code", "asset-codex"]);
	});

	it("connects GitHub using the selected Cloud environment and offers manual setup", async () => {
		state.signedIn = true;
		state.orgId = "org-1";
		state.orgLoading = false;
		const root = await mount();
		expect(root.findByProps({ title: "Connect GitHub" })).toBeDefined();
		await act(async () => { await root.findByProps({ title: "Connect GitHub" }).props.onPress(); });
		expect(state.startGitHubInstallation).toHaveBeenCalledWith("org-1", expect.anything());
		expect(openGitHub).toHaveBeenCalledWith("https://github.com/apps/ao/installations/new");
		await act(async () => { root.findByProps({ title: "Set up manually with a token" }).props.onPress(); });
		expect(root.findByProps({ accessibilityLabel: "GitHub access token" })).toBeDefined();
	});

	it("searches granted repositories and creates an App-backed project", async () => {
		state.signedIn = true;
		state.orgId = "org-1";
		state.orgLoading = false;
		state.listGitHubInstallations.mockResolvedValue([{ id: "install-1", status: "active", syncStatus: "ready" }] as never);
		state.listGitHubRepositories.mockResolvedValue({ items: [
			{ githubRepositoryId: "1", name: "alpha", fullName: "me/alpha", htmlUrl: "https://github.com/me/alpha", defaultBranch: "main", access: "active", isArchived: false },
			{ githubRepositoryId: "2", name: "beta", fullName: "me/beta", htmlUrl: "https://github.com/me/beta", defaultBranch: "develop", access: "active", isArchived: false },
		], page: { hasMore: false } } as never);
		state.listUserProviderConnections.mockResolvedValue([{ provider: "claude-code", validationState: "valid" }] as never);
		const root = await mount();
		await act(async () => { root.findByProps({ accessibilityLabel: "Search repositories" }).props.onChangeText("beta"); });
		expect(root.findAllByProps({ accessibilityLabel: "Select me/alpha" })).toHaveLength(0);
		await act(async () => { root.findByProps({ accessibilityLabel: "Select me/beta" }).props.onPress(); });
		await act(async () => { root.findByProps({ title: "Continue" }).props.onPress(); });
		await act(async () => { await root.findByProps({ title: "Create project" }).props.onPress(); });
		expect(state.createProjectFromGitHub).toHaveBeenCalledWith("org-1", {
			githubRepositoryId: "2",
			displayName: "beta",
			config: { worker: { agent: "claude-code" }, orchestrator: { agent: "claude-code" } },
		}, { idempotencyKey: "test-key" });
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
		expect(openGitHub).toHaveBeenCalledWith("https://github.com/apps/ao/installations/new");
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
		const text = root.findAll((node) => String(node.type) === "Text").map((node) => node.props.children);
		expect(text).toContain("Syncing repositories from me");
		expect(text).not.toContain("Waiting for GitHub approval…");
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
		state.listUserProviderConnections.mockResolvedValueOnce([
			{ provider: "github", validationState: "valid" },
			{ provider: "codex", validationState: "valid" },
		] as never);
		const root = await mount();
		await act(async () => { root.findByProps({ title: "Set up manually with a token" }).props.onPress(); });
		await act(async () => {
			root.findByProps({ accessibilityLabel: "Repository URL" }).props.onChangeText("https://github.com/me/manual");
			root.findByProps({ accessibilityLabel: "Project name" }).props.onChangeText("Manual");
		});
		await act(async () => { await root.findByProps({ title: "Check repository access" }).props.onPress(); });
		await act(async () => { await root.findByProps({ title: "Create project" }).props.onPress(); });
		expect(state.createProject).toHaveBeenCalledWith("org-1", expect.objectContaining({ repositoryUrl: "https://github.com/me/manual", displayName: "Manual" }), { idempotencyKey: "test-key" });
		expect(state.createProjectFromGitHub).not.toHaveBeenCalled();
	});
});
