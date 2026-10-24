import { act, create, type ReactTestRenderer } from "react-test-renderer";
import { afterEach, beforeEach, expect, it, vi } from "vitest";

const fixture = vi.hoisted(() => ({
	params: {} as Record<string, string>, push: vi.fn(), setActiveProject: vi.fn(),
	projects: [{ id: "cloud-a", name: "Cloud A" }, { id: "cloud-b", name: "Cloud B" }],
	hostStates: [{ hostId: "mac", projectsKnown: true, projects: [{ id: "local-a", name: "Local A" }, { id: "local-b", name: "Local B" }] }],
}));
vi.mock("react-native", () => ({ View: "View", Text: "Text", Pressable: "Pressable", Platform: { OS: "ios" }, StyleSheet: { create: (value: unknown) => value } }));
vi.mock("expo-router", () => ({ useRouter: () => ({ push: fixture.push, back: vi.fn(), canGoBack: () => true }), useLocalSearchParams: () => fixture.params }));
vi.mock("./store", () => ({ useApp: () => ({ projects: fixture.projects, projectsKnown: true, activeProjectId: "cloud-a", setActiveProject: fixture.setActiveProject, hostStates: fixture.hostStates }) }));
vi.mock("./icons", () => ({ Feather: "Feather" }));
vi.mock("./haptics", () => ({ haptics: { tap: vi.fn() } }));
vi.mock("./ThemeProvider", () => ({ useTheme: () => ({}), useThemedStyles: (make: (value: object) => unknown) => make({}) }));
vi.mock("./ProjectPickerSheet", () => ({ ProjectPickerSheet: "ProjectPickerSheet" }));
vi.mock("./RouteErrorBoundary", () => ({ SheetErrorBoundary: "SheetErrorBoundary" }));

import { ProjectSwitcher } from "./ProjectSwitcher";
import ProjectSheetRoute from "../app/sheets/project";
import { projectSheetRoute, resetSheetResults } from "./sheetResult";

let renderer: ReactTestRenderer | undefined;
beforeEach(() => { vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true); vi.clearAllMocks(); resetSheetResults(); fixture.params = {}; });
afterEach(() => { act(() => renderer?.unmount()); renderer = undefined; vi.unstubAllGlobals(); });

it("carries the exact Local host from the switcher into the native project sheet", () => {
	const onSelect = vi.fn();
	act(() => { renderer = create(<ProjectSwitcher hostId="mac" selected="local-b" onSelect={onSelect} />); });
	act(() => renderer!.root.findByProps({ accessibilityLabel: "Change active project" }).props.onPress());
	fixture.params = fixture.push.mock.calls[0][0].params;
	expect(fixture.params).toMatchObject({ hostId: "mac", selected: "local-b" });
	act(() => renderer!.update(<ProjectSheetRoute />));
	const picker = renderer!.root.findByType("ProjectPickerSheet" as never);
	expect(picker.props.projects).toEqual(fixture.hostStates[0].projects);
	act(() => picker.props.onSelect("local-a"));
	expect(onSelect).toHaveBeenCalledWith("local-a");
	expect(fixture.setActiveProject).not.toHaveBeenCalled();
});

it("keeps ordinary unscoped callers on their existing project selection", () => {
	act(() => { renderer = create(<ProjectSwitcher />); });
	act(() => renderer!.root.findByProps({ accessibilityLabel: "Change active project" }).props.onPress());
	fixture.params = fixture.push.mock.calls[0][0].params;
	expect(fixture.params.hostId).toBeUndefined();
	act(() => renderer!.update(<ProjectSheetRoute />));
	const picker = renderer!.root.findByType("ProjectPickerSheet" as never);
	expect(picker.props.projects).toEqual(fixture.projects);
	act(() => picker.props.onSelect("cloud-b"));
	expect(fixture.setActiveProject).toHaveBeenCalledWith("cloud-b");
});

it("never borrows Cloud projects for a removed or unknown Local host", () => {
	fixture.params = projectSheetRoute({ hostId: "removed", selected: "all", onSelect: vi.fn() }).params;
	act(() => { renderer = create(<ProjectSheetRoute />); });
	expect(renderer!.root.findByType("ProjectPickerSheet" as never).props.projects).toEqual([]);
	act(() => renderer!.update(<ProjectSwitcher hostId="removed" selected="all" onSelect={vi.fn()} />));
	expect(renderer!.root.findAllByProps({ accessibilityLabel: "Change active project" })).toHaveLength(0);
});
