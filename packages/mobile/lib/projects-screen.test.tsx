import { act, create, type ReactTestRenderer } from "react-test-renderer";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { HostSnapshot } from "./otherHosts";
import type { Scoped, ScopedBoard } from "./environment/scopedBoard";
import type { OrchestratorProjectRow } from "./orchestratorView";

const state = vi.hoisted(() => ({ board: {} as ScopedBoard, hosts: [] as HostSnapshot[], scheme: "dark" as "dark" | "light" }));
vi.mock("expo-router", () => ({ useRouter: () => ({ push: vi.fn(), navigate: vi.fn() }) }));
vi.mock("react-native-safe-area-context", () => ({ useSafeAreaInsets: () => ({ top: 0, bottom: 0 }) }));
vi.mock("react-native", async () => {
	const React = await import("react");
	return {
		Platform: { OS: "ios" },
		ActivityIndicator: "ActivityIndicator", Pressable: "Pressable", View: "View", Text: "Text", RefreshControl: "RefreshControl",
		StyleSheet: { create: (styles: object) => styles, hairlineWidth: 1 },
		SectionList: ({ sections, renderItem, ...props }: { sections: { key: string; data: Scoped<OrchestratorProjectRow>[] }[]; renderItem: (input: { item: Scoped<OrchestratorProjectRow> }) => React.ReactNode }) =>
			React.createElement("SectionList", { ...props, sections }, sections.map((section) =>
				React.createElement(React.Fragment, { key: section.key }, section.data.map((item) =>
					React.createElement(React.Fragment, { key: item.value.project.id }, renderItem({ item })))))),
	};
});
vi.mock("react-native-svg", () => ({ default: "Svg", Path: "Path", Rect: "Rect" }));
vi.mock("./icons", () => ({ Feather: "Feather" }));
vi.mock("./orchestrator-icon", () => ({ OrchestratorIcon: "OrchestratorIcon" }));
vi.mock("./haptics", () => ({ haptics: { tap: vi.fn(), select: vi.fn() } }));
vi.mock("./cloud/authStore", () => ({ useCloudAuth: () => ({ signedIn: true, busy: false, signInWithWorkOS: vi.fn(async () => true) }) }));
vi.mock("./store", () => ({ useApp: () => ({ scopedBoard: state.board, hostStates: state.hosts, refreshAll: vi.fn(async () => {}), notificationsUnread: 0 }) }));
vi.mock("./useOrchestratorLauncher", () => ({ useOrchestratorLauncher: () => ({ busyProjects: new Set(), openOrchestrator: vi.fn() }) }));
vi.mock("./useTabScrollToTop", () => ({ useTabScrollToTop: () => ({ current: null }) }));
vi.mock("./StaleBanner", () => ({ StaleBanner: "StaleBanner" }));
vi.mock("./ui", () => ({ Button: "Button", EmptyState: "EmptyState", HeaderIconButton: "HeaderIconButton", ListSectionHeader: "ListSectionHeader", ScreenHeader: "ScreenHeader", Dot: "Dot" }));
vi.mock("./ThemeProvider", async () => {
	const { darkTheme, lightTheme } = await import("./theme");
	const current = () => state.scheme === "dark" ? darkTheme : lightTheme;
	return { useTheme: current, useThemedStyles: (factory: (theme: typeof darkTheme) => unknown) => factory(current()) };
});

import ProjectsScreen from "../app/(tabs)/projects";
import { composeBoards } from "./environment/scopedBoard";
import { MachineIdentity } from "./MachineIdentity";

let renderer: ReactTestRenderer | undefined;
beforeEach(() => {
	vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true);
	state.scheme = "dark";
	const error = console.error;
	vi.spyOn(console, "error").mockImplementation((...args) => {
		if (String(args[0]).startsWith("react-test-renderer is deprecated")) return;
		error(...args);
	});
	state.hosts = [{ hostId: "mac-1", name: "Prasad Mac", appearance: { symbol: "mac-studio" }, connection: "open", notificationsUnread: 0 } as HostSnapshot];
	state.board = composeBoards([
		{ source: { kind: "local", id: "mac-1" }, status: { resolved: true, available: true, loading: false, stale: false, error: null }, snapshot: { board: {
			projects: [{ id: "same-project", name: "Local project" }], sessions: [], orchestrators: [{ id: "local-orchestrator", projectId: "same-project", projectName: "Local project", mode: "chat", status: "idle" }],
		} } },
		{ source: { kind: "cloud", id: "org-1" }, status: { resolved: true, available: true, loading: false, stale: false, error: null }, snapshot: { board: {
			projects: [{ id: "same-project", name: "Cloud project" }], sessions: [], orchestrators: [{ id: "cloud-orchestrator", projectId: "same-project", projectName: "Cloud project", mode: "chat", status: "idle" }],
		} } },
	]);
});
afterEach(async () => { if (renderer) await act(async () => renderer?.unmount()); renderer = undefined; vi.restoreAllMocks(); vi.unstubAllGlobals(); });

describe("combined Projects source identity", () => {
	it.each(["dark", "light"] as const)("renders the source machine and Cloud icon once from plain scoped projects in %s mode", async (scheme) => {
		state.scheme = scheme;
		await act(async () => { renderer = create(<ProjectsScreen />); });
		expect(renderer!.root.findAllByType(MachineIdentity).map((identity) => ({ name: identity.props.name, appearance: identity.props.appearance }))).toEqual([
			{ name: "Prasad Mac", appearance: { symbol: "mac-studio" } },
			{ name: "Cloud", appearance: { symbol: "cloud" } },
		]);
	});
	it("keeps section keys distinct when Local and Cloud use the same project id and status lane", async () => {
		await act(async () => { renderer = create(<ProjectsScreen />); });
		const list = renderer!.root.findByType("SectionList" as never);
		expect(list.props.sections.map((section: { key: string }) => section.key)).toEqual(['["local","mac-1"]:coordinating', '["cloud","org-1"]:coordinating']);
		const entries = list.props.sections.flatMap((section: { data: unknown[] }) => section.data);
		expect(entries.map(list.props.keyExtractor)).toEqual(['["local","mac-1","same-project"]', '["cloud","org-1","same-project"]']);
	});
	it("keeps a cached project's owning machine after the host disconnects or is renamed", async () => {
		state.hosts[0] = { ...state.hosts[0], name: "Build Mac", connection: "closed" };
		await act(async () => { renderer = create(<ProjectsScreen />); });
		expect(renderer!.root.findAllByType(MachineIdentity).map((identity) => identity.props.name)).toEqual(["Build Mac (offline)", "Cloud"]);
	});
});
