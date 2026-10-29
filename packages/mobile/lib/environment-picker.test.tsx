import { act, create, type ReactTestInstance, type ReactTestRenderer } from "react-test-renderer";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const select = vi.hoisted(() => vi.fn());

vi.mock("./haptics", () => ({ haptics: { select } }));
vi.mock("./ThemeProvider", () => ({
	useTheme: () => new Proxy({}, { get: (_, key) => `#${String(key)}` }),
	useThemeState: () => ({ scheme: "dark" }),
}));
vi.mock("./toolbarIcon.android", () => ({ materialDrawable: (name: string) => `drawable:${name}` }));
vi.mock("./machine-menu-image.android", () => ({ machineMenuImage: (symbol: string) => `machine:${symbol}` }));
vi.mock("@expo/ui/swift-ui", async () => {
	const React = await import("react");
	const host = (name: string) => ({ children, ...props }: { children?: React.ReactNode }) => React.createElement(name, props, children);
	return { Host: host("SwiftHost"), Menu: host("SwiftMenu"), Section: host("SwiftSection"), Toggle: host("SwiftToggle") };
});
vi.mock("@expo/ui/swift-ui/modifiers", () => ({
	buttonStyle: (style: string) => ({ buttonStyle: style }),
	controlSize: (size: string) => ({ controlSize: size }),
	tint: (color: string) => ({ tint: color }),
}));
vi.mock("@expo/ui/jetpack-compose", async () => {
	const React = await import("react");
	const host = (name: string) => ({ children, ...props }: { children?: React.ReactNode }) => React.createElement(name, props, children);
	return {
		Host: host("ComposeHost"),
		DropdownMenu: Object.assign(host("DropdownMenu"), { Trigger: host("TriggerSlot"), Items: host("ItemsSlot") }),
		DropdownMenuItem: Object.assign(host("DropdownMenuItem"), { Text: host("TextSlot"), LeadingIcon: host("LeadingIconSlot"), TrailingIcon: host("TrailingIconSlot") }),
		Icon: host("Icon"),
		OutlinedButton: host("OutlinedButton"),
		Text: host("ComposeText"),
	};
});
vi.mock("@expo/ui/jetpack-compose/modifiers", () => ({
	padding: (...edges: number[]) => ({ padding: edges }),
	testID: (tag: string) => ({ testID: tag }),
}));

import { EnvironmentPicker } from "./environment-picker";
import { EnvironmentPicker as AndroidEnvironmentPicker } from "./environment-picker.android";

const environments = [{ id: "mac", name: "Mac", appearance: { symbol: "laptop" as const } }, { id: "dev", name: "Dev box" }];
let renderer: ReactTestRenderer | undefined;

beforeEach(() => {
	vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true);
	select.mockClear();
});
afterEach(() => {
	act(() => renderer?.unmount());
	renderer = undefined;
	vi.unstubAllGlobals();
});

function render(element: React.ReactElement): ReactTestInstance {
	act(() => {
		renderer = create(element);
	});
	return renderer!.root;
}

describe("EnvironmentPicker on iOS", () => {
	it("is a bordered SwiftUI menu button", () => {
		const menu = render(<EnvironmentPicker environments={environments} selectedEnvironmentId="mac" onSelectEnvironment={vi.fn()} />).findByType("SwiftMenu" as never);
		expect(menu.props.label).toBe("Switch environment");
		expect(menu.props.modifiers).toContainEqual({ buttonStyle: "bordered" });
	});

	// The system menu draws the check on the current machine.
	it("lists every environment with its machine symbol and a check on the current one", () => {
		const toggles = render(<EnvironmentPicker environments={environments} selectedEnvironmentId="mac" onSelectEnvironment={vi.fn()} />).findAllByType("SwiftToggle" as never);
		expect(toggles.map((toggle) => [toggle.props.label, toggle.props.systemImage, toggle.props.isOn])).toEqual([
			["All machines", "server.rack", false],
			["Mac", "laptopcomputer", true],
			["Dev box", "server.rack", false],
		]);
	});

	it("switches to a new environment, and ignores the current one", async () => {
		const onSelectEnvironment = vi.fn();
		const [all, mac] = render(<EnvironmentPicker environments={environments} selectedEnvironmentId="mac" onSelectEnvironment={onSelectEnvironment} />).findAllByType("SwiftToggle" as never);
		await act(async () => mac.props.onIsOnChange(false));
		expect(onSelectEnvironment).not.toHaveBeenCalled();
		await act(async () => all.props.onIsOnChange(true));
		expect(onSelectEnvironment).toHaveBeenCalledWith("all");
		expect(select).toHaveBeenCalledTimes(1);
	});
});

describe("EnvironmentPicker on Android", () => {
	it("is Material's outlined button opening a dropdown", () => {
		const root = render(<AndroidEnvironmentPicker environments={environments} selectedEnvironmentId="mac" onSelectEnvironment={vi.fn()} />);
		const menu = root.findByType("DropdownMenu" as never);
		expect(menu.props.expanded).toBe(false);
		const button = root.findByType("OutlinedButton" as never);
		expect(button.findByType("ComposeText" as never).props.children).toBe("Switch environment");
		act(() => button.props.onClick());
		expect(root.findByType("DropdownMenu" as never).props.expanded).toBe(true);
		act(() => root.findByType("DropdownMenu" as never).props.onDismissRequest());
		expect(root.findByType("DropdownMenu" as never).props.expanded).toBe(false);
	});

	// Checked with a glyph TalkBack names, not by colour.
	it("checks the current environment", () => {
		const items = render(<AndroidEnvironmentPicker environments={environments} selectedEnvironmentId="mac" onSelectEnvironment={vi.fn()} />).findAllByType("DropdownMenuItem" as never);
		expect(items.map((item) => item.findByType("TextSlot" as never).findByType("ComposeText" as never).props.children)).toEqual(["All machines", "Mac", "Dev box"]);
		expect(items.map((item) => item.findAllByType("TrailingIconSlot" as never).length)).toEqual([0, 1, 0]);
		expect(items[1].findByType("TrailingIconSlot" as never).findByType("Icon" as never).props).toMatchObject({ source: "drawable:check", contentDescription: "Current" });
	});

	// The same identity iOS shows, as Material Symbols; decorative beside the name.
	it("leads every environment with its machine's symbol", () => {
		const items = render(<AndroidEnvironmentPicker environments={environments} selectedEnvironmentId="mac" onSelectEnvironment={vi.fn()} />).findAllByType("DropdownMenuItem" as never);
		const glyphs = items.map((item) => item.findByType("LeadingIconSlot" as never).findByType("Icon" as never).props);
		expect(glyphs.map((glyph) => glyph.source)).toEqual(["machine:server", "machine:laptop", "machine:server"]);
		expect(glyphs.every((glyph) => glyph.contentDescription === undefined)).toBe(true);
	});

	it("closes the menu and switches to a new environment", async () => {
		const onSelectEnvironment = vi.fn();
		const root = render(<AndroidEnvironmentPicker environments={environments} selectedEnvironmentId="mac" onSelectEnvironment={onSelectEnvironment} />);
		act(() => root.findByType("OutlinedButton" as never).props.onClick());
		await act(async () => root.findAllByType("DropdownMenuItem" as never)[2].props.onClick());
		expect(onSelectEnvironment).toHaveBeenCalledWith("dev");
		expect(root.findByType("DropdownMenu" as never).props.expanded).toBe(false);
		act(() => root.findByType("OutlinedButton" as never).props.onClick());
		await act(async () => root.findAllByType("DropdownMenuItem" as never)[1].props.onClick());
		expect(onSelectEnvironment).toHaveBeenCalledTimes(1);
		expect(select).toHaveBeenCalledTimes(1);
	});
});
