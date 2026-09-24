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
});

afterEach(async () => {
	if (renderer) await act(async () => renderer?.unmount());
	renderer = undefined;
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
		await act(async () => {
			root.findByProps({ accessibilityLabel: "Repository URL" }).props.onChangeText("https://github.com/owner/repo");
			root.findByProps({ accessibilityLabel: "Project name" }).props.onChangeText("Tappy");
		});
		await act(async () => { root.findByProps({ title: "Check repository access" }).props.onPress(); });
		const marks = root.findAll((node) => String(node.type) === "Image").map((node) => node.props.source);
		expect(marks).toEqual(["asset-claude-code", "asset-codex", "asset-claude-code", "asset-codex"]);
	});
});
