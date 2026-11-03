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
vi.mock("@expo/ui/swift-ui/modifiers", () => ({ buttonStyle: () => ({}), controlSize: () => ({}), tint: () => ({}) }));
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
	// The system menu draws the check on the current machine.
	it("lists every environment with a check on the current one", () => {
		const root = render(<EnvironmentPicker environments={environments} selectedEnvironmentId="mac" onSelectEnvironment={vi.fn()} />);
		expect(root.findByType("SwiftMenu" as never).props.label).toBe("Switch machine");
		expect(root.findAllByType("SwiftToggle" as never).map((toggle) => [toggle.props.label, toggle.props.isOn])).toEqual([["All machines", false], ["Mac", true], ["Dev box", false]]);
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
	// Checked with a glyph TalkBack names, not by colour.
	it("checks the current environment", () => {
		const items = render(<AndroidEnvironmentPicker environments={environments} selectedEnvironmentId="mac" onSelectEnvironment={vi.fn()} />).findAllByType("DropdownMenuItem" as never);
		expect(items.map((item) => item.findByType("TextSlot" as never).findByType("ComposeText" as never).props.children)).toEqual(["All machines", "Mac", "Dev box"]);
		expect(items.map((item) => item.findAllByType("TrailingIconSlot" as never).length)).toEqual([0, 1, 0]);
		expect(items[1].findByType("TrailingIconSlot" as never).findByType("Icon" as never).props.contentDescription).toBe("Current");
	});

	it("opens from Switch machine, switches to a new environment and closes", async () => {
		const onSelectEnvironment = vi.fn();
		const root = render(<AndroidEnvironmentPicker environments={environments} selectedEnvironmentId="mac" onSelectEnvironment={onSelectEnvironment} />);
		const button = root.findByType("OutlinedButton" as never);
		expect(button.findByType("ComposeText" as never).props.children).toBe("Switch machine");
		act(() => button.props.onClick());
		expect(root.findByType("DropdownMenu" as never).props.expanded).toBe(true);
		act(() => root.findByType("DropdownMenu" as never).props.onDismissRequest());
		expect(root.findByType("DropdownMenu" as never).props.expanded).toBe(false);
		act(() => button.props.onClick());
		await act(async () => root.findAllByType("DropdownMenuItem" as never)[2].props.onClick());
		expect(onSelectEnvironment).toHaveBeenCalledWith("dev");
		expect(root.findByType("DropdownMenu" as never).props.expanded).toBe(false);
		act(() => button.props.onClick());
		await act(async () => root.findAllByType("DropdownMenuItem" as never)[1].props.onClick());
		expect(onSelectEnvironment).toHaveBeenCalledTimes(1);
		expect(select).toHaveBeenCalledTimes(1);
	});
});
