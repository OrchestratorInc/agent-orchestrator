import { act, create, type ReactTestInstance, type ReactTestRenderer } from "react-test-renderer";
import { afterEach, beforeEach, expect, it, vi } from "vitest";

const fixtures = vi.hoisted(() => ({ select: vi.fn() }));

vi.mock("./ThemeProvider", () => ({ useTheme: () => ({ textPrimary: "#primary", textSecondary: "#secondary" }), useThemeState: () => ({ scheme: "dark" }) }));
vi.mock("./haptics", () => ({ haptics: { select: fixtures.select } }));
vi.mock("react-native", () => ({ Platform: { OS: "ios", Version: "26.0" }, StyleSheet: { create: (styles: object) => styles } }));
vi.mock("expo-router", async () => {
	const React = await import("react");
	const host = (name: string) => ({ children, ...props }: { children?: React.ReactNode }) => React.createElement(name, props, children);
	return { Stack: { Toolbar: { View: host("StackToolbarView") } } };
});
vi.mock("@expo/ui", async () => {
	const React = await import("react");
	return { Host: ({ children, ...props }: { children?: React.ReactNode }) => React.createElement("Host", props, children) };
});
vi.mock("@expo/ui/swift-ui", async () => {
	const React = await import("react");
	const host = (name: string) => ({ children, ...props }: { children?: React.ReactNode }) => React.createElement(name, props, children);
	return { HStack: host("HStack"), Image: host("Image"), Label: host("Label"), Menu: host("Menu"), Section: host("Section"), Text: host("Text"), Toggle: host("Toggle") };
});
vi.mock("@expo/ui/swift-ui/modifiers", () => {
	const record = (name: string) => (...args: unknown[]) => ({ modifier: name, args });
	return {
		...Object.fromEntries([
			"accessibilityIdentifier", "accessibilityLabel", "backgroundOverlay", "buttonStyle", "clipShape", "contentShape", "disabled", "font", "frame",
			"lineLimit", "menuOrder", "padding", "tint", "truncationMode",
		].map((name) => [name, record(name)])),
		shapes: { rectangle: () => ({ shape: "rectangle" }) },
	};
});

import { workerFilterMenu } from "./worker-filter-menu.ios";

let renderer: ReactTestRenderer | undefined;

beforeEach(() => {
	vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true);
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

const options = (overrides: Partial<Parameters<typeof workerFilterMenu>[0]> = {}) => ({
	selectedEnvironmentId: "all",
	projects: [{ id: "ao", name: "AO", hostId: "mac" }],
	selectedProjectId: "all",
	onSelectProject: vi.fn(),
	badgeColor: "#accent",
	...overrides,
});

const modifier = (node: ReactTestInstance, name: string) =>
	(node.props.modifiers as { modifier: string; args: unknown[] }[]).find((entry) => entry.modifier === name);

// A bar button's frame is under 44pt and UIKit offers no way to grow it, so the
// filter is our own view with a 44pt target: the circled glyph, nothing behind it.
it("is the circled filter glyph in the bar's own ink, no glass or fill, named for VoiceOver", async () => {
	await act(async () => { renderer = create(workerFilterMenu(options())); });
	expect(renderer!.root.findByType("StackToolbarView" as never).props.hidesSharedBackground).toBe(true);
	expect(renderer!.root.findByType("Host" as never).props.style).toEqual({ width: 44, height: 44 });
	const menu = renderer!.root.findAllByType("Menu" as never)[0];
	expect(modifier(menu, "accessibilityLabel")?.args).toEqual(["Filter workers"]);
	const face = menu.props.label;
	expect(face.props).toMatchObject({ systemName: "line.3.horizontal.decrease.circle", size: 24, color: "#primary" });
	expect(face.props.modifiers).toContainEqual({ modifier: "frame", args: [{ width: 44, height: 44 }] });
	const names = (face.props.modifiers as { modifier: string }[]).map((entry) => entry.modifier);
	expect(names).not.toContain("backgroundOverlay");
	expect(names).not.toContain("clipShape");
});

// A chosen machine is the chip row's to show; the filter names its project.
it("fills the glyph and names the project once one filters the board", async () => {
	await act(async () => { renderer = create(workerFilterMenu(options({ selectedEnvironmentId: "mac" }))); });
	expect(renderer!.root.findAllByType("Menu" as never)[0].props.label.props.systemName).toBe("line.3.horizontal.decrease.circle");
	await act(async () => { renderer!.update(workerFilterMenu(options({ selectedEnvironmentId: "mac", selectedProjectId: JSON.stringify(["mac", "ao"]) }))); });
	const menu = renderer!.root.findAllByType("Menu" as never)[0];
	expect(menu.props.label.props.systemName).toBe("line.3.horizontal.decrease.circle.fill");
	expect(modifier(menu, "accessibilityLabel")?.args).toEqual(["Filter workers, filtered to AO"]);
});

// Machines are the chip row's; the menu is the projects, one tap each.
it("lists the projects directly, with no machine section", async () => {
	const props = options();
	await act(async () => { renderer = create(workerFilterMenu(props)); });
	const sections = renderer!.root.findAllByType("Section" as never);
	expect(sections.map((section) => section.props.title)).toEqual(["Project"]);
	expect(renderer!.root.findAllByType("Menu" as never)).toHaveLength(1);
	const choice = (label: string) => renderer!.root.findAllByType("Toggle" as never).find((node) => node.props.label === label)!;
	expect(choice("Mac")).toBeUndefined();
	await act(async () => { choice("AO").props.onIsOnChange(true); });
	// Projects are keyed by their machine too.
	expect(props.onSelectProject).toHaveBeenCalledWith(JSON.stringify(["mac", "ao"]));
	expect(fixtures.select).toHaveBeenCalledTimes(1);
});
