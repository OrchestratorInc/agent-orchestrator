import { act, create, type ReactTestInstance, type ReactTestRenderer } from "react-test-renderer";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const fixture = vi.hoisted(() => ({
	platform: { OS: "ios" as "ios" | "android" },
	app: {
		projects: [{ id: "app", name: "App" }, { id: "api", name: "API" }] as { id: string; name: string }[],
		projectsKnown: true,
		activeProjectId: "api",
		setActiveProject: vi.fn(),
	},
	select: vi.fn(),
	show: vi.fn(),
}));

vi.mock("react-native", async () => {
	const React = await import("react");
	const host = (name: string) => ({ children, ...props }: { children?: React.ReactNode }) => React.createElement(name, props, children);
	return {
		Platform: fixture.platform,
		Pressable: host("Pressable"),
		StyleSheet: { create: (styles: unknown) => styles },
		Text: host("Text"),
		View: host("View"),
	};
});
vi.mock("@expo/ui/community/menu", async () => {
	const React = await import("react");
	return {
		MenuView: ({ ref, children, ...props }: { ref?: React.Ref<{ show(): void }>; children?: React.ReactNode }) => {
			React.useImperativeHandle(ref, () => ({ show: fixture.show }));
			return React.createElement("MenuView", props, children);
		},
	};
});
vi.mock("./store", () => ({ useApp: () => fixture.app }));
vi.mock("./haptics", () => ({ haptics: { select: fixture.select } }));
vi.mock("./ThemeProvider", () => {
	const theme = new Proxy({}, { get: (_, key) => `#${String(key)}` });
	return { useTheme: () => theme, useThemeState: () => ({ scheme: "dark" }), useThemedStyles: (factory: (value: unknown) => unknown) => factory(theme) };
});

import { ProjectSwitcher } from "./ProjectSwitcher";

let renderer: ReactTestRenderer | undefined;

beforeEach(() => {
	vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true);
	fixture.platform.OS = "ios";
	fixture.app.projects = [{ id: "app", name: "App" }, { id: "api", name: "API" }];
	fixture.app.activeProjectId = "api";
	fixture.app.setActiveProject.mockClear();
	fixture.select.mockClear();
	fixture.show.mockClear();
});
afterEach(() => {
	act(() => renderer?.unmount());
	renderer = undefined;
	vi.unstubAllGlobals();
});

function render(): ReactTestInstance {
	act(() => {
		renderer = create(<ProjectSwitcher />);
	});
	return renderer!.root;
}

describe("ProjectSwitcher", () => {
	// The system menu draws the check; selection is a state, not a colour.
	it("lists every project with a check on the current one", () => {
		const menu = render().findByType("MenuView" as never);
		expect(menu.props.actions).toEqual([
			{ id: "all", title: "All projects", state: "off" },
			{ id: "app", title: "App", state: "off" },
			{ id: "api", title: "API", state: "on" },
		]);
	});

	it("names the current project on its trigger", () => {
		const trigger = render().findAll((node) => node.props.accessibilityRole === "button" && typeof node.type === "string")[0];
		expect(trigger.props.accessibilityLabel).toBe("Project, API");
	});

	// A project the machine no longer lists falls back to All, and the check
	// follows what the list actually shows.
	it("checks All when the saved project is gone", () => {
		fixture.app.activeProjectId = "deleted";
		const menu = render().findByType("MenuView" as never);
		expect(menu.props.actions.filter((action: { state: string }) => action.state === "on").map((action: { id: string }) => action.id)).toEqual(["all"]);
	});

	it("applies a new choice in place, and ignores the current one", () => {
		const menu = render().findByType("MenuView" as never);
		act(() => menu.props.onPressAction({ nativeEvent: { event: "api" } }));
		expect(fixture.app.setActiveProject).not.toHaveBeenCalled();
		act(() => menu.props.onPressAction({ nativeEvent: { event: "all" } }));
		expect(fixture.app.setActiveProject).toHaveBeenCalledWith("all");
		expect(fixture.select).toHaveBeenCalledTimes(1);
	});

	it("stays out of the way with a single project", () => {
		fixture.app.projects = [{ id: "app", name: "App" }];
		act(() => {
			renderer = create(<ProjectSwitcher />);
		});
		expect(renderer!.toJSON()).toBeNull();
	});

	// iOS's SwiftUI menu takes the tap. On Android the trigger holds the touch,
	// so it opens the dropdown itself and ripples.
	it("opens the dropdown from its own trigger on Android", () => {
		fixture.platform.OS = "android";
		const trigger = render().findByType("Pressable" as never);
		expect(trigger.props.accessibilityRole).toBe("button");
		act(() => trigger.props.onPress());
		expect(fixture.show).toHaveBeenCalledTimes(1);
	});
});
