import { act, create, type ReactTestRenderer } from "react-test-renderer";
import { describe, expect, it, vi } from "vitest";

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true, IS_REACT_NATIVE_TEST_ENVIRONMENT: true });

const { alert, haptics } = vi.hoisted(() => ({
	alert: vi.fn(),
	haptics: { tap: vi.fn(), select: vi.fn(), warning: vi.fn() },
}));

vi.mock("react-native", () => ({
	ActivityIndicator: "ActivityIndicator",
	Alert: { alert },
	Pressable: "Pressable",
	StyleSheet: { create: (styles: unknown) => styles, hairlineWidth: 1 },
	Text: "Text",
	View: "View",
}));
vi.mock("./icons", () => ({ Feather: "Feather" }));
vi.mock("./haptics", () => ({ haptics }));
vi.mock("./ThemeProvider", () => ({
	useTheme: () => ({ accent: "blue", bgElevatedHover: "hover", bgSubtle: "subtle", borderSubtle: "border", green: "green", textFaint: "faint", textPrimary: "white", textSecondary: "gray", textTertiary: "dim" }),
	useThemedStyles: (factory: (theme: Record<string, string>) => unknown) => factory({ accent: "blue", bgElevatedHover: "hover", bgSubtle: "subtle", borderSubtle: "border", green: "green", textFaint: "faint", textPrimary: "white", textSecondary: "gray", textTertiary: "dim" }),
}));

import { MachineSelector } from "./MachineSelector";

const hosts = [
	{ id: "prod", name: "Prod" },
	{ id: "dev", name: "Dev" },
] as never;

function render(overrides: Partial<React.ComponentProps<typeof MachineSelector>> = {}) {
	let renderer!: ReactTestRenderer;
	act(() => {
		renderer = create(<MachineSelector
			hosts={hosts}
			selectedHostId="dev"
			statusLabel="Connected"
			statusColor="green"
			switchingHostId={null}
			onSelect={vi.fn()}
			{...overrides}
		/>);
	});
	return renderer;
}

describe("MachineSelector", () => {
	it("uses the machine name as the trigger and places a Default pill beside it", () => {
		const renderer = render();

		expect(renderer.root.findByProps({ testID: "machine-selector-name" }).props.children).toBe("Dev");
		expect(renderer.root.findByProps({ testID: "machine-selector-default" }).props.children).toBe("Default");
		expect(renderer.root.findByProps({ testID: "machine-selector-trigger" }).props.accessibilityState).toEqual({ expanded: false });
		expect(renderer.root.findAllByProps({ testID: "machine-selector-options" })).toHaveLength(0);
	});

	it("expands only saved machines, leaving adding a machine to Settings", () => {
		const renderer = render();

		act(() => renderer.root.findByProps({ testID: "machine-selector-trigger" }).props.onPress());

		const options = renderer.root.findByProps({ testID: "machine-selector-options" });
		expect(renderer.root.findByProps({ testID: "machine-selector-trigger" }).props.accessibilityState).toEqual({ expanded: true });
		expect(renderer.root.findByProps({ testID: "machine-selector-option-prod" }).props.accessibilityState).toEqual({ selected: false, disabled: false });
		expect(renderer.root.findByProps({ testID: "machine-selector-option-dev" }).props.accessibilityState).toEqual({ selected: true, disabled: false });
		expect(options.findAllByProps({ testID: "machine-selector-add" })).toHaveLength(0);
	});

	it("selects a machine and collapses the dropdown", () => {
		const onSelect = vi.fn();
		const renderer = render({ onSelect });
		act(() => renderer.root.findByProps({ testID: "machine-selector-trigger" }).props.onPress());

		act(() => renderer.root.findByProps({ testID: "machine-selector-option-prod" }).props.onPress());

		expect(onSelect).toHaveBeenCalledWith("prod");
		expect(renderer.root.findAllByProps({ testID: "machine-selector-options" })).toHaveLength(0);
		expect(haptics.select).toHaveBeenCalled();
	});

	it("only selects a default, leaving edit and removal to Environments", () => {
		const renderer = render();
		act(() => renderer.root.findByProps({ testID: "machine-selector-trigger" }).props.onPress());
		const options = renderer.root.findByProps({ testID: "machine-selector-options" });
		expect(options.findAllByType("Pressable" as never)).toHaveLength(2);
		expect(options.findAllByType("Feather" as never).map((icon) => icon.props.name)).not.toContain("trash-2");
		expect(options.findAllByType("Feather" as never).map((icon) => icon.props.name)).not.toContain("edit-2");
	});

	it("does not put an add action inside the empty dropdown", () => {
		const renderer = render({ hosts: [], selectedHostId: null, selectedHostName: null });
		act(() => renderer.root.findByProps({ testID: "machine-selector-trigger" }).props.onPress());

		const options = renderer.root.findByProps({ testID: "machine-selector-options" });
		expect(options.children).toHaveLength(0);
	});
});
