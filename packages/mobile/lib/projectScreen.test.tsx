import { act, create, type ReactTestRenderer } from "react-test-renderer";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { ProjectInfo } from "./api";
import type { Scoped } from "./environment/scopedBoard";
import { MachineIdentity } from "./MachineIdentity";
import { ScreenHeader } from "./ui";

const state = vi.hoisted(() => ({
	push: vi.fn(), openOrchestrator: vi.fn(), signIn: vi.fn(async () => true), available: true, action: "start" as "start" | "open",
	params: { id: "project-1", source: "cloud", sourceId: "org-1" } as { id: string; source?: string; sourceId?: string; hostId?: string },
	projects: [] as Scoped<ProjectInfo>[],
}));

vi.mock("react-native", () => ({
	ActivityIndicator: "ActivityIndicator",
	Alert: { alert: vi.fn() },
	Platform: { OS: "ios" },
	StyleSheet: { create: (styles: unknown) => styles, hairlineWidth: 1 },
	Text: "Text",
	View: "View",
}));
vi.mock("react-native-safe-area-context", () => ({ useSafeAreaInsets: () => ({ top: 0, bottom: 24 }) }));
vi.mock("expo-router", () => ({ useLocalSearchParams: () => state.params, useRouter: () => ({ push: state.push, canGoBack: () => true, back: vi.fn() }) }));
vi.mock("./haptics", () => ({ haptics: { tap: vi.fn() } }));
vi.mock("./cloud/authStore", () => ({ useCloudAuth: () => ({ signedIn: false, signInWithWorkOS: state.signIn }) }));
vi.mock("./store", () => ({ useApp: () => ({ environment: "cloud", configured: true, loading: false, error: null, refresh: vi.fn(), projects: [], sessions: [], orchestrators: [],
	hostStates: [
		{ hostId: "host-default", name: "Default Mac", appearance: { symbol: "server" } },
		{ hostId: "host-dev", name: "Development Laptop", appearance: { symbol: "laptop" } },
	],
	scopedBoard: { projects: state.projects, sessions: [], orchestrators: [],
		sources: { '["cloud","org-1"]': { resolved: true, available: state.available, loading: false, error: null } } },
	sourceFor: () => state.available ? { kind: "cloud" } : undefined, refreshSource: vi.fn(async () => {}) }) }));
vi.mock("./orchestratorView", () => ({
	orchestratorProjectSections: (projects: ProjectInfo[]) => state.available ? [{ data: projects.map((project) => ({ project, action: state.action, link: null })) }] : [],
	projectDetailSessions: () => [],
	projectPageStats: () => ({ workers: 0, needsYou: 0, ready: 0, archived: 0 }),
}));
vi.mock("./project-card", async (importOriginal) => ({ ...await importOriginal<typeof import("./project-card")>(), ProjectPageHeader: "ProjectPageHeader" }));
vi.mock("react-native-svg", () => ({ default: "Svg", Path: "Path", Rect: "Rect", Circle: "Circle" }));
vi.mock("./icons", () => ({ Feather: "Feather" }));
vi.mock("./native-header-button", () => ({ NativeHeaderButton: "NativeHeaderButton" }));
vi.mock("./motionHooks", () => ({ useEnterTransition: () => ({}), usePressScale: () => ({}) }));
vi.mock("./StaleBanner", () => ({ StaleBanner: "StaleBanner" }));
vi.mock("./UnpairedState", () => ({ CloudUnreadyState: "CloudUnreadyState" }));
vi.mock("./ThemeProvider", async () => {
	const { darkTheme } = await import("./theme");
	return { useTheme: () => darkTheme, useThemedStyles: (makeStyles: (theme: typeof darkTheme) => unknown) => makeStyles(darkTheme) };
});
vi.mock("./useOrchestratorLauncher", () => ({ useOrchestratorLauncher: () => ({ busyProjects: new Set(), openOrchestrator: state.openOrchestrator }) }));
vi.mock("./ui", async (importOriginal) => ({ ...await importOriginal<typeof import("./ui")>(), Button: "Button", EmptyState: "EmptyState", HeaderIconButton: "HeaderIconButton", ListSectionHeader: "ListSectionHeader" }));
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
	state.action = "start";
	state.available = true;
	state.params = { id: "project-1", source: "cloud", sourceId: "org-1" };
	state.projects = [{ source: { kind: "cloud", id: "org-1" }, value: { id: "project-1", name: "Tappy", kind: "single_repo" } }];
});

afterEach(async () => {
	if (renderer) await act(async () => renderer?.unmount());
	renderer = undefined;
	vi.restoreAllMocks();
	vi.unstubAllGlobals();
});

describe("Cloud project page", () => {
	it("shows Cloud once at the trailing edge of a long project title", async () => {
		state.projects[0] = { ...state.projects[0], value: { id: "project-1", name: "A long project title that needs space beside the Cloud identity", kind: "single_repo" } };
		await act(async () => { renderer = create(<ProjectScreen />); });
		const header = renderer!.root.findByType(ScreenHeader);
		expect(header.findAllByType(MachineIdentity)).toHaveLength(1);
		expect(header.findByType(MachineIdentity).props.name).toBe("Cloud");
		expect(header.findAll((node) => String(node.type) === "Feather" && node.props.name === "cloud")).toHaveLength(1);
		const title = header.find((node) => String(node.type) === "Text" && node.props.children === state.projects[0].value.name);
		expect(title.props.numberOfLines).toBe(1);
		expect(title.props.ellipsizeMode).toBe("tail");
		expect(title.parent!.props.style).toMatchObject({ flex: 1, minWidth: 0 });
		const headerRow = header.find((node) => String(node.type) === "View" && node.children.includes(title.parent!));
		expect(headerRow.children.at(-1)).toBe(header.findByType(MachineIdentity).parent);
	});
	it("shows the resolved Local machine when the project ID also exists in Cloud", async () => {
		state.params = { id: "project-1", source: "local", sourceId: "host-dev" };
		state.projects.push({ source: { kind: "local", id: "host-dev" }, value: { id: "project-1", name: "Local Tappy", kind: "single_repo" } });
		await act(async () => { renderer = create(<ProjectScreen />); });
		const header = renderer!.root.findByType(ScreenHeader);
		expect(header.props.title).toBe("Local Tappy");
		expect(header.findAllByType(MachineIdentity)).toHaveLength(1);
		expect(header.findByType(MachineIdentity).props.name).toBe("Development Laptop");
		expect(header.findByType(MachineIdentity).props.appearance).toEqual({ symbol: "laptop" });
	});
	it.each([
		{ id: "project-1" },
		{ id: "project-1", source: "cloud", sourceId: "org-1", hostId: "host-dev" },
		{ id: "project-1", source: "cloud", sourceId: " " },
	])("does not name a source for an ambiguous or invalid project link %j", async (params) => {
		state.params = params;
		state.projects.push({ source: { kind: "local", id: "host-dev" }, value: { id: "project-1", name: "Local Tappy", kind: "single_repo" } });
		await act(async () => { renderer = create(<ProjectScreen />); });
		expect(renderer!.root.findByType(ScreenHeader).findAllByType(MachineIdentity)).toHaveLength(0);
		expect(renderer!.root.findByType("EmptyState" as never).props.title).toBe("Choose a project from Projects");
	});
	it("opens hosted sign-in directly when its Cloud source is disconnected", async () => {
		state.available = false;
		await act(async () => { renderer = create(<ProjectScreen />); });
		const action = renderer!.root.findByType("EmptyState" as never).props.action;
		expect(action.props.title).toBe("Sign in to Orchestrator Cloud");
		await act(async () => { await action.props.onPress(); });
		expect(state.signIn).toHaveBeenCalledOnce();
		expect(state.push).not.toHaveBeenCalled();
	});
	it.each(["start", "open"] as const)("uses the same orchestrator %s action and qualified project source", async (action) => {
		state.action = action;
		await act(async () => { renderer = create(<ProjectScreen />); });
		const header = renderer!.root.findByType("WorkerBoardList" as never).props.ListHeaderComponent;
		expect(header.props.row.action).toBe(action);
		await act(async () => { header.props.onPress(); });
		expect(state.openOrchestrator).toHaveBeenCalledWith(expect.objectContaining({ source: { kind: "cloud", id: "org-1" } }));
	});
	it("places worker spawn at the bottom, not in the header, and keeps the project selected", async () => {
		await act(async () => { renderer = create(<ProjectScreen />); });
		const root = renderer!.root;
		const dock = root.find((node) => String(node.type) === "WorkerDock");
		await act(async () => dock.props.onSpawn());
		expect(state.push).toHaveBeenCalledWith({ pathname: "/spawn", params: { projectId: "project-1", source: "cloud", sourceId: "org-1" } });
	});
});
