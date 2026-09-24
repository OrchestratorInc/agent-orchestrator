import { act, create, type ReactTestRenderer } from "react-test-renderer";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const state = vi.hoisted(() => ({ push: vi.fn() }));

vi.mock("react-native", () => ({
	ActivityIndicator: "ActivityIndicator",
	StyleSheet: { create: (styles: unknown) => styles },
	View: "View",
}));
vi.mock("react-native-safe-area-context", () => ({ useSafeAreaInsets: () => ({ top: 0, bottom: 24 }) }));
vi.mock("expo-router", () => ({ useLocalSearchParams: () => ({ id: "project-1" }), useRouter: () => ({ push: state.push, canGoBack: () => true, back: vi.fn() }) }));
vi.mock("./haptics", () => ({ haptics: { tap: vi.fn() } }));
vi.mock("./store", () => ({ useApp: () => ({ environment: "cloud", configured: true, loading: false, error: null, refresh: vi.fn(), projects: [], sessions: [], orchestrators: [] }) }));
vi.mock("./orchestratorView", () => ({
	orchestratorProjectSections: () => [{ data: [{ project: { id: "project-1", name: "Tappy", kind: "single_repo" }, action: "start", link: null }] }],
	projectDetailSessions: () => [],
	projectPageStats: () => ({ workers: 0, needsYou: 0, ready: 0, archived: 0 }),
}));
vi.mock("./project-card", () => ({ ProjectPageHeader: "ProjectPageHeader" }));
vi.mock("./StaleBanner", () => ({ StaleBanner: "StaleBanner" }));
vi.mock("./UnpairedState", () => ({ CloudUnreadyState: "CloudUnreadyState" }));
vi.mock("./ThemeProvider", () => ({ useTheme: () => ({ bgBase: "#000", blue: "#00f" }), useThemedStyles: (makeStyles: (theme: unknown) => unknown) => makeStyles({ bgBase: "#000", blue: "#00f" }) }));
vi.mock("./useOrchestratorLauncher", () => ({ useOrchestratorLauncher: () => ({ busyProjects: new Set(), openOrchestrator: vi.fn() }) }));
vi.mock("./ui", () => ({ Button: "Button", EmptyState: "EmptyState", HeaderIconButton: "HeaderIconButton", ListSectionHeader: "ListSectionHeader", ScreenHeader: "ScreenHeader" }));
vi.mock("./worker-board-list", () => ({ WorkerBoardList: "WorkerBoardList" }));
vi.mock("./worker-dock", () => ({ WorkerDock: "WorkerDock" }));
vi.mock("./RouteErrorBoundary", () => ({ RouteErrorBoundary: "RouteErrorBoundary" }));

import ProjectScreen from "../app/project/[id]";

let renderer: ReactTestRenderer | undefined;

beforeEach(() => {
	vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true);
	const error = console.error;
	vi.spyOn(console, "error").mockImplementation((...args) => {
		if (String(args[0]).startsWith("react-test-renderer is deprecated")) return;
		error(...args);
	});
	vi.clearAllMocks();
});

afterEach(async () => {
	if (renderer) await act(async () => renderer?.unmount());
	renderer = undefined;
	vi.restoreAllMocks();
	vi.unstubAllGlobals();
});

describe("Cloud project page", () => {
	it("places worker spawn at the bottom, not in the header, and keeps the project selected", async () => {
		await act(async () => { renderer = create(<ProjectScreen />); });
		const root = renderer!.root;
		expect(root.find((node) => String(node.type) === "ScreenHeader").props.right).toBeNull();
		const dock = root.find((node) => String(node.type) === "WorkerDock");
		await act(async () => dock.props.onSpawn());
		expect(state.push).toHaveBeenCalledWith({ pathname: "/spawn", params: { projectId: "project-1" } });
	});
});
