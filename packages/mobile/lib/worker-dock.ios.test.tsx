import { act, create, type ReactTestRenderer } from "react-test-renderer";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const hapticSelect = vi.hoisted(() => vi.fn());

vi.mock("./ThemeProvider", () => ({
	useTheme: () => ({ accent: "#ffffff", textSecondary: "#999999", textPrimary: "#ffffff" }),
	useThemeState: () => ({ scheme: "dark" }),
}));
vi.mock("./haptics", () => ({ haptics: { select: hapticSelect, tap: vi.fn() } }));
vi.mock("./glass", () => ({ glassCircle: () => null, glassField: () => null }));
vi.mock("./native-header-button.ios", () => ({ GLASS_CIRCLE_SIZE: 52 }));
vi.mock("@expo/ui", async () => {
	const React = await import("react");
	return { Host: ({ children, ...props }: { children?: React.ReactNode }) => React.createElement("Host", props, children) };
});
vi.mock("@expo/ui/swift-ui", async () => {
	const React = await import("react");
	const host = (name: string) => ({ children, ...props }: { children?: React.ReactNode }) => React.createElement(name, props, children);
	return {
		Button: host("Button"), GlassEffectContainer: host("GlassEffectContainer"), Group: host("Group"),
		HStack: host("HStack"), Image: host("Image"), Menu: host("Menu"), Section: host("Section"),
		Spacer: host("Spacer"), TextField: host("TextField"), useNativeState: (value: string) => ({ get: () => value, set: vi.fn() }),
	};
});
vi.mock("@expo/ui/swift-ui/modifiers", () => {
	const noop = () => null;
	return {
		accessibilityIdentifier: noop, accessibilityLabel: noop, Animation: { spring: noop }, animation: noop,
		buttonBorderShape: noop, buttonStyle: noop, controlSize: noop, frame: noop, labelStyle: noop,
		menuOrder: noop, opacity: noop, padding: noop, scaleEffect: noop, textFieldStyle: noop, tint: noop,
	};
});

import { WorkerDock } from "./worker-dock.ios";

let renderer: ReactTestRenderer | undefined;

beforeEach(() => {
	vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true);
	hapticSelect.mockReset();
	const error = console.error;
	vi.spyOn(console, "error").mockImplementation((...args) => {
		if (String(args[0]).startsWith("react-test-renderer is deprecated")) return;
		error(...args);
	});
});

afterEach(async () => {
	if (renderer) await act(async () => renderer?.unmount());
	renderer = undefined;
	vi.restoreAllMocks();
	vi.unstubAllGlobals();
});

describe("iOS worker filter menu", () => {
	it("selects Cloud from the Environment submenu and keeps source-qualified projects", async () => {
		const selectEnvironment = vi.fn();
		const selectProject = vi.fn();
		await act(async () => {
			renderer = create(<WorkerDock
				query="" onQueryChange={vi.fn()} onSpawn={vi.fn()}
				searchOpen={false} onSearchOpen={vi.fn()} onSearchClose={vi.fn()} onOpenControls={vi.fn()}
				projectFiltered={false}
				environmentFilter="all" onSelectEnvironment={selectEnvironment}
				projectOptions={[{ id: "all", label: "All projects" }, { id: "cloud:org-1:tap", label: "Tap · Cloud" }]}
				selectedProjectId="all" selectedProjectLabel="All projects" onSelectProject={selectProject}
			/>);
		});
		const menus = renderer!.root.findAll((node) => String(node.type) === "Menu");
		const environment = menus.find((node) => node.props.label === "Environment");
		expect(environment).toBeDefined();
		expect(environment!.findAll((node) => String(node.type) === "Button").map((node) => node.props.label))
			.toEqual(["All environments", "Local", "Cloud"]);
		const cloud = environment!.findAll((node) => String(node.type) === "Button" && node.props.label === "Cloud")[0];
		act(() => cloud.props.onPress());
		expect(selectEnvironment).toHaveBeenCalledWith("cloud");
		expect(hapticSelect).toHaveBeenCalledOnce();
		const project = menus.find((node) => node.props.label === "All projects");
		expect(project!.findAll((node) => String(node.type) === "Button").map((node) => node.props.label))
			.toEqual(["All projects", "Tap · Cloud"]);
		const tap = project!.findAll((node) => String(node.type) === "Button" && node.props.label === "Tap · Cloud")[0];
		act(() => tap.props.onPress());
		expect(selectProject).toHaveBeenCalledWith("cloud:org-1:tap");
	});
});
